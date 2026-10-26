package waf_service

import (
	"SamWaf/common/wafctx"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/request"
	response2 "SamWaf/model/response"
	"context"
	"fmt"

	"gorm.io/gorm"
)

type WafAIService struct{}

var WafAIServiceApp = new(WafAIService)

// AIScopeCtx AI 语料/标注的读取作用域：平台侧（无租户上下文或平台自身租户）看全租户——
// AI 模型是平台级能力，训练语料必须覆盖所有租户；租户侧只看自己的日志。
func AIScopeCtx(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if tid, _, ok := wafctx.TenantFrom(ctx); ok && tid != "" && tid != global.GWAF_TENANT_ID {
		return ctx
	}
	return wafctx.PlatformScope(ctx)
}

// AIScopeTenant 与 AIScopeCtx 对应：标注行/租户条件应落的值。
func AIScopeTenant(ctx context.Context) (tenantID, userCode string) {
	if ctx != nil {
		if tid, ucode, ok := wafctx.TenantFrom(ctx); ok && tid != "" && tid != global.GWAF_TENANT_ID {
			if ucode == "" {
				ucode = global.GWAF_USER_CODE
			}
			return tid, ucode
		}
	}
	return global.GWAF_TENANT_ID, global.GWAF_USER_CODE
}

// DashboardApi 聚合 AI 检测看板数据：按类别汇总、分数分布、observe/block 趋势。
// 数据源为 security_event 中 ai_score>0 的命中子集（AI 命中的请求必是安全事件），按 day 范围/站点过滤。
// 平台侧聚合全部租户（不能只看平台切片），租户侧只看自己的命中。
func (receiver *WafAIService) DashboardApi(ctx context.Context, req request.WafAIDashboardReq) response2.WafAIDashboard {
	var res response2.WafAIDashboard
	res.Categories = []response2.WafAINameValue{}
	res.Trend = []response2.WafAITrendPoint{}

	if global.GWAF_LOCAL_LOG_DB == nil {
		res.ScoreHist = buildEmptyScoreHist()
		return res
	}
	logDB := global.GWAF_LOCAL_LOG_DB.WithContext(AIScopeCtx(ctx))

	// 每次查询都用全新的 where 链，避免 GORM 条件被复用污染
	base := func() *gorm.DB {
		q := logDB.Model(&model.SecurityEvent{}).Where("ai_score > 0")
		if req.StartDay > 0 && req.EndDay > 0 {
			q = q.Where("day between ? and ?", req.StartDay, req.EndDay)
		}
		if req.HostCode != "" {
			q = q.Where("host_code = ?", req.HostCode)
		}
		return q
	}

	// 1) 按类别（rule）汇总
	base().Select("rule as name, count(*) as value").
		Group("rule").Order("value desc").Scan(&res.Categories)

	// 2) 分数分布直方图（10 桶：0.0-0.1 ... 0.9-1.0；score==1.0 归入最后一桶）
	type bucketRow struct {
		Bucket int
		Cnt    int64
	}
	var brows []bucketRow
	base().Select("cast(ai_score*10 as int) as bucket, count(*) as cnt").
		Group("bucket").Scan(&brows)
	counts := make([]int64, 10)
	for _, b := range brows {
		idx := b.Bucket
		if idx >= 10 {
			idx = 9 // score==1.0 归入 0.9-1.0
		}
		if idx < 0 {
			idx = 0
		}
		counts[idx] += b.Cnt
	}
	res.ScoreHist = make([]response2.WafAINameValue, 10)
	for i := 0; i < 10; i++ {
		res.ScoreHist[i] = response2.WafAINameValue{
			Name:  fmt.Sprintf("%.1f-%.1f", float64(i)/10, float64(i+1)/10),
			Value: counts[i],
		}
	}

	// 3) 按天 observe/block 趋势
	base().Select("day, sum(case when log_only_mode=1 then 1 else 0 end) as observe, sum(case when log_only_mode=0 then 1 else 0 end) as block").
		Group("day").Order("day asc").Scan(&res.Trend)

	// 4) 汇总总数
	for _, t := range res.Trend {
		res.ObserveCnt += t.Observe
		res.BlockCnt += t.Block
	}
	res.Total = res.ObserveCnt + res.BlockCnt

	return res
}

func buildEmptyScoreHist() []response2.WafAINameValue {
	hist := make([]response2.WafAINameValue, 10)
	for i := 0; i < 10; i++ {
		hist[i] = response2.WafAINameValue{Name: fmt.Sprintf("%.1f-%.1f", float64(i)/10, float64(i+1)/10)}
	}
	return hist
}
