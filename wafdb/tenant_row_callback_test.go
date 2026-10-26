package wafdb

import (
	"context"
	"testing"

	"SamWaf/common/wafctx"
)

// TestTenantRowCallbackScan 锚定 Row 回调链的租户过滤语义（2026-10 审计收口）。
//
// 背景：GORM 的 Scan()/Row() 走 Row 回调链而非 Query 链（finisher_api.go:
// Scan→Rows→callbacks.Row()）。RegisterTenantScopeCallbacks 曾只注册 before_query
// 于 Query 链，导致所有 Scan 系查询（统计/聚合读取大量使用）静默绕过租户过滤。
// 现已把同一 before_query 挂到 Row 链；本测试锁定三条语义：
//  1. 模型 Scan：随 ctx 租户过滤（与 Find 同口径）；
//  2. Table() Scan（nil schema）：同样被过滤（与 Query 链的 Table() 语义一致，
//     waf_sql_query/日志详情通道的隔离依赖它）；
//  3. 无 ctx：回退全局常量（与单租户时代行为一致）。
// Raw()/Exec() 通道不经回调，语义由 tenant_bypass_channels_test.go 另行锚定。
func TestTenantRowCallbackScan(t *testing.T) {
	db := openTenantTestDB(t, "sqlite")
	seedTenantScopeData(t, db)
	RegisterTenantScopeCallbacks(db)
	t.Cleanup(func() { _ = db.Migrator().DropTable(&tenantScopeTestModel{}) })

	ctxA := wafctx.WithTenant(context.Background(), "tenantA", "userA")

	t.Run("模型Scan随ctx租户过滤", func(t *testing.T) {
		var rows []tenantScopeTestModel
		if err := db.WithContext(ctxA).Model(&tenantScopeTestModel{}).Scan(&rows).Error; err != nil {
			t.Fatalf("Scan 失败: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("tenantA ctx 的 Scan 应命中 2 行，实际 %d", len(rows))
		}
		for _, r := range rows {
			if r.Tenant_ID != "tenantA" {
				t.Errorf("Scan 泄漏其他租户行: %+v", r)
			}
		}
	})

	t.Run("Table无schema的Scan同样过滤", func(t *testing.T) {
		var rows []tenantScopeTestModel
		if err := db.WithContext(ctxA).Table("samwaf_tenant_scope_test").Scan(&rows).Error; err != nil {
			t.Fatalf("Table Scan 失败: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("tenantA ctx 的 Table Scan 应命中 2 行，实际 %d", len(rows))
		}
	})

	t.Run("无ctx回退全局常量", func(t *testing.T) {
		var rows []tenantScopeTestModel
		if err := db.Model(&tenantScopeTestModel{}).Scan(&rows).Error; err != nil {
			t.Fatalf("Scan 失败: %v", err)
		}
		// 种子数据里没有全局租户（tenantZ）的行
		if len(rows) != 0 {
			t.Fatalf("全局回退应命中 0 行，实际 %d", len(rows))
		}
	})
}
