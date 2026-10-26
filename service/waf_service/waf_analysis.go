package waf_service

import (
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/request"
	response2 "SamWaf/model/response"
	"context"
)

type WafAnalysisService struct{}

var WafAnalysisServiceApp = new(WafAnalysisService)

// StatAnalysisDayCountryRangeApi 通过时间获取国家级别的 攻击数 访问数（按请求租户过滤）。
// 用 Find 而非 Scan 收结果：Scan 走 gorm Row 回调链，不经过注册在 Query 链上的租户过滤。
func (receiver *WafAnalysisService) StatAnalysisDayCountryRangeApi(ctx context.Context, req request.WafStatsAnalysisDayRangeCountryReq) []response2.WafAnalysisDayStats {
	statsDB := global.GWAF_LOCAL_STATS_DB.WithContext(ctx)
	var CountOfRange []response2.WafAnalysisDayStats
	if req.AttackType == "" {
		statsDB.Model(&model.StatsIPCityDay{}).Where("day between ? and ? and country<>? and country<>? ",
			req.StartDay, req.EndDay, "0", "内网").Select(" country as Name ,sum(count) as Value").Group("country").Order("sum(count) desc").Find(&CountOfRange)

	} else {
		statsDB.Model(&model.StatsIPCityDay{}).Where("day between ? and ? and type = ?  and country<>? and country<>? ",
			req.StartDay, req.EndDay, req.AttackType, "0", "内网").Select(" country as Name ,sum(count) as Value").Group("country").Order("sum(count) desc").Find(&CountOfRange)

	}
	return CountOfRange
}

// AnalysisSpiderApi 爬虫分析
// 数据源是 access_log（分层后所有请求的窄行都在这里）；今天它也只看实时库，
// 与改造前只读 live web_logs 的口径一致。查询按请求租户过滤。
func (receiver *WafAnalysisService) AnalysisSpiderApi(ctx context.Context, req request.WafAnalysisSpiderReq) []response2.WafAnalysisSpiderResp {
	logDB := global.GWAF_LOCAL_LOG_DB.WithContext(ctx)
	var CountOfRange []response2.WafAnalysisSpiderResp
	if req.Host == "" {
		logDB.Model(&model.AccessLog{}).Where("day between ? and ? and  is_bot=1 ",
			req.StartDay, req.EndDay).Select(" guest_id_entification as Name ,count(1) as Value").
			Group("guest_id_entification").Order("count(1) desc").Find(&CountOfRange)

	} else {
		logDB.Model(&model.AccessLog{}).Where("day between ? and ? and  is_bot=1 and host_code = ? ",
			req.StartDay, req.EndDay, req.Host).
			Select(" guest_id_entification as Name ,count(1) as Value").
			Group("guest_id_entification").Order("count(1) desc").Find(&CountOfRange)
	}
	return CountOfRange
}
