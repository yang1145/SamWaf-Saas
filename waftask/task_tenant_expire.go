package waftask

import (
	"SamWaf/common/wafctx"
	"SamWaf/common/zlog"
	"SamWaf/customtype"
	"SamWaf/enums"
	"SamWaf/global"
	"SamWaf/innerbean"
	"SamWaf/model"
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// tenantExpireTaskMethod 定时任务方法名（tasks.task_method）。
// enums/task_enum.go 的系统任务清单已定稿不再扩充，平台侧任务的方法名常量收敛在本包内。
const tenantExpireTaskMethod = "task_tenant_expire"

// tenantExpireRemindDays 剩余天数命中这些阈值时发到期提醒（每日任务，每个阈值恰好触发一次）
var tenantExpireRemindDays = map[int]bool{7: true, 3: true, 1: true}

func init() {
	// 包内自注册：main.go 调 waftask.NewTaskRegistry() 时由 NewTaskRegistry 统一挂载，
	// 无需再改 cmd/samwaf/main.go 的注册清单
	extraTaskFuncs[tenantExpireTaskMethod] = TaskTenantExpire
}

// TaskTenantExpire 租户到期巡检（每日一次，平台视角遍历全部租户）：
//   - Active 且已过到期时间 → 状态推进为 Expired（落库），并失效该租户的请求期策略缓存
//     （缓存里的旧快照还是 Active，不清的话请求期最迟要等 5 分钟 TTL 才按 Expired 判定）
//   - 未到期且剩余天数恰为 7/3/1 → 经系统消息队列发到期提醒
//     （剩余天数 = 距到期时间的向上取整天数）
func TaskTenantExpire() {
	innerLogName := "TaskTenantExpire"
	if global.GWAF_SHUTDOWN_SIGNAL {
		zlog.Info(innerLogName + " - Shutdown")
		return
	}
	// 平台视角：租户注册表虽平台豁免，这里仍显式声明平台作用域，语义与读全量租户一致
	ctx := wafctx.PlatformScope(context.Background())
	var tenants []model.Tenant
	if err := global.GWAF_LOCAL_DB.WithContext(ctx).Find(&tenants).Error; err != nil {
		zlog.Error(fmt.Sprintf("%s 读取租户列表失败: %v", innerLogName, err))
		return
	}
	now := time.Now()
	for _, tenant := range tenants {
		if global.GWAF_SHUTDOWN_SIGNAL {
			zlog.Info(innerLogName + " - Shutdown detected")
			return
		}
		if tenant.Status != enums.TenantStatusActive {
			continue
		}
		expireTime := strings.TrimSpace(tenant.ExpireTime)
		if expireTime == "" {
			continue // 空=永不过期
		}
		expire, err := time.ParseInLocation("2006-01-02 15:04:05", expireTime, time.Local)
		if err != nil {
			zlog.Warn(fmt.Sprintf("%s 租户[%s](%s) 到期时间格式非法(%s)，跳过", innerLogName, tenant.TenantName, tenant.TenantID, expireTime))
			continue
		}
		if now.After(expire) {
			// 推进 Active → Expired（平台作用域写路径不过滤，按主键定位）
			if uerr := global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Tenant{}).
				Where("id = ?", tenant.Id).
				Updates(map[string]interface{}{
					"status":      enums.TenantStatusExpired,
					"UPDATE_TIME": customtype.JsonTime(now),
				}).Error; uerr != nil {
				zlog.Error(fmt.Sprintf("%s 租户[%s](%s) 推进过期状态失败: %v", innerLogName, tenant.TenantName, tenant.TenantID, uerr))
				continue
			}
			global.GCACHE_WAFCACHE.Remove(enums.CACHE_TENANT_STATUS + tenant.TenantID)
			zlog.Info(fmt.Sprintf("%s 租户[%s](%s) 已到到期时间(%s)，状态推进为已过期", innerLogName, tenant.TenantName, tenant.TenantID, expireTime))
			continue
		}
		// 未到期：剩余天数恰为 7/3/1 时提醒（每日一次的任务，每个阈值恰好命中一天）
		remainDays := int(math.Ceil(expire.Sub(now).Hours() / 24))
		if !tenantExpireRemindDays[remainDays] {
			continue
		}
		if global.GQEQUE_MESSAGE_DB == nil {
			continue // 消息队列未就绪（极端时序），跳过本轮提醒
		}
		global.GQEQUE_MESSAGE_DB.Enqueue(innerbean.SystemErrorMessageInfo{
			ErrorType: "租户到期提醒",
			ErrorMsg: fmt.Sprintf("租户[%s](%s)将于 %d 天后到期(%s)，请联系平台管理员续期",
				tenant.TenantName, tenant.TenantID, remainDays, expireTime),
			Time: now.Format("2006-01-02 15:04:05"),
		})
		zlog.Info(fmt.Sprintf("%s 已发送租户[%s](%s)的到期提醒，剩余 %d 天", innerLogName, tenant.TenantName, tenant.TenantID, remainDays))
	}
}
