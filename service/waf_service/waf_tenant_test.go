package waf_service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"SamWaf/cache"
	"SamWaf/common/uuid"
	"SamWaf/common/wafctx"
	"SamWaf/customtype"
	"SamWaf/enums"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/request"
	"SamWaf/utils"
	"SamWaf/wafdb"

	sqlite "github.com/samwafgo/sqlitedriver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const tenantTestTimeLayout = "2006-01-02 15:04:05"

// intPtr 测试辅助：配额指针字段取值
func intPtr(v int) *int { return &v }

// openTenantTestDB 独立临时库 + 生产同款租户回调，替换全局 core 库与缓存。
// 全局租户指向测试数据中不存在的 tenantZ：任何命中都只能来自请求上下文/显式租户码。
func openTenantTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tenant_test.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	wafdb.RegisterTenantScopeCallbacks(db)
	if err := db.AutoMigrate(&model.Tenant{}, &model.Plan{}, &model.PermissionGroup{}, &model.Account{}, &model.Hosts{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	oldDB, oldTenant, oldUser, oldCache := global.GWAF_LOCAL_DB, global.GWAF_TENANT_ID, global.GWAF_USER_CODE, global.GCACHE_WAFCACHE
	t.Cleanup(func() {
		global.GWAF_LOCAL_DB = oldDB
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser
		global.GCACHE_WAFCACHE = oldCache
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	global.GWAF_LOCAL_DB = db
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"
	global.GCACHE_WAFCACHE = cache.InitWafCache()
	return db
}

// seedTenantRow 直插一条租户行（tenants 平台豁免，回调不盖章，手工赋 BaseOrm）。
func seedTenantRow(t *testing.T, db *gorm.DB, code string, status int, expireTime string, graceDays int, maxHosts, maxAccounts int) {
	t.Helper()
	now := time.Now()
	bean := model.Tenant{
		BaseOrm:     baseorm.BaseOrm{Id: uuid.GenUUID(), USER_CODE: "userZ", Tenant_ID: code, CREATE_TIME: customtype.JsonTime(now), UPDATE_TIME: customtype.JsonTime(now)},
		TenantID:    code,
		TenantName:  "租户" + code,
		Status:      status,
		ExpireTime:  expireTime,
		GraceDays:   graceDays,
		MaxHosts:    maxHosts,
		MaxAccounts: maxAccounts,
	}
	if err := db.Create(&bean).Error; err != nil {
		t.Fatalf("插入租户行 %s 失败: %v", code, err)
	}
}

// TestTenantProvisioning 开通租户的落库明细与租户隔离。
func TestTenantProvisioning(t *testing.T) {
	db := openTenantTestDB(t)
	pctx := wafctx.PlatformScope(context.Background())

	// 生产顺序：预置组先于任何开通动作存在（main.go 启动播种）
	if err := WafPermissionGroupServiceApp.InitDefaultPermissionGroups(); err != nil {
		t.Fatalf("播种平台预置组失败: %v", err)
	}

	bean, err := WafTenantServiceApp.AddTenantApi(pctx, request.WafTenantAddReq{
		TenantID:      "tenant-x",
		TenantName:    "X公司",
		MaxHosts:      intPtr(5),
		MaxAccounts:   intPtr(3),
		GraceDays:     intPtr(3),
		Remarks:       "测试租户",
		AdminAccount:  "xadmin",
		AdminPassword: "xAdmin@2026",
	})
	if err != nil {
		t.Fatalf("开通租户失败: %v", err)
	}
	if bean.TenantID != "tenant-x" || bean.Status != enums.TenantStatusActive {
		t.Fatalf("返回的租户行不符合预期: %+v", bean)
	}

	// 租户行存在且字段落库正确
	var row model.Tenant
	if err := db.WithContext(pctx).Where("tenant_id = ?", "tenant-x").First(&row).Error; err != nil {
		t.Fatalf("新租户行不存在: %v", err)
	}
	if row.TenantName != "X公司" || row.MaxHosts != 5 || row.MaxAccounts != 3 || row.GraceDays != 3 {
		t.Fatalf("租户行字段不符: %+v", row)
	}
	// 说明：model.Tenant 的外层 TenantID 与内嵌 BaseOrm.Tenant_ID 同名映射 tenant_id 列，
	// GORM 绑定的是外层 TenantID（读回时 BaseOrm.Tenant_ID 为零值），列值本身一致
	if row.TenantID != "tenant-x" || row.USER_CODE != "userZ" || row.Id == "" {
		t.Fatalf("租户行归属三要素不符: tenantID=%q userCode=%q id=%q", row.TenantID, row.USER_CODE, row.Id)
	}

	// 权限组：3 个平台预置组副本 + 1 个租户管理员组，全部归属新租户码
	var groups []model.PermissionGroup
	if err := db.WithContext(pctx).Where("tenant_id = ?", "tenant-x").Order("CREATE_TIME asc").Find(&groups).Error; err != nil {
		t.Fatalf("查询新租户权限组失败: %v", err)
	}
	if len(groups) != 4 {
		t.Fatalf("新租户应有 4 个权限组(3 预置副本+1 管理员组)，实际 %d", len(groups))
	}
	var adminGroup model.PermissionGroup
	for _, g := range groups {
		if g.BaseOrm.Tenant_ID != "tenant-x" || g.BaseOrm.USER_CODE != "userZ" {
			t.Fatalf("权限组 %s 归属租户不符: %+v", g.GroupName, g.BaseOrm)
		}
		if g.GroupName == "租户管理员" {
			adminGroup = g
		}
	}
	if adminGroup.Id == "" {
		t.Fatalf("未找到租户管理员组: %+v", groups)
	}
	if strings.Contains(adminGroup.Permissions, enums.PERM_PLATFORM_ADMIN) ||
		strings.Contains(adminGroup.Permissions, enums.PERM_GROUP_MANAGE) {
		t.Fatalf("租户管理员组不应包含平台域/权限组管理权限码: %s", adminGroup.Permissions)
	}
	for _, code := range []string{
		enums.PERM_DASHBOARD, enums.PERM_HOST_MANAGE, enums.PERM_LOG_VIEW, enums.PERM_SECURITY,
		enums.PERM_AUDIT, enums.PERM_SYSTEM_OPS, enums.PERM_ACCOUNT_MANAGE,
	} {
		if !permissionListContains(adminGroup.Permissions, code) {
			t.Fatalf("租户管理员组缺少权限码 %s: %s", code, adminGroup.Permissions)
		}
	}

	// 租户管理员账号：归属新租户、角色 tenantAdmin、绑定管理员组、密码 bcrypt
	var acc model.Account
	if err := db.WithContext(pctx).Where("login_account = ?", "xadmin").First(&acc).Error; err != nil {
		t.Fatalf("租户管理员账号不存在: %v", err)
	}
	if acc.BaseOrm.Tenant_ID != "tenant-x" || acc.BaseOrm.USER_CODE != "userZ" {
		t.Fatalf("管理员账号 BaseOrm 三要素不符: %+v", acc.BaseOrm)
	}
	if acc.Role != enums.ROLE_TENANT_ADMIN || acc.GroupId != adminGroup.Id {
		t.Fatalf("管理员账号角色/组绑定不符: role=%s groupId=%s", acc.Role, acc.GroupId)
	}
	if !utils.IsBcryptHash(acc.LoginPassword) {
		t.Fatalf("管理员密码应为 bcrypt 哈希: %s", acc.LoginPassword)
	}

	// 租户隔离：新租户 ctx 可见自己的组，别的租户不可见（权限组非豁免模型，随租户过滤）
	ctxX := wafctx.WithTenant(context.Background(), "tenant-x", "userZ")
	ctxY := wafctx.WithTenant(context.Background(), "tenant-y", "userZ")
	var cntX, cntY int64
	if err := db.WithContext(ctxX).Model(&model.PermissionGroup{}).Count(&cntX).Error; err != nil {
		t.Fatalf("租户 ctx 查组失败: %v", err)
	}
	if err := db.WithContext(ctxY).Model(&model.PermissionGroup{}).Count(&cntY).Error; err != nil {
		t.Fatalf("其他租户 ctx 查组失败: %v", err)
	}
	if cntX != 4 {
		t.Fatalf("新租户 ctx 应见 4 个组，实际 %d", cntX)
	}
	if cntY != 0 {
		t.Fatalf("其他租户 ctx 不应见新租户的组，实际 %d", cntY)
	}

	// 同租户码重复开通被拒
	if _, err := WafTenantServiceApp.AddTenantApi(pctx, request.WafTenantAddReq{
		TenantID: "tenant-x", TenantName: "重复", AdminAccount: "xadmin2", AdminPassword: "yAdmin@2026",
	}); err == nil {
		t.Fatalf("重复租户码应被拒绝")
	}
}

// TestCheckTenantLoginable 登录期状态机。
func TestCheckTenantLoginable(t *testing.T) {
	db := openTenantTestDB(t)
	now := time.Now()

	// 不存在 → nil（向后兼容：存量实例无租户行=平台自身）
	if err := WafTenantServiceApp.CheckTenantLoginable("no-such-tenant"); err != nil {
		t.Fatalf("不存在的租户应放行，实际 %v", err)
	}

	seedTenantRow(t, db, "t-active", enums.TenantStatusActive, "", 0, 0, 0)
	seedTenantRow(t, db, "t-suspend", enums.TenantStatusSuspended, "", 0, 0, 0)
	seedTenantRow(t, db, "t-cancel", enums.TenantStatusCancelled, "", 0, 0, 0)
	// 宽限内：过期 1 天、宽限 3 天
	seedTenantRow(t, db, "t-grace", enums.TenantStatusExpired, now.AddDate(0, 0, -1).Format(tenantTestTimeLayout), 3, 0, 0)
	// 超宽限：过期 2 天、宽限 1 天（deadline = 过期+1天 < now）
	seedTenantRow(t, db, "t-over", enums.TenantStatusExpired, now.AddDate(0, 0, -2).Format(tenantTestTimeLayout), 1, 0, 0)
	// 过期且宽限 0 天：到期即禁登录
	seedTenantRow(t, db, "t-over0", enums.TenantStatusExpired, now.AddDate(0, 0, -1).Format(tenantTestTimeLayout), 0, 0, 0)

	if err := WafTenantServiceApp.CheckTenantLoginable("t-active"); err != nil {
		t.Fatalf("Active 应放行，实际 %v", err)
	}
	if err := WafTenantServiceApp.CheckTenantLoginable("t-suspend"); err == nil || !strings.Contains(err.Error(), "停用") {
		t.Fatalf("Suspended 应报停用，实际 %v", err)
	}
	if err := WafTenantServiceApp.CheckTenantLoginable("t-cancel"); err == nil || !strings.Contains(err.Error(), "注销") {
		t.Fatalf("Cancelled 应报注销，实际 %v", err)
	}
	if err := WafTenantServiceApp.CheckTenantLoginable("t-grace"); err != nil {
		t.Fatalf("宽限内 Expired 应放行，实际 %v", err)
	}
	if err := WafTenantServiceApp.CheckTenantLoginable("t-over"); err == nil || !strings.Contains(err.Error(), "宽限") {
		t.Fatalf("超宽限 Expired 应报错，实际 %v", err)
	}
	if err := WafTenantServiceApp.CheckTenantLoginable("t-over0"); err == nil || !strings.Contains(err.Error(), "宽限") {
		t.Fatalf("宽限 0 天的 Expired 应报错，实际 %v", err)
	}
}

// TestRequestPolicy 请求期策略矩阵 + 缓存失效。
func TestRequestPolicy(t *testing.T) {
	db := openTenantTestDB(t)
	now := time.Now()

	seedTenantRow(t, db, "p-active", enums.TenantStatusActive, "", 0, 0, 0)
	seedTenantRow(t, db, "p-suspend", enums.TenantStatusSuspended, "", 0, 0, 0)
	seedTenantRow(t, db, "p-cancel", enums.TenantStatusCancelled, "", 0, 0, 0)
	seedTenantRow(t, db, "p-grace", enums.TenantStatusExpired, now.AddDate(0, 0, -1).Format(tenantTestTimeLayout), 3, 0, 0)
	seedTenantRow(t, db, "p-over", enums.TenantStatusExpired, now.AddDate(0, 0, -2).Format(tenantTestTimeLayout), 1, 0, 0)

	cases := []struct {
		tenantID     string
		wantDeny     bool
		wantReadOnly bool
	}{
		{"no-such-tenant", false, false},
		{"p-active", false, false},
		{"p-suspend", true, false},
		{"p-cancel", true, false},
		{"p-grace", false, true},
		{"p-over", true, false},
	}
	for _, c := range cases {
		deny, ro := WafTenantServiceApp.RequestPolicy(c.tenantID)
		if deny != c.wantDeny || ro != c.wantReadOnly {
			t.Fatalf("%s 期望 (%v,%v) 实际 (%v,%v)", c.tenantID, c.wantDeny, c.wantReadOnly, deny, ro)
		}
	}

	// 缓存生效：再次读取命中缓存结果一致
	deny, ro := WafTenantServiceApp.RequestPolicy("p-grace")
	if deny || !ro {
		t.Fatalf("缓存路径下宽限内仍应只读，实际 (%v,%v)", deny, ro)
	}

	// 状态修改失效缓存：Active → Suspended 后请求期立即拒绝
	seedTenantRow(t, db, "p-edit", enums.TenantStatusActive, "", 0, 0, 0)
	if deny, _ := WafTenantServiceApp.RequestPolicy("p-edit"); deny {
		t.Fatalf("修改前应为放行")
	}
	if err := db.Model(&model.Tenant{}).Where("tenant_id = ?", "p-edit").
		Update("status", enums.TenantStatusSuspended).Error; err != nil {
		t.Fatalf("改状态失败: %v", err)
	}
	WafTenantServiceApp.invalidateTenantPolicyCache("p-edit")
	if deny, _ := WafTenantServiceApp.RequestPolicy("p-edit"); !deny {
		t.Fatalf("缓存失效后应按新状态拒绝")
	}
}

// TestCheckTenantQuota 配额判定：max_hosts=1 建满后报错，0=不限。
func TestCheckTenantQuota(t *testing.T) {
	db := openTenantTestDB(t)
	seedTenantRow(t, db, "t-quota", enums.TenantStatusActive, "", 0, 1, 0)
	ctxQ := wafctx.WithTenant(context.Background(), "t-quota", "userZ")

	// 未建满 → nil
	if err := WafTenantServiceApp.CheckTenantQuota(ctxQ, enums.TenantQuotaKindHost); err != nil {
		t.Fatalf("未达配额应放行，实际 %v", err)
	}
	// 账号配额 0=不限
	if err := WafTenantServiceApp.CheckTenantQuota(ctxQ, enums.TenantQuotaKindAccount); err != nil {
		t.Fatalf("账号不限配额应放行，实际 %v", err)
	}
	// 未知维度 → nil
	if err := WafTenantServiceApp.CheckTenantQuota(ctxQ, "unknown"); err != nil {
		t.Fatalf("未知维度应放行，实际 %v", err)
	}

	// 建满 1 台 host（租户 ctx 写入，回调自动盖章租户标识）
	host := model.Hosts{
		BaseOrm: baseorm.BaseOrm{Id: uuid.GenUUID(), CREATE_TIME: customtype.JsonTime(time.Now()), UPDATE_TIME: customtype.JsonTime(time.Now())},
		Code:    "h-quota-1", Host: "quota.example.com", Port: 80,
	}
	if err := db.WithContext(ctxQ).Create(&host).Error; err != nil {
		t.Fatalf("插入 host 失败: %v", err)
	}
	// 直查（平台作用域，避免无 ctx 时回退全局租户过滤）确认租户归属正确
	var hostCnt int64
	if err := db.WithContext(wafctx.PlatformScope(context.Background())).Model(&model.Hosts{}).
		Where("tenant_id = ? and user_code = ?", "t-quota", "userZ").Count(&hostCnt).Error; err != nil || hostCnt != 1 {
		t.Fatalf("host 行应归属 t-quota，实际 cnt=%d err=%v", hostCnt, err)
	}
	err := WafTenantServiceApp.CheckTenantQuota(ctxQ, enums.TenantQuotaKindHost)
	if err == nil || !strings.Contains(err.Error(), "配额上限") {
		t.Fatalf("建满后应报配额上限，实际 %v", err)
	}

	// 无租户行的租户码（回退全局=平台自身）不受限
	if err := WafTenantServiceApp.CheckTenantQuota(wafctx.WithTenant(context.Background(), "tenantZ", "userZ"), enums.TenantQuotaKindHost); err != nil {
		t.Fatalf("无租户行应不受限，实际 %v", err)
	}
}

// TestRenewTenant 续期恢复 Active 且策略缓存失效。
func TestRenewTenant(t *testing.T) {
	db := openTenantTestDB(t)
	pctx := wafctx.PlatformScope(context.Background())
	now := time.Now()

	// 已过期（超宽限）的租户：请求期策略为拒绝，且缓存住该快照
	seedTenantRow(t, db, "t-renew", enums.TenantStatusExpired, now.AddDate(0, 0, -2).Format(tenantTestTimeLayout), 1, 0, 0)
	if deny, _ := WafTenantServiceApp.RequestPolicy("t-renew"); !deny {
		t.Fatalf("续期前超宽限应拒绝")
	}

	bean, err := WafTenantServiceApp.RenewTenantApi(pctx, request.WafTenantRenewReq{TenantID: "t-renew", AddDays: 30})
	if err != nil {
		t.Fatalf("续期失败: %v", err)
	}
	if bean.Status != enums.TenantStatusActive {
		t.Fatalf("续期后应恢复 Active，实际 %d", bean.Status)
	}
	newExpire, perr := time.ParseInLocation(tenantTestTimeLayout, bean.ExpireTime, time.Local)
	if perr != nil {
		t.Fatalf("续期后到期时间非法: %s", bean.ExpireTime)
	}
	// 已到期租户从当前时间起算 +30 天（允许 1 分钟时钟误差）
	if d := newExpire.Sub(now); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("续期应从当前时间顺延 30 天，实际 %v", d)
	}

	// 缓存已失效：再查策略应为放行（若未失效，旧快照仍是 Expired→拒绝）
	if deny, ro := WafTenantServiceApp.RequestPolicy("t-renew"); deny || ro {
		t.Fatalf("续期后请求期应放行，实际 (%v,%v)", deny, ro)
	}

	// 绝对时间优先于 AddDays
	if _, err := WafTenantServiceApp.RenewTenantApi(pctx, request.WafTenantRenewReq{
		TenantID: "t-renew", ExpireTime: "2027-01-01 00:00:00", AddDays: 5,
	}); err != nil {
		t.Fatalf("绝对时间续期失败: %v", err)
	}
	var row model.Tenant
	if err := db.WithContext(pctx).Where("tenant_id = ?", "t-renew").First(&row).Error; err != nil {
		t.Fatalf("回读租户失败: %v", err)
	}
	if row.ExpireTime != "2027-01-01 00:00:00" {
		t.Fatalf("绝对时间应优先，实际 %s", row.ExpireTime)
	}
	if row.Status != enums.TenantStatusActive {
		t.Fatalf("续期后应为 Active，实际 %d", row.Status)
	}

	// 两个字段都没给 → 报错
	if _, err := WafTenantServiceApp.RenewTenantApi(pctx, request.WafTenantRenewReq{TenantID: "t-renew"}); err == nil {
		t.Fatalf("未提供续期口径应报错")
	}
}

// TestTenantEditAndDel 编辑与软删除。
func TestTenantEditAndDel(t *testing.T) {
	db := openTenantTestDB(t)
	pctx := wafctx.PlatformScope(context.Background())

	// 编辑里指定 plan_code=pro：套餐须真实存在（不存在则整单拒绝）
	if _, err := WafPlanServiceApp.AddApi(pctx, request.WafPlanAddReq{
		PlanCode: "pro", PlanName: "专业版",
		Permissions: enums.PERM_DASHBOARD + "," + enums.PERM_HOST_MANAGE,
		MaxHosts: 50, MaxAccounts: 20,
	}); err != nil {
		t.Fatalf("播种套餐 pro 失败: %v", err)
	}

	seedTenantRow(t, db, "t-edit", enums.TenantStatusActive, "", 0, 1, 2)

	quotaHosts, quotaAccounts := 10, 20
	grace := 5
	suspended := enums.TenantStatusSuspended
	permExpire := "2027-06-30 23:59:59"
	bean, err := WafTenantServiceApp.EditTenantApi(pctx, request.WafTenantEditReq{
		TenantID: "t-edit", TenantName: "改名的租户", PlanCode: "pro",
		MaxHosts: &quotaHosts, MaxAccounts: &quotaAccounts, GraceDays: &grace,
		Status: &suspended, ExpireTime: &permExpire, Remarks: "改备注",
	})
	if err != nil {
		t.Fatalf("编辑失败: %v", err)
	}
	if bean.TenantName != "改名的租户" || bean.PlanCode != "pro" || bean.MaxHosts != 10 ||
		bean.MaxAccounts != 20 || bean.GraceDays != 5 || bean.Status != enums.TenantStatusSuspended ||
		bean.ExpireTime != "2027-06-30 23:59:59" {
		t.Fatalf("编辑结果不符: %+v", bean)
	}
	// 状态改为停用后，请求期策略立即拒绝（编辑已失效缓存）
	if deny, _ := WafTenantServiceApp.RequestPolicy("t-edit"); !deny {
		t.Fatalf("停用后请求期应拒绝")
	}

	// 软删除：平台自身拒删
	if err := WafTenantServiceApp.DelTenantApi(pctx, request.WafTenantDelReq{TenantID: "tenantZ"}); err == nil {
		t.Fatalf("平台自身租户应拒删")
	}
	// 其他租户软删除 → Cancelled，行保留
	if err := WafTenantServiceApp.DelTenantApi(pctx, request.WafTenantDelReq{TenantID: "t-edit"}); err != nil {
		t.Fatalf("软删除失败: %v", err)
	}
	var row model.Tenant
	if err := db.WithContext(pctx).Where("tenant_id = ?", "t-edit").First(&row).Error; err != nil {
		t.Fatalf("软删除后行应保留: %v", err)
	}
	if row.Status != enums.TenantStatusCancelled {
		t.Fatalf("软删除后应为 Cancelled，实际 %d", row.Status)
	}
	// 注销后策略立即拒绝
	if deny, _ := WafTenantServiceApp.RequestPolicy("t-edit"); !deny {
		t.Fatalf("注销后请求期应拒绝")
	}
	// 不存在的租户
	if err := WafTenantServiceApp.DelTenantApi(pctx, request.WafTenantDelReq{TenantID: "no-such"}); err == nil {
		t.Fatalf("不存在的租户应报错")
	}
}

// TestInitDefaultTenant 启动播种幂等。
func TestInitDefaultTenant(t *testing.T) {
	db := openTenantTestDB(t)
	pctx := wafctx.PlatformScope(context.Background())

	if err := WafTenantServiceApp.InitDefaultTenant(); err != nil {
		t.Fatalf("首次播种失败: %v", err)
	}
	if err := WafTenantServiceApp.InitDefaultTenant(); err != nil {
		t.Fatalf("重复播种失败: %v", err)
	}
	var cnt int64
	if err := db.WithContext(pctx).Model(&model.Tenant{}).Where("tenant_id = ?", "tenantZ").Count(&cnt).Error; err != nil {
		t.Fatalf("查询播种结果失败: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("幂等播种后应恰好 1 行，实际 %d", cnt)
	}
	var row model.Tenant
	if err := db.WithContext(pctx).Where("tenant_id = ?", "tenantZ").First(&row).Error; err != nil {
		t.Fatalf("回读平台租户失败: %v", err)
	}
	if row.TenantName != "平台" || row.Status != enums.TenantStatusActive ||
		row.MaxHosts != 0 || row.MaxAccounts != 0 || row.ExpireTime != "" {
		t.Fatalf("平台租户字段不符: %+v", row)
	}
}
