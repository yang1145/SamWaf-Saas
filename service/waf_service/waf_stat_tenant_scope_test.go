package waf_service_test

// 流量落库盖章 + 统计读侧租户隔离的端到端用例。
//
// 外部测试包（waf_service_test）：需要驱动 waftask.FlushTrafficStats（waftask 引用
// waf_service，内部测试包再反过来 import waftask 会成环）。走生产同款入口：
// 引擎累加器 AddTraffic → FlushTrafficStats（平台作用域解析 host 归属后盖章落库），
// 再用各租户 ctx 调读侧接口断言隔离。

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"SamWaf/common/uuid"
	"SamWaf/common/wafctx"
	"SamWaf/customtype"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/request"
	"SamWaf/service/waf_service"
	"SamWaf/waftask"
	"SamWaf/wafdb"

	sqlite "github.com/samwafgo/sqlitedriver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openStatScopeDBs 临时核心库 + 统计库（生产同款租户回调），替换全局句柄。
// 全局租户指向不存在的 tenantZ：任何命中都只能来自显式租户 ctx / 站点归属解析。
func openStatScopeDBs(t *testing.T) (coreDB, statsDB *gorm.DB) {
	t.Helper()
	newDB := func(name string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), name)),
			&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatalf("打开测试库 %s 失败: %v", name, err)
		}
		wafdb.RegisterTenantScopeCallbacks(db)
		return db
	}
	coreDB = newDB("stat_scope_core.db")
	statsDB = newDB("stat_scope_stats.db")
	if err := coreDB.AutoMigrate(&model.Hosts{}); err != nil {
		t.Fatalf("核心库建表失败: %v", err)
	}
	if err := statsDB.AutoMigrate(&model.StatsSiteDay{}, &model.StatsSiteHour{}, &model.StatsIPDay{}); err != nil {
		t.Fatalf("统计库建表失败: %v", err)
	}

	oldCore, oldStats := global.GWAF_LOCAL_DB, global.GWAF_LOCAL_STATS_DB
	oldTenant, oldUser := global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	global.GWAF_LOCAL_DB, global.GWAF_LOCAL_STATS_DB = coreDB, statsDB
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"
	t.Cleanup(func() {
		global.GWAF_LOCAL_DB, global.GWAF_LOCAL_STATS_DB = oldCore, oldStats
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser
		global.DrainTraffic() // 清掉本用例残留的桶，避免污染其它测试
		for _, db := range []*gorm.DB{coreDB, statsDB} {
			if sqlDB, e := db.DB(); e == nil {
				_ = sqlDB.Close()
			}
		}
	})
	global.DrainTraffic()
	return coreDB, statsDB
}

// seedScopeHost 直插一个归属指定租户的站点（平台豁免查询 + 手工赋 BaseOrm）
func seedScopeHost(t *testing.T, db *gorm.DB, code, host, tenantID, userCode, remarks string) {
	t.Helper()
	now := customtype.JsonTime(time.Now())
	bean := model.Hosts{
		BaseOrm: baseorm.BaseOrm{Id: uuid.GenUUID(), USER_CODE: userCode, Tenant_ID: tenantID, CREATE_TIME: now, UPDATE_TIME: now},
		Code:    code, Host: host, Port: 80, REMARKS: remarks,
	}
	if err := db.WithContext(wafctx.PlatformScope(context.Background())).Create(&bean).Error; err != nil {
		t.Fatalf("插入站点 %s 失败: %v", code, err)
	}
}

// TestStatTenantScopeTrafficStamping 两个租户的站点各产生流量，落库必须盖各自租户的章；
// 归属未知的桶跳过；同桶二次落库走累加不重建行。
func TestStatTenantScopeTrafficStamping(t *testing.T) {
	coreDB, statsDB := openStatScopeDBs(t)
	pctx := wafctx.PlatformScope(context.Background())

	seedScopeHost(t, coreDB, "host-a", "a.example.com", "tenant-a", "userA", "A租户的站")
	seedScopeHost(t, coreDB, "host-b", "b.example.com", "tenant-b", "userB", "B租户的站")

	day, hour := global.TrafficBucketOf(time.Now())
	global.AddTraffic("host-a", "a.example.com", day, hour, 100, 200)
	global.AddTraffic("host-b", "b.example.com", day, hour, 300, 400)
	global.AddTraffic("host-ghost", "ghost.example.com", day, hour, 999, 999) // 归属未知
	waftask.FlushTrafficStats()

	var days []model.StatsSiteDay
	if err := statsDB.WithContext(pctx).Order("host_code").Find(&days).Error; err != nil {
		t.Fatalf("查询天级行失败: %v", err)
	}
	if len(days) != 2 {
		t.Fatalf("应只有 2 行天级统计（ghost 被跳过），实际 %d: %+v", len(days), days)
	}
	if days[0].HostCode != "host-a" || days[0].Tenant_ID != "tenant-a" || days[0].USER_CODE != "userA" ||
		days[0].TrafficIn != 100 || days[0].TrafficOut != 200 {
		t.Fatalf("host-a 天级行盖章/数值不符: %+v", days[0])
	}
	if days[1].HostCode != "host-b" || days[1].Tenant_ID != "tenant-b" || days[1].USER_CODE != "userB" ||
		days[1].TrafficIn != 300 || days[1].TrafficOut != 400 {
		t.Fatalf("host-b 天级行盖章/数值不符: %+v", days[1])
	}

	var hours []model.StatsSiteHour
	if err := statsDB.WithContext(pctx).Find(&hours).Error; err != nil {
		t.Fatalf("查询小时级行失败: %v", err)
	}
	if len(hours) != 2 {
		t.Fatalf("应只有 2 行小时级统计（ghost 被跳过），实际 %d", len(hours))
	}
	for _, h := range hours {
		if h.Tenant_ID == "" || h.USER_CODE == "" {
			t.Fatalf("小时级行没盖章: %+v", h)
		}
	}

	// 二次落库：同桶必须累加到原行（租户 WHERE 命中），不能另建行
	global.AddTraffic("host-a", "a.example.com", day, hour, 50, 50)
	waftask.FlushTrafficStats()
	var rowA model.StatsSiteDay
	if err := statsDB.WithContext(pctx).Where("host_code = ?", "host-a").First(&rowA).Error; err != nil {
		t.Fatalf("回读 host-a 失败: %v", err)
	}
	if rowA.TrafficIn != 150 || rowA.TrafficOut != 250 {
		t.Fatalf("累加后应为 150/250，实际 %d/%d", rowA.TrafficIn, rowA.TrafficOut)
	}
	var cnt int64
	if err := statsDB.WithContext(pctx).Model(&model.StatsSiteDay{}).Count(&cnt).Error; err != nil || cnt != 2 {
		t.Fatalf("二次落库不该新增行，实际 %d 行 err=%v", cnt, err)
	}
}

// TestStatTenantScopeOverviewIsolation 概览读侧：租户 A/B 各见其站，平台作用域见全部。
func TestStatTenantScopeOverviewIsolation(t *testing.T) {
	coreDB, statsDB := openStatScopeDBs(t)
	pctx := wafctx.PlatformScope(context.Background())

	seedScopeHost(t, coreDB, "host-a", "a.example.com", "tenant-a", "userA", "A租户的站")
	seedScopeHost(t, coreDB, "host-b", "b.example.com", "tenant-b", "userB", "B租户的站")

	day, hour := global.TrafficBucketOf(time.Now())
	global.AddTraffic("host-a", "a.example.com", day, hour, 100, 200)
	global.AddTraffic("host-b", "b.example.com", day, hour, 300, 400)
	waftask.FlushTrafficStats()

	_ = statsDB // 落库断言见 TrafficStamping 用例，这里专注读侧
	dayStr := time.Now().Format("20060102")
	req := request.WafStatsSiteOverviewReq{StartDay: dayStr, EndDay: dayStr}
	svc := waf_service.WafStatServiceApp

	ctxA := wafctx.WithTenant(context.Background(), "tenant-a", "userA")
	ovA, err := svc.StatSiteOverviewApi(ctxA, req)
	if err != nil {
		t.Fatalf("租户A概览查询失败: %v", err)
	}
	if len(ovA.SiteList) != 1 || ovA.SiteList[0].HostCode != "host-a" {
		t.Fatalf("租户A应只看到 host-a，实际: %+v", ovA.SiteList)
	}
	if ovA.SiteList[0].HostRemark != "A租户的站" {
		t.Fatalf("租户A应回填自己的站点备注，实际 %q", ovA.SiteList[0].HostRemark)
	}
	if ovA.TotalPv != 0 { // 流量路径不写 PV，此断言防止误读
		t.Fatalf("流量落库不该产生 PV，实际 %d", ovA.TotalPv)
	}

	ctxB := wafctx.WithTenant(context.Background(), "tenant-b", "userB")
	ovB, err := svc.StatSiteOverviewApi(ctxB, req)
	if err != nil {
		t.Fatalf("租户B概览查询失败: %v", err)
	}
	if len(ovB.SiteList) != 1 || ovB.SiteList[0].HostCode != "host-b" {
		t.Fatalf("租户B应只看到 host-b，实际: %+v", ovB.SiteList)
	}
	if ovB.SiteList[0].HostRemark != "B租户的站" {
		t.Fatalf("租户B应回填自己的站点备注，实际 %q", ovB.SiteList[0].HostRemark)
	}

	ovP, err := svc.StatSiteOverviewApi(pctx, req)
	if err != nil {
		t.Fatalf("平台作用域概览查询失败: %v", err)
	}
	if len(ovP.SiteList) != 2 {
		t.Fatalf("平台作用域应看到全部 2 个站点，实际 %d", len(ovP.SiteList))
	}
}

// TestStatTenantScopeDetailIsolation 详情趋势：跨租户拿别人的 host_code 只能查到空。
func TestStatTenantScopeDetailIsolation(t *testing.T) {
	coreDB, _ := openStatScopeDBs(t)

	seedScopeHost(t, coreDB, "host-a", "a.example.com", "tenant-a", "userA", "")

	day, hour := global.TrafficBucketOf(time.Now())
	global.AddTraffic("host-a", "a.example.com", day, hour, 100, 200)
	waftask.FlushTrafficStats()

	svc := waf_service.WafStatServiceApp
	ctxA := wafctx.WithTenant(context.Background(), "tenant-a", "userA")
	dA, err := svc.StatSiteDetailApi(ctxA, request.WafStatsSiteDetailReq{HostCode: "host-a", TimeRange: "24h"})
	if err != nil {
		t.Fatalf("租户A详情查询失败: %v", err)
	}
	if dA.Host != "a.example.com" {
		t.Fatalf("租户A应查到自己的域名，实际 %q", dA.Host)
	}

	ctxB := wafctx.WithTenant(context.Background(), "tenant-b", "userB")
	dB, err := svc.StatSiteDetailApi(ctxB, request.WafStatsSiteDetailReq{HostCode: "host-a", TimeRange: "24h"})
	if err != nil {
		t.Fatalf("租户B详情查询失败: %v", err)
	}
	// 24h 趋势会补零点占位，真正的隔离证据是域名与计数都为空（First 未命中、聚合全零）
	if dB.Host != "" || dB.TotalCountSum != 0 {
		t.Fatalf("租户B不应看到 host-a 的数据: Host=%q count=%d", dB.Host, dB.TotalCountSum)
	}
}
