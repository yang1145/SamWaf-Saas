package waf_service

// 套餐（plans）驱动租户差异化的服务级测试：套餐 CRUD 校验、开通按套餐下发
// 权限组/配额、换套餐同步、套餐编辑传播到存量租户、权限天花板判定与缓存失效。

import (
	"context"
	"strings"
	"testing"

	"SamWaf/common/wafctx"
	"SamWaf/enums"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/request"
)

// addPlanForTest 建一个测试套餐（失败即终止）。
func addPlanForTest(t *testing.T, code, permissions string, maxHosts, maxAccounts int) model.Plan {
	t.Helper()
	bean, err := WafPlanServiceApp.AddApi(wafctx.PlatformScope(context.Background()), request.WafPlanAddReq{
		PlanCode:    code,
		PlanName:    "套餐" + code,
		Permissions: permissions,
		MaxHosts:    maxHosts,
		MaxAccounts: maxAccounts,
	})
	if err != nil {
		t.Fatalf("创建套餐 %s 失败: %v", code, err)
	}
	return bean
}

// adminGroupOf 查指定租户的「租户管理员」组（平台视角直查）。
func adminGroupOf(t *testing.T, tenantID string) model.PermissionGroup {
	t.Helper()
	var group model.PermissionGroup
	err := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(context.Background())).
		Model(&model.PermissionGroup{}).
		Where("tenant_id = ? and group_name = ?", tenantID, "租户管理员").
		First(&group).Error
	if err != nil {
		t.Fatalf("查 %s 的租户管理员组失败: %v", tenantID, err)
	}
	return group
}

// TestPlanCrudValidation 套餐写入侧校验：平台码整单拒绝、非法码拒绝、空集拒绝、
// 套餐码唯一、负数配额拒绝。
func TestPlanCrudValidation(t *testing.T) {
	openTenantTestDB(t)
	pctx := wafctx.PlatformScope(context.Background())

	for i, bad := range []struct {
		name        string
		perms       string
		wantErrPart string
	}{
		{"平台域码", enums.PERM_DASHBOARD + "," + enums.PERM_PLATFORM_ADMIN, "平台侧权限码"},
		{"权限组管理码", enums.PERM_GROUP_MANAGE, "平台侧权限码"},
		{"实例配置码", enums.PERM_SYSTEM_CONFIG, "平台侧权限码"},
		{"非法码", enums.PERM_DASHBOARD + ",not:a:code", "非法"},
		{"空集", " , ", "至少选择一个"},
	} {
		if _, err := WafPlanServiceApp.AddApi(pctx, request.WafPlanAddReq{
			PlanCode: "bad-" + string(rune('a'+i)), PlanName: bad.name, Permissions: bad.perms,
		}); err == nil || !strings.Contains(err.Error(), bad.wantErrPart) {
			t.Fatalf("%s 应被拒绝(含 %q)，实际 err=%v", bad.name, bad.wantErrPart, err)
		}
	}
	if _, err := WafPlanServiceApp.AddApi(pctx, request.WafPlanAddReq{
		PlanCode: "UPPER", PlanName: "大写", Permissions: enums.PERM_DASHBOARD,
	}); err == nil || !strings.Contains(err.Error(), "套餐码") {
		t.Fatalf("大写套餐码应被拒绝，实际 %v", err)
	}
	if _, err := WafPlanServiceApp.AddApi(pctx, request.WafPlanAddReq{
		PlanCode: "neg", PlanName: "负配额", Permissions: enums.PERM_DASHBOARD, MaxHosts: -1,
	}); err == nil || !strings.Contains(err.Error(), "负数") {
		t.Fatalf("负配额应被拒绝，实际 %v", err)
	}

	// 唯一性
	addPlanForTest(t, "dup", enums.PERM_DASHBOARD, 0, 0)
	if _, err := WafPlanServiceApp.AddApi(pctx, request.WafPlanAddReq{
		PlanCode: "dup", PlanName: "重复", Permissions: enums.PERM_DASHBOARD,
	}); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("重复套餐码应被拒绝，实际 %v", err)
	}
}

// TestProvisionWithPlan 开通集成：带套餐开通时管理员组授权集=套餐权限集，
// 配额/宽限期未传取套餐默认，显式传值优先。
func TestProvisionWithPlan(t *testing.T) {
	openTenantTestDB(t)
	pctx := wafctx.PlatformScope(context.Background())
	if err := WafPermissionGroupServiceApp.InitDefaultPermissionGroups(); err != nil {
		t.Fatalf("播种预置组失败: %v", err)
	}
	addPlanForTest(t, "lite", enums.PERM_DASHBOARD+","+enums.PERM_HOST_MANAGE, 3, 2)

	// 未传配额 → 套餐默认
	bean, err := WafTenantServiceApp.AddTenantApi(pctx, request.WafTenantAddReq{
		TenantID: "t-lite", TenantName: "套餐租户", PlanCode: "lite",
		AdminAccount: "liteadmin", AdminPassword: "Lite@2026pwd",
	})
	if err != nil {
		t.Fatalf("开通失败: %v", err)
	}
	if bean.MaxHosts != 3 || bean.MaxAccounts != 2 || bean.GraceDays != 0 {
		t.Fatalf("配额应取套餐默认(3,2,0)，实际 (%d,%d,%d)", bean.MaxHosts, bean.MaxAccounts, bean.GraceDays)
	}
	group := adminGroupOf(t, "t-lite")
	if group.Permissions != enums.PERM_DASHBOARD+","+enums.PERM_HOST_MANAGE {
		t.Fatalf("管理员组授权集应等于套餐权限集，实际 %s", group.Permissions)
	}
	if permissionListContains(group.Permissions, enums.PERM_SECURITY) {
		t.Fatalf("lite 套餐不应含防护策略权限")
	}

	// 显式传值（含 0=不限）优先于套餐默认
	bean2, err := WafTenantServiceApp.AddTenantApi(pctx, request.WafTenantAddReq{
		TenantID: "t-lite2", TenantName: "显式配额", PlanCode: "lite",
		MaxHosts:    intPtr(10),
		MaxAccounts: intPtr(0),
		AdminAccount: "lite2admin", AdminPassword: "Lite@2026pwd",
	})
	if err != nil {
		t.Fatalf("开通失败: %v", err)
	}
	if bean2.MaxHosts != 10 || bean2.MaxAccounts != 0 {
		t.Fatalf("显式配额应优先(10,0)，实际 (%d,%d)", bean2.MaxHosts, bean2.MaxAccounts)
	}

	// 不存在的套餐 / 停用的套餐 → 拒绝
	if _, err := WafTenantServiceApp.AddTenantApi(pctx, request.WafTenantAddReq{
		TenantID: "t-noplan", TenantName: "无套餐", PlanCode: "ghost",
		AdminAccount: "npadmin", AdminPassword: "Np@2026pwd",
	}); err == nil || !strings.Contains(err.Error(), "套餐不存在") {
		t.Fatalf("不存在的套餐应被拒绝，实际 %v", err)
	}
	offPlan := addPlanForTest(t, "off", enums.PERM_DASHBOARD, 0, 0)
	disabled := 0
	if _, err := WafPlanServiceApp.ModifyApi(pctx, request.WafPlanEditReq{
		Id: offPlan.Id, PlanName: "停用套餐", Permissions: enums.PERM_DASHBOARD, Status: &disabled,
	}); err != nil {
		t.Fatalf("停用套餐失败: %v", err)
	}
	if _, err := WafTenantServiceApp.AddTenantApi(pctx, request.WafTenantAddReq{
		TenantID: "t-off", TenantName: "停用套餐开通", PlanCode: "off",
		AdminAccount: "offadmin", AdminPassword: "Off@2026pwd",
	}); err == nil || !strings.Contains(err.Error(), "已停用") {
		t.Fatalf("停用套餐开通应被拒绝，实际 %v", err)
	}
}

// TestPlanEditPropagation 套餐编辑传播：权限集变化同步到引用租户的管理员组；
// 被引用的套餐禁删；解除引用后可删。
func TestPlanEditPropagation(t *testing.T) {
	openTenantTestDB(t)
	pctx := wafctx.PlatformScope(context.Background())
	if err := WafPermissionGroupServiceApp.InitDefaultPermissionGroups(); err != nil {
		t.Fatalf("播种预置组失败: %v", err)
	}
	plan := addPlanForTest(t, "prop", enums.PERM_DASHBOARD+","+enums.PERM_HOST_MANAGE, 0, 0)

	if _, err := WafTenantServiceApp.AddTenantApi(pctx, request.WafTenantAddReq{
		TenantID: "t-prop", TenantName: "传播租户", PlanCode: "prop",
		AdminAccount: "propadmin", AdminPassword: "Prop@2026pwd",
	}); err != nil {
		t.Fatalf("开通失败: %v", err)
	}

	// 编辑套餐：扩充权限集 → 租户管理员组同步
	newPerms := enums.PERM_DASHBOARD + "," + enums.PERM_HOST_MANAGE + "," + enums.PERM_LOG_VIEW
	if _, err := WafPlanServiceApp.ModifyApi(pctx, request.WafPlanEditReq{
		Id: plan.Id, PlanName: "套餐prop", Permissions: newPerms,
	}); err != nil {
		t.Fatalf("编辑套餐失败: %v", err)
	}
	if got := adminGroupOf(t, "t-prop").Permissions; got != newPerms {
		t.Fatalf("套餐编辑应同步到租户管理员组，期望 %s 实际 %s", newPerms, got)
	}

	// 被引用的套餐禁删
	if err := WafPlanServiceApp.DelApi(pctx, request.WafPlanDelReq{Id: plan.Id}); err == nil ||
		!strings.Contains(err.Error(), "个租户在用") {
		t.Fatalf("被引用套餐应禁删，实际 %v", err)
	}

	// 换租户套餐后旧套餐可删
	if _, err := WafTenantServiceApp.EditTenantApi(pctx, request.WafTenantEditReq{
		TenantID: "t-prop", TenantName: "传播租户", PlanCode: "",
	}); err != nil {
		t.Fatalf("清除套餐失败: %v", err)
	}
	if err := WafPlanServiceApp.DelApi(pctx, request.WafPlanDelReq{Id: plan.Id}); err != nil {
		t.Fatalf("解除引用后应可删，实际 %v", err)
	}
}

// TestTenantPlanChangeSync 换套餐：管理员组重写 + 配额未显式传值时重置 + 天花板失效。
func TestTenantPlanChangeSync(t *testing.T) {
	openTenantTestDB(t)
	pctx := wafctx.PlatformScope(context.Background())
	if err := WafPermissionGroupServiceApp.InitDefaultPermissionGroups(); err != nil {
		t.Fatalf("播种预置组失败: %v", err)
	}
	addPlanForTest(t, "plan-a", enums.PERM_DASHBOARD+","+enums.PERM_HOST_MANAGE, 3, 2)
	addPlanForTest(t, "plan-b", enums.PERM_DASHBOARD+","+enums.PERM_HOST_MANAGE+","+enums.PERM_SECURITY, 8, 6)

	if _, err := WafTenantServiceApp.AddTenantApi(pctx, request.WafTenantAddReq{
		TenantID: "t-sync", TenantName: "换套餐", PlanCode: "plan-a",
		AdminAccount: "syncadmin", AdminPassword: "Sync@2026pwd",
	}); err != nil {
		t.Fatalf("开通失败: %v", err)
	}
	// 天花板先按 plan-a 缓存
	ceiling := WafTenantServiceApp.TenantPermissionCeiling("t-sync")
	if ceiling == nil || ceiling[enums.PERM_SECURITY] || !ceiling[enums.PERM_HOST_MANAGE] {
		t.Fatalf("plan-a 天花板不符: %+v", ceiling)
	}

	// 换到 plan-b：未显式传配额 → 重置为 plan-b 默认
	bean, err := WafTenantServiceApp.EditTenantApi(pctx, request.WafTenantEditReq{
		TenantID: "t-sync", TenantName: "换套餐", PlanCode: "plan-b",
	})
	if err != nil {
		t.Fatalf("换套餐失败: %v", err)
	}
	if bean.MaxHosts != 8 || bean.MaxAccounts != 6 {
		t.Fatalf("换套餐后配额应重置为(8,6)，实际 (%d,%d)", bean.MaxHosts, bean.MaxAccounts)
	}
	if got := adminGroupOf(t, "t-sync").Permissions; !permissionListContains(got, enums.PERM_SECURITY) {
		t.Fatalf("换套餐后管理员组应含 security 码，实际 %s", got)
	}
	// 天花板缓存已失效：按 plan-b 判定
	ceiling = WafTenantServiceApp.TenantPermissionCeiling("t-sync")
	if ceiling == nil || !ceiling[enums.PERM_SECURITY] {
		t.Fatalf("换套餐后天花板应含 security: %+v", ceiling)
	}

	// 换到不存在的套餐 → 拒绝且数据不动
	if _, err := WafTenantServiceApp.EditTenantApi(pctx, request.WafTenantEditReq{
		TenantID: "t-sync", TenantName: "换套餐", PlanCode: "ghost",
	}); err == nil || !strings.Contains(err.Error(), "套餐不存在") {
		t.Fatalf("换到不存在套餐应被拒绝，实际 %v", err)
	}
}

// TestTenantPermissionCeiling 天花板判定矩阵：无租户行/未绑套餐 → nil；
// 绑套餐 → 套餐权限集；套餐编辑后缓存失效按新集判定。
func TestTenantPermissionCeiling(t *testing.T) {
	db := openTenantTestDB(t)
	pctx := wafctx.PlatformScope(context.Background())

	if c := WafTenantServiceApp.TenantPermissionCeiling(""); c != nil {
		t.Fatalf("空租户码应无天花板，实际 %+v", c)
	}
	if c := WafTenantServiceApp.TenantPermissionCeiling("no-such-tenant"); c != nil {
		t.Fatalf("无租户行应无天花板，实际 %+v", c)
	}
	seedTenantRow(t, db, "c-noplan", enums.TenantStatusActive, "", 0, 0, 0)
	if c := WafTenantServiceApp.TenantPermissionCeiling("c-noplan"); c != nil {
		t.Fatalf("未绑套餐应无天花板（存量租户行为不变），实际 %+v", c)
	}

	plan := addPlanForTest(t, "ceil", enums.PERM_DASHBOARD+","+enums.PERM_LOG_VIEW, 0, 0)
	seedTenantRow(t, db, "c-plan", enums.TenantStatusActive, "", 0, 0, 0)
	if err := db.Model(&model.Tenant{}).Where("tenant_id = ?", "c-plan").
		Update("plan_code", "ceil").Error; err != nil {
		t.Fatalf("绑定套餐失败: %v", err)
	}
	c := WafTenantServiceApp.TenantPermissionCeiling("c-plan")
	if c == nil || !c[enums.PERM_DASHBOARD] || !c[enums.PERM_LOG_VIEW] || c[enums.PERM_HOST_MANAGE] {
		t.Fatalf("天花板应等于套餐权限集，实际 %+v", c)
	}

	// 套餐权限集编辑 → 传播逻辑失效天花板 → 新集生效
	newPerms := enums.PERM_DASHBOARD + "," + enums.PERM_HOST_MANAGE
	if _, err := WafPlanServiceApp.ModifyApi(pctx, request.WafPlanEditReq{
		Id: plan.Id, PlanName: "套餐ceil", Permissions: newPerms,
	}); err != nil {
		t.Fatalf("编辑套餐失败: %v", err)
	}
	c = WafTenantServiceApp.TenantPermissionCeiling("c-plan")
	if c == nil || !c[enums.PERM_HOST_MANAGE] || c[enums.PERM_LOG_VIEW] {
		t.Fatalf("套餐编辑后天花板应按新集判定，实际 %+v", c)
	}
}

// TestInitDefaultPlans 预置套餐播种幂等 + 内容合法（权限集全部可租持）。
func TestInitDefaultPlans(t *testing.T) {
	db := openTenantTestDB(t)
	pctx := wafctx.PlatformScope(context.Background())

	if err := WafPlanServiceApp.InitDefaultPlans(); err != nil {
		t.Fatalf("首次播种失败: %v", err)
	}
	if err := WafPlanServiceApp.InitDefaultPlans(); err != nil {
		t.Fatalf("重复播种失败: %v", err)
	}
	var cnt int64
	if err := db.WithContext(pctx).Model(&model.Plan{}).Count(&cnt).Error; err != nil {
		t.Fatalf("查询播种结果失败: %v", err)
	}
	if cnt != 4 {
		t.Fatalf("幂等播种后应恰好 4 个套餐，实际 %d", cnt)
	}
	var plans []model.Plan
	if err := db.WithContext(pctx).Find(&plans).Error; err != nil {
		t.Fatalf("回读套餐失败: %v", err)
	}
	for _, p := range plans {
		if p.Status != 1 {
			t.Fatalf("预置套餐 %s 应启用，实际 %d", p.PlanCode, p.Status)
		}
		for code := range enums.ParsePermissionSet(p.Permissions) {
			if !enums.IsTenantAssignableCode(code) {
				t.Fatalf("预置套餐 %s 含租户不可持有码 %s", p.PlanCode, code)
			}
		}
	}
}
