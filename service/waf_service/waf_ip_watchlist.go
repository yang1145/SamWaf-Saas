package waf_service

import (
	"SamWaf/common/uuid"
	"SamWaf/common/wafctx"
	"SamWaf/customtype"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/request"
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"gorm.io/gorm"
)

type WafIPWatchlistService struct{}

var WafIPWatchlistServiceApp = new(WafIPWatchlistService)

const (
	watchDefaultDays  = 7
	watchMaxDays      = 30
	watchReasonMaxLen = 255
)

// AddApi 加入观察名单；已存在则续期（到期时间取更晚者）并更新原因。
// 名单是租户级资源（ip_watchlists 租户内唯一）：查询/写入随请求 ctx 走租户
// 过滤与盖章，租户各维护各的观察名单，互不可见。
func (receiver *WafIPWatchlistService) AddApi(ctx context.Context, req request.WafIPWatchlistAddReq) error {
	ip := strings.TrimSpace(req.IP)
	if net.ParseIP(ip) == nil {
		return errors.New("IP 格式不正确")
	}
	days := req.Days
	if days <= 0 {
		days = watchDefaultDays
	}
	if days > watchMaxDays {
		days = watchMaxDays
	}
	reason := req.Reason
	if len(reason) > watchReasonMaxLen {
		reason = reason[:watchReasonMaxLen]
	}
	expireAt := time.Now().AddDate(0, 0, days).Unix()

	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	var existing model.IPWatchlist
	res := db.Where("ip = ?", ip).First(&existing)
	if res.Error == nil {
		fields := map[string]interface{}{
			"reason":      reason,
			"update_time": customtype.JsonTime(time.Now()),
		}
		if existing.ExpireAt < expireAt {
			fields["expire_at"] = expireAt
		}
		err := db.Model(&model.IPWatchlist{}).Where("id = ?", existing.Id).Updates(fields).Error
		global.GWAF_IP_WATCH.Reload()
		return err
	}
	// BaseOrm 显式值是无 ctx 调用方的兜底；请求 ctx 携带租户时由
	// before_create 回调覆盖为请求租户（ctx 权威）
	bean := &model.IPWatchlist{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   global.GWAF_TENANT_ID,
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},
		IP:       ip,
		ExpireAt: expireAt,
		Reason:   reason,
	}
	err := db.Create(bean).Error
	global.GWAF_IP_WATCH.Reload()
	return err
}

// DelApi 移出观察名单（随请求 ctx 的租户过滤，只删本租户的行）
func (receiver *WafIPWatchlistService) DelApi(ctx context.Context, ip string) error {
	err := global.GWAF_LOCAL_DB.WithContext(ctx).
		Where("ip = ?", strings.TrimSpace(ip)).Delete(&model.IPWatchlist{}).Error
	global.GWAF_IP_WATCH.Reload()
	return err
}

// ListApi 名单分页（顺带清掉已到期行）。
// 到期清理是平台级巡检语义（PlatformScope，不分租户）；列表查询随请求租户过滤。
func (receiver *WafIPWatchlistService) ListApi(ctx context.Context, req request.WafIPWatchlistSearch) ([]model.IPWatchlist, int64, error) {
	var list []model.IPWatchlist
	var total int64
	now := time.Now().Unix()
	global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(context.Background())).
		Where("expire_at <= ?", now).Delete(&model.IPWatchlist{})
	build := func() *gorm.DB {
		q := global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.IPWatchlist{}).
			Where("expire_at > ?", now)
		if strings.TrimSpace(req.IP) != "" {
			q = q.Where("ip like ?", "%"+strings.TrimSpace(req.IP)+"%")
		}
		return q
	}
	build().Count(&total)
	err := build().Order("expire_at desc").
		Limit(req.PageSize).Offset(req.PageSize * (req.PageIndex - 1)).Find(&list).Error
	return list, total, err
}
