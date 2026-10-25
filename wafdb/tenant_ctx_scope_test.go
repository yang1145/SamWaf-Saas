package wafdb

import (
	"context"
	"testing"

	"SamWaf/common/wafctx"
	"SamWaf/global"
)

// TestTenantCtxScopeQuery 验证 before_query 的租户取值优先级：
// 请求上下文（wafctx，随 db.WithContext 传入）> 全局常量（global.GWAF_TENANT_ID/GWAF_USER_CODE）。
// 这是共享库多租户（阶段0）的核心机制测试；测试表与种子数据复用 tenant_scope_test.go。
func TestTenantCtxScopeQuery(t *testing.T) {
	db := openTenantTestDB(t, "sqlite")
	seedTenantScopeData(t, db)

	// 注册与生产一致的 before_query 租户回调
	if err := db.Callback().Query().Before("gorm:query").
		Register("tenant_plugin:before_query", before_query); err != nil {
		t.Fatalf("注册 before_query 回调失败: %v", err)
	}

	// 全局常量指向一个测试数据里不存在的租户，任何命中都只能来自上下文
	oldTenant, oldUser := global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"
	t.Cleanup(func() {
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser
	})

	findRows := func(t *testing.T, ctx context.Context) []tenantScopeTestModel {
		t.Helper()
		var list []tenantScopeTestModel
		var err error
		if ctx != nil {
			err = db.WithContext(ctx).Find(&list).Error
		} else {
			err = db.Find(&list).Error
		}
		if err != nil {
			t.Fatalf("Find 查询失败: %v", err)
		}
		return list
	}

	t.Run("上下文租户优先于全局常量", func(t *testing.T) {
		list := findRows(t, wafctx.WithTenant(context.Background(), "tenantA", "userA"))
		if len(list) != 2 {
			t.Fatalf("上下文租户隔离失败：期望命中 tenantA 的 2 行，实际 %d 行", len(list))
		}
		for _, r := range list {
			if r.Tenant_ID != "tenantA" {
				t.Errorf("查询到非上下文租户数据：id=%s tenant=%s", r.Id, r.Tenant_ID)
			}
		}
	})

	t.Run("不同上下文租户互不可见", func(t *testing.T) {
		list := findRows(t, wafctx.WithTenant(context.Background(), "tenantB", "userB"))
		if len(list) != 1 || list[0].Id != "b1" {
			t.Fatalf("tenantB 隔离失败：期望仅命中 b1，实际 %d 行", len(list))
		}
	})

	t.Run("上下文缺识别号时回退全局识别号", func(t *testing.T) {
		// 全局 user_code=userZ，tenantA 的行都属于 userA：
		// 上下文只声明租户时识别号回退全局值，期望 0 行（证明回退源是全局而非上下文残留）
		list := findRows(t, wafctx.WithTenant(context.Background(), "tenantA", ""))
		if len(list) != 0 {
			t.Fatalf("识别号回退失败：期望 0 行，实际命中 %d 行", len(list))
		}
	})

	t.Run("无上下文回退全局常量(向后兼容)", func(t *testing.T) {
		// 全局指向 tenantZ/userZ，测试数据里没有该租户的行，期望 0 行；
		// 换成与数据一致的取值应命中 2 行，证明回退路径仍然生效
		if list := findRows(t, nil); len(list) != 0 {
			t.Fatalf("无 ctx 时应命中全局租户(tenantZ)：期望 0 行，实际 %d 行", len(list))
		}
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantA", "userA"
		if list := findRows(t, nil); len(list) != 2 {
			t.Fatalf("无 ctx 回退全局失败：期望 2 行，实际 %d 行", len(list))
		}
	})
}
