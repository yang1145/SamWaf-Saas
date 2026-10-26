package waftask

import (
	"SamWaf/common/uuid"
	"SamWaf/common/wafctx"
	"SamWaf/common/zlog"
	"SamWaf/customtype"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// 站点流量落库：把引擎侧累加的真实进出字节按天/小时增量写进统计库。
//
// 与 CollectStatsFromLogs 的分工（issue #930 后）：
//   - 本任务：只写 traffic_in / traffic_out，数据来自引擎字节计量，与日志无关；
//   - 日志聚合：只写 total/attack/normal_count 与 total_time_spent。
//
// 两边都用「先 Update 累加、影响 0 行再 Create」的写法，谁先建行都不冲突。

// trafficDayAgg 合并后的天级增量
type trafficDayAgg struct {
	HostCode string
	Host     string
	Day      int
	In       int64
	Out      int64
}

// trafficHourAgg 合并后的小时级增量
type trafficHourAgg struct {
	HostCode string
	Host     string
	HourTime int64
	In       int64
	Out      int64
}

// planTrafficUpserts 把 Drain 出来的桶合并成天级/小时级两组增量。
// 纯函数：同一天的多个整点桶会合并成一条天级增量，全零桶直接丢弃。
func planTrafficUpserts(list []global.TrafficSnapshot) ([]trafficDayAgg, []trafficHourAgg) {
	type dayKey struct {
		HostCode string
		Day      int
	}
	type hourKey struct {
		HostCode string
		HourTime int64
	}
	dayMap := make(map[dayKey]*trafficDayAgg)
	hourMap := make(map[hourKey]*trafficHourAgg)
	var days []*trafficDayAgg
	var hours []*trafficHourAgg

	for _, s := range list {
		if s.HostCode == "" || (s.In <= 0 && s.Out <= 0) {
			continue
		}
		dk := dayKey{HostCode: s.HostCode, Day: s.Day}
		d := dayMap[dk]
		if d == nil {
			d = &trafficDayAgg{HostCode: s.HostCode, Host: s.Host, Day: s.Day}
			dayMap[dk] = d
			days = append(days, d)
		}
		d.In += s.In
		d.Out += s.Out

		hk := hourKey{HostCode: s.HostCode, HourTime: s.HourTime}
		h := hourMap[hk]
		if h == nil {
			h = &trafficHourAgg{HostCode: s.HostCode, Host: s.Host, HourTime: s.HourTime}
			hourMap[hk] = h
			hours = append(hours, h)
		}
		h.In += s.In
		h.Out += s.Out
	}

	dayList := make([]trafficDayAgg, 0, len(days))
	for _, d := range days {
		dayList = append(dayList, *d)
	}
	hourList := make([]trafficHourAgg, 0, len(hours))
	for _, h := range hours {
		hourList = append(hourList, *h)
	}
	return dayList, hourList
}

// trafficHostOwner 一个站点的归属租户（从核心库 hosts 表解析）
type trafficHostOwner struct {
	TenantID string
	UserCode string
}

// loadTrafficHostOwners 用平台作用域一次性读出全部站点的归属映射（host_code → 租户）。
//
// 引擎侧流量计量的桶只带 host_code（热路径不碰库），落库前必须回核心库解析归属租户，
// 才能把字节记到各自租户的统计行上；平台作用域跳过租户回调，保证能看到所有租户的站点。
// 读取失败返回错误：调用方不得取走内存增量，保留下轮重试，绝不带着空归属瞎写。
func loadTrafficHostOwners() (map[string]trafficHostOwner, error) {
	if global.GWAF_LOCAL_DB == nil {
		return nil, errors.New("核心库未初始化")
	}
	var rows []struct {
		Code     string
		TenantID string
		UserCode string
	}
	res := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(context.Background())).
		Model(&model.Hosts{}).
		Select("code, tenant_id, user_code").
		Scan(&rows)
	if res.Error != nil {
		return nil, res.Error
	}
	owners := make(map[string]trafficHostOwner, len(rows))
	for _, r := range rows {
		// 归属不全的站点按"无法解析"处理：宁可跳过告警，也不能并进平台桶串租户
		if r.Code == "" || r.TenantID == "" || r.UserCode == "" {
			continue
		}
		owners[r.Code] = trafficHostOwner{TenantID: r.TenantID, UserCode: r.UserCode}
	}
	return owners, nil
}

// TaskTrafficFlush 定时任务入口（默认 30s 一次）
func TaskTrafficFlush() {
	FlushTrafficStats()
}

// FlushTrafficStats 把内存里累计的流量落库。
// 库没就绪、正在切库或站点归属解析失败时**不取走**内存里的增量，等下个周期，
// 避免把字节丢在切库/解析失败窗口里。
func FlushTrafficStats() {
	if global.GWAF_LOCAL_STATS_DB == nil {
		return
	}
	if global.GDATA_CURRENT_CHANGE {
		zlog.Debug("流量统计落库", "正在切换数据库，本轮跳过")
		return
	}

	// 归属解析先行：解析不了就不 Drain，增量原样留在内存里下轮重试
	owners, err := loadTrafficHostOwners()
	if err != nil {
		zlog.Warn("流量统计落库：站点归属解析失败，本轮跳过等待重试", "错误", err.Error())
		return
	}

	list := global.DrainTraffic()
	if len(list) == 0 {
		return
	}

	if err := writeTrafficStats(global.GWAF_LOCAL_STATS_DB, list, owners); err != nil {
		// 整笔事务回滚了，把增量放回累加器下轮重试，绝不静默丢数
		global.RestoreTraffic(list)
		zlog.Error("流量统计落库失败，已退回内存等待重试", "错误", err.Error(), "桶数", len(list))
		return
	}
	zlog.Debug("流量统计落库完成", "桶数", len(list))
}

// writeTrafficStats 单事务写入：要么全成，要么全退（避免部分成功后重试造成重复计数）。
// 每个桶先按 host_code 从 owners 解析归属租户，UPSERT 的 WHERE 与新建行的 BaseOrm
// 都盖该租户的标识；归属未知的桶跳过并告警（严禁落进平台全局桶，防止跨租户串数据）。
func writeTrafficStats(db *gorm.DB, list []global.TrafficSnapshot, owners map[string]trafficHostOwner) error {
	days, hours := planTrafficUpserts(list)
	if len(days) == 0 && len(hours) == 0 {
		return nil
	}
	now := customtype.JsonTime(time.Now())

	return db.Transaction(func(tx *gorm.DB) error {
		for _, d := range days {
			owner, ok := owners[d.HostCode]
			if !ok {
				zlog.Warn("流量统计落库：站点归属租户未知，跳过该天级桶", "host_code", d.HostCode, "day", d.Day)
				continue
			}
			res := tx.Model(&model.StatsSiteDay{}).
				Where("tenant_id = ? and user_code = ? and host_code = ? and day = ?",
					owner.TenantID, owner.UserCode, d.HostCode, d.Day).
				Updates(map[string]interface{}{
					"traffic_in":  gorm.Expr("traffic_in + ?", d.In),
					"traffic_out": gorm.Expr("traffic_out + ?", d.Out),
					"update_time": now,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				if err := tx.Create(&model.StatsSiteDay{
					BaseOrm: baseorm.BaseOrm{
						Id:          uuid.GenUUID(),
						USER_CODE:   owner.UserCode,
						Tenant_ID:   owner.TenantID,
						CREATE_TIME: now,
						UPDATE_TIME: now,
					},
					HostCode:   d.HostCode,
					Day:        d.Day,
					Host:       d.Host,
					TrafficIn:  d.In,
					TrafficOut: d.Out,
				}).Error; err != nil {
					return err
				}
			}
		}

		for _, h := range hours {
			owner, ok := owners[h.HostCode]
			if !ok {
				zlog.Warn("流量统计落库：站点归属租户未知，跳过该小时级桶", "host_code", h.HostCode, "hour_time", h.HourTime)
				continue
			}
			res := tx.Model(&model.StatsSiteHour{}).
				Where("tenant_id = ? and user_code = ? and host_code = ? and hour_time = ?",
					owner.TenantID, owner.UserCode, h.HostCode, h.HourTime).
				Updates(map[string]interface{}{
					"traffic_in":  gorm.Expr("traffic_in + ?", h.In),
					"traffic_out": gorm.Expr("traffic_out + ?", h.Out),
					"update_time": now,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				if err := tx.Create(&model.StatsSiteHour{
					BaseOrm: baseorm.BaseOrm{
						Id:          uuid.GenUUID(),
						USER_CODE:   owner.UserCode,
						Tenant_ID:   owner.TenantID,
						CREATE_TIME: now,
						UPDATE_TIME: now,
					},
					HostCode:   h.HostCode,
					HourTime:   h.HourTime,
					Host:       h.Host,
					TrafficIn:  h.In,
					TrafficOut: h.Out,
				}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
