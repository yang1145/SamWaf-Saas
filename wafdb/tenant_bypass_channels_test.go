package wafdb

import (
	"context"
	"testing"

	"SamWaf/common/wafctx"
	"SamWaf/global"
)

// TestTenantScopeBypassChannels 锚定三类"绕过通道"的租户回调现状行为（安全审计 M1）：
//  1. Table() 查询（Statement.Schema 为 nil）仍会被 before_query 追加租户过滤——
//     waf_sql_query 诊断查询、日志导出等通道的隔离依赖这一行为，重构不得翻掉；
//  2. Raw() 预构建 SQL，回调追加的 Where 不会被渲染——回调不生效，
//     使用方必须自行携带租户条件（见 rawSQLTenantScope）；
//  3. Exec() 不经过 Query/Update/Delete 处理器——同上。
//
// 若未来有意改变其中任何一条语义，必须同步修改对应依赖方与本测试。
func TestTenantScopeBypassChannels(t *testing.T) {
	db := openTenantTestDB(t, "sqlite")
	seedTenantScopeData(t, db)

	if err := db.Callback().Query().Before("gorm:query").
		Register("tenant_plugin:before_query", before_query); err != nil {
		t.Fatalf("注册 before_query 回调失败: %v", err)
	}

	oldTenant, oldUser := global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"
	t.Cleanup(func() {
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser
	})

	ctxA := wafctx.WithTenant(context.Background(), "tenantA", "userA")

	t.Run("Table查询nilSchema仍按ctx过滤", func(t *testing.T) {
		var rows []map[string]interface{}
		if err := db.WithContext(ctxA).Table("samwaf_tenant_scope_test").Find(&rows).Error; err != nil {
			t.Fatalf("Table 查询失败: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("nil-schema Table 查询应命中 tenantA 的 2 行（回调过滤生效），实际 %d 行", len(rows))
		}
	})

	t.Run("Raw语句不经过租户回调", func(t *testing.T) {
		var rows []map[string]interface{}
		if err := db.WithContext(ctxA).Raw("SELECT * FROM samwaf_tenant_scope_test").Scan(&rows).Error; err != nil {
			t.Fatalf("Raw 查询失败: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("Raw 应返回全部租户的 3 行（回调不生效，锚定现状），实际 %d 行", len(rows))
		}
	})

	t.Run("Exec更新不经过租户回调", func(t *testing.T) {
		// host-1 在 tenantA(a1) 与 tenantB(b1) 各有一行；若回调向 Exec 注入过滤，
		// b1 不会被更新——两者都被更新即证明 Exec 无租户过滤
		if err := db.WithContext(ctxA).
			Exec(`UPDATE samwaf_tenant_scope_test SET host_code = 'host-1-exec' WHERE host_code = 'host-1'`).Error; err != nil {
			t.Fatalf("Exec 更新失败: %v", err)
		}
		var cnt int64
		if err := db.Raw(`SELECT COUNT(*) FROM samwaf_tenant_scope_test WHERE host_code = 'host-1-exec'`).Scan(&cnt).Error; err != nil {
			t.Fatalf("回读失败: %v", err)
		}
		if cnt != 2 {
			t.Fatalf("Exec 应跨租户生效（回调不生效，锚定现状）：期望 2 行被更新，实际 %d 行", cnt)
		}
	})
}
