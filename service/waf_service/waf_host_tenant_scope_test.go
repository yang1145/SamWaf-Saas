package waf_service

import (
	"context"
	"path/filepath"
	"testing"

	"SamWaf/common/wafctx"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	commonrequest "SamWaf/model/common/request"
	"SamWaf/model/request"
	"SamWaf/wafdb"

	sqlite "github.com/samwafgo/sqlitedriver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestHostServiceTenantScope 在 service 层验证请求级租户隔离（共享库多租户阶段2试点）：
// WafHostService 的查询随 ctx（wafctx 声明的租户）过滤，跨租户的记录即使按精确
// code 点查也不可见；无 ctx 时回退全局常量，与单租户时代行为一致。
func TestHostServiceTenantScope(t *testing.T) {
	// 独立临时库 + 生产同款租户回调，替换全局 core 库
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "host_tenant_scope.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	wafdb.RegisterTenantScopeCallbacks(db)
	if err := db.AutoMigrate(&model.Hosts{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	oldDB, oldTenant, oldUser := global.GWAF_LOCAL_DB, global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	t.Cleanup(func() {
		global.GWAF_LOCAL_DB = oldDB
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	global.GWAF_LOCAL_DB = db
	// 全局指向测试数据中不存在的租户：任何命中都只能来自请求上下文
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"

	seedHosts := []model.Hosts{
		{BaseOrm: baseorm.BaseOrm{Id: "ha1", USER_CODE: "userA", Tenant_ID: "tenantA"}, Code: "hA1", Host: "a.example.com", Port: 8001},
		{BaseOrm: baseorm.BaseOrm{Id: "ha2", USER_CODE: "userA", Tenant_ID: "tenantA"}, Code: "hA2", Host: "b.example.com", Port: 8002},
		{BaseOrm: baseorm.BaseOrm{Id: "hb1", USER_CODE: "userB", Tenant_ID: "tenantB"}, Code: "hB1", Host: "c.example.com", Port: 8003},
	}
	for i := range seedHosts {
		if err := db.Create(&seedHosts[i]).Error; err != nil {
			t.Fatalf("写入种子数据失败: %v", err)
		}
	}

	ctxA := wafctx.WithTenant(context.Background(), "tenantA", "userA")
	ctxB := wafctx.WithTenant(context.Background(), "tenantB", "userB")

	t.Run("列表只返回上下文租户的站点", func(t *testing.T) {
		req := request.WafHostSearchReq{PageInfo: commonrequest.PageInfo{PageIndex: 1, PageSize: 10}}
		list, total, err := WafHostServiceApp.GetListApi(ctxA, req)
		if err != nil {
			t.Fatalf("GetListApi 失败: %v", err)
		}
		if total != 2 || len(list) != 2 {
			t.Fatalf("tenantA 期望 2 行，实际 len=%d total=%d", len(list), total)
		}
		for _, h := range list {
			if h.Tenant_ID != "tenantA" {
				t.Errorf("泄漏其他租户数据：code=%s tenant=%s", h.Code, h.Tenant_ID)
			}
		}
	})

	t.Run("按code点查跨租户站点不可见", func(t *testing.T) {
		req := request.WafHostSearchReq{PageInfo: commonrequest.PageInfo{PageIndex: 1, PageSize: 10}, Code: "hB1"}
		list, total, err := WafHostServiceApp.GetListApi(ctxA, req)
		if err != nil {
			t.Fatalf("GetListApi 失败: %v", err)
		}
		if total != 0 || len(list) != 0 {
			t.Fatalf("tenantA 按 code 点查 tenantB 站点应不可见，实际 len=%d total=%d", len(list), total)
		}
	})

	t.Run("详情按code点查跨租户站点不可见", func(t *testing.T) {
		bean := WafHostServiceApp.GetDetailByCodeApi(ctxA, "hB1")
		if bean.Code != "" {
			t.Fatalf("tenantA 点查 tenantB 站点应返回空，实际 code=%s tenant=%s", bean.Code, bean.Tenant_ID)
		}
		bean = WafHostServiceApp.GetDetailByCodeApi(ctxB, "hB1")
		if bean.Code != "hB1" {
			t.Fatalf("tenantB 点查自己站点应命中，实际 code=%q", bean.Code)
		}
	})

	t.Run("不同上下文租户互不可见", func(t *testing.T) {
		req := request.WafHostSearchReq{PageInfo: commonrequest.PageInfo{PageIndex: 1, PageSize: 10}}
		list, total, err := WafHostServiceApp.GetListApi(ctxB, req)
		if err != nil {
			t.Fatalf("GetListApi 失败: %v", err)
		}
		if total != 1 || len(list) != 1 || list[0].Code != "hB1" {
			t.Fatalf("tenantB 期望仅命中 hB1，实际 len=%d total=%d", len(list), total)
		}
	})

	t.Run("无上下文回退全局常量", func(t *testing.T) {
		req := request.WafHostSearchReq{PageInfo: commonrequest.PageInfo{PageIndex: 1, PageSize: 10}}
		list, total, err := WafHostServiceApp.GetListApi(context.Background(), req)
		if err != nil {
			t.Fatalf("GetListApi 失败: %v", err)
		}
		// 全局 tenantZ 无数据
		if total != 0 || len(list) != 0 {
			t.Fatalf("无 ctx 应命中全局租户(tenantZ)，期望 0 行，实际 %d 行", len(list))
		}
		// 全局切到 tenantA 后回退路径仍生效
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantA", "userA"
		list, total, err = WafHostServiceApp.GetListApi(context.Background(), req)
		if err != nil {
			t.Fatalf("GetListApi 失败: %v", err)
		}
		if total != 2 || len(list) != 2 {
			t.Fatalf("无 ctx 回退全局失败：期望 2 行，实际 %d 行", len(list))
		}
	})
}
