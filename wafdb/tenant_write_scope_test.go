package wafdb

import (
	"context"
	"testing"

	"SamWaf/common/wafctx"
	"SamWaf/global"
	"SamWaf/model/baseorm"
)

// TestTenantWriteScope 验证写入路径的租户隔离：UPDATE/DELETE 只作用于上下文租户的行，
// CREATE 按上下文租户盖章（覆盖业务残留的全局常量赋值）；无 ctx 时回退全局且不覆盖显式值。
func TestTenantWriteScope(t *testing.T) {
	db := openTenantTestDB(t, "sqlite")
	seedTenantScopeData(t, db)
	RegisterTenantScopeCallbacks(db)

	oldTenant, oldUser := global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"
	t.Cleanup(func() {
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser
	})

	ctxA := wafctx.WithTenant(context.Background(), "tenantA", "userA")
	ctxB := wafctx.WithTenant(context.Background(), "tenantB", "userB")

	t.Run("更新只影响上下文租户的行", func(t *testing.T) {
		// host-1 在 tenantA(a1) 与 tenantB(b1) 各有一行
		res := db.WithContext(ctxA).Model(&tenantScopeTestModel{}).
			Where("host_code = ?", "host-1").Update("host_code", "host-1-updated")
		if res.Error != nil {
			t.Fatalf("更新失败: %v", res.Error)
		}
		if res.RowsAffected != 1 {
			t.Fatalf("tenantA 更新应只命中 1 行，实际 %d 行", res.RowsAffected)
		}
		var rec tenantScopeTestModel
		if err := db.WithContext(ctxB).Where("host_code = ?", "host-1").First(&rec).Error; err != nil {
			t.Fatalf("tenantB 查询失败: %v", err)
		}
		if rec.Id != "b1" || rec.HostCode != "host-1" {
			t.Fatalf("tenantB 的行不应被 tenantA 的更新波及，实际 id=%s host_code=%s", rec.Id, rec.HostCode)
		}
	})

	t.Run("删除只影响上下文租户的行", func(t *testing.T) {
		// host-2 只存在于 tenantA(a2)：tenantB 的删除不应命中
		res := db.WithContext(ctxB).Where("host_code = ?", "host-2").Delete(&tenantScopeTestModel{})
		if res.Error != nil {
			t.Fatalf("删除失败: %v", res.Error)
		}
		if res.RowsAffected != 0 {
			t.Fatalf("tenantB 删除 tenantA 独有行应命中 0 行，实际 %d 行", res.RowsAffected)
		}
		res = db.WithContext(ctxA).Where("host_code = ?", "host-2").Delete(&tenantScopeTestModel{})
		if res.Error != nil {
			t.Fatalf("删除失败: %v", res.Error)
		}
		if res.RowsAffected != 1 {
			t.Fatalf("tenantA 删除自己的行应命中 1 行，实际 %d 行", res.RowsAffected)
		}
	})

	t.Run("无ctx的更新删除不过滤(平台维护语义)", func(t *testing.T) {
		// 数据密钥迁移、库表修复等平台维护任务在启动期无 ctx 运行，
		// 必须能改写租户列尚为空的存量行——写路径仅在有请求租户上下文时过滤
		row := tenantScopeTestModel{
			BaseOrm:  baseorm.BaseOrm{Id: "wm1", Tenant_ID: "tenantA", USER_CODE: "userA"},
			HostCode: "host-maint",
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("准备维护目标行失败: %v", err)
		}
		res := db.Where("host_code = ?", "host-maint").Delete(&tenantScopeTestModel{})
		if res.Error != nil {
			t.Fatalf("无ctx删除失败: %v", res.Error)
		}
		if res.RowsAffected != 1 {
			t.Fatalf("无ctx删除应跨租户可达（平台维护语义），期望命中 1 行，实际 %d 行", res.RowsAffected)
		}
		row2 := tenantScopeTestModel{
			BaseOrm:  baseorm.BaseOrm{Id: "wm2", Tenant_ID: "tenantB", USER_CODE: "userB"},
			HostCode: "host-maint-2",
		}
		if err := db.Create(&row2).Error; err != nil {
			t.Fatalf("准备维护目标行失败: %v", err)
		}
		res = db.Model(&tenantScopeTestModel{}).Where("host_code = ?", "host-maint-2").Update("host_code", "host-maint-2-upd")
		if res.Error != nil {
			t.Fatalf("无ctx更新失败: %v", res.Error)
		}
		if res.RowsAffected != 1 {
			t.Fatalf("无ctx更新应跨租户可达（平台维护语义），期望命中 1 行，实际 %d 行", res.RowsAffected)
		}
	})

	t.Run("新增行按上下文租户盖章", func(t *testing.T) {
		// 业务代码残留的全局常量赋值（tenantZ）必须被上下文租户覆盖，
		// 这是防止存量代码把行写进错误租户的关键语义
		row := tenantScopeTestModel{
			BaseOrm:  baseorm.BaseOrm{Id: "w1", Tenant_ID: "tenantZ", USER_CODE: "userZ"},
			HostCode: "host-w1",
		}
		if err := db.WithContext(ctxA).Create(&row).Error; err != nil {
			t.Fatalf("创建失败: %v", err)
		}
		if row.Tenant_ID != "tenantA" || row.USER_CODE != "userA" {
			t.Fatalf("ctx 应覆盖业务赋值：实际 tenant=%s user=%s", row.Tenant_ID, row.USER_CODE)
		}
	})

	t.Run("新增行无ctx补空缺且不覆盖显式值", func(t *testing.T) {
		row1 := tenantScopeTestModel{BaseOrm: baseorm.BaseOrm{Id: "w2"}, HostCode: "host-w2"}
		if err := db.Create(&row1).Error; err != nil {
			t.Fatalf("创建失败: %v", err)
		}
		if row1.Tenant_ID != "tenantZ" || row1.USER_CODE != "userZ" {
			t.Fatalf("空缺字段应补全局值：实际 tenant=%s user=%s", row1.Tenant_ID, row1.USER_CODE)
		}
		row2 := tenantScopeTestModel{
			BaseOrm:  baseorm.BaseOrm{Id: "w3", Tenant_ID: "tenantB", USER_CODE: "userB"},
			HostCode: "host-w3",
		}
		if err := db.Create(&row2).Error; err != nil {
			t.Fatalf("创建失败: %v", err)
		}
		if row2.Tenant_ID != "tenantB" || row2.USER_CODE != "userB" {
			t.Fatalf("显式赋值不应被无ctx回退覆盖：实际 tenant=%s user=%s", row2.Tenant_ID, row2.USER_CODE)
		}
	})
}
