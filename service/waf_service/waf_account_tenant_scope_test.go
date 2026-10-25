package waf_service

// 账号域租户收敛的服务级测试：Account 在 tenantExemptModels 平台豁免清单里，
// 租户回调不给它追加过滤——租户管理员（account:manage）对账号的列表/详情/编辑/删除
// 必须由服务层手工限定归属，否则可见/可操作全平台账号。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"SamWaf/common/wafctx"
	"SamWaf/customtype"
	"SamWaf/enums"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/request"
	commonrequest "SamWaf/model/common/request"

	"gorm.io/gorm"
)

// seedAccountForTest 直插一条账号行（Account 平台豁免，手工赋 BaseOrm 归属）。
func seedAccountForTest(t *testing.T, db *gorm.DB, id, loginAccount, tenantID, role string) model.Account {
	t.Helper()
	bean := model.Account{
		BaseOrm: baseorm.BaseOrm{
			Id:          id,
			USER_CODE:   "userZ",
			Tenant_ID:   tenantID,
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},
		LoginAccount:  loginAccount,
		Role:          role,
		LoginPassword: "$2a$10$seedseedseedseedseedseedseedseedseedseedseedseedse",
		Status:        0,
	}
	if err := db.Create(&bean).Error; err != nil {
		t.Fatalf("插入账号 %s 失败: %v", loginAccount, err)
	}
	return bean
}

// TestAccountListTenantScope 列表/详情按租户限定：A 只见本租户账号，平台视角不受限。
func TestAccountListTenantScope(t *testing.T) {
	db := openTenantTestDB(t)
	seedAccountForTest(t, db, "acc-a1", "a1", "tenantA", enums.ROLE_TENANT_ADMIN)
	seedAccountForTest(t, db, "acc-a2", "a2", "tenantA", enums.ROLE_TENANT_ADMIN)
	seedAccountForTest(t, db, "acc-b1", "b1", "tenantB", enums.ROLE_TENANT_ADMIN)

	ctxA := wafctx.WithTenant(context.Background(), "tenantA", "userZ")
	req := request.WafAccountSearchReq{PageInfo: commonrequest.PageInfo{PageIndex: 1, PageSize: 50}}

	list, total, err := WafAccountServiceApp.GetListApi(ctxA, req)
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("tenantA 应只见本租户 2 个账号，实际 total=%d len=%d", total, len(list))
	}
	for _, v := range list {
		if v.LoginAccount == "b1" {
			t.Fatalf("越权读取：tenantA 的账号列表出现了 tenantB 的账号 b1")
		}
	}

	// 详情：A 查 B 的账号必须为空
	view := WafAccountServiceApp.GetDetailApi(ctxA, request.WafAccountDetailReq{Id: "acc-b1"})
	if view.LoginAccount != "" {
		t.Fatalf("越权读取：tenantA 拿到了 tenantB 账号详情 login=%s", view.LoginAccount)
	}
	view = WafAccountServiceApp.GetDetailApi(ctxA, request.WafAccountDetailReq{Id: "acc-a1"})
	if view.LoginAccount != "a1" {
		t.Fatalf("tenantA 查自己账号应命中，实际 %q", view.LoginAccount)
	}

	// 平台视角（PlatformScope）不受限
	list, total, err = WafAccountServiceApp.GetListApi(wafctx.PlatformScope(context.Background()), req)
	if err != nil || total != 3 || len(list) != 3 {
		t.Fatalf("平台视角应见全部 3 个账号，实际 total=%d len=%d err=%v", total, len(list), err)
	}
}

// TestAccountModifyTenantScope 编辑限定：A 改 B 的账号必须报"账号不存在"。
func TestAccountModifyTenantScope(t *testing.T) {
	db := openTenantTestDB(t)
	seedAccountForTest(t, db, "acc-a1", "a1", "tenantA", enums.ROLE_TENANT_ADMIN)
	seedAccountForTest(t, db, "acc-b1", "b1", "tenantB", enums.ROLE_TENANT_ADMIN)
	ctxA := wafctx.WithTenant(context.Background(), "tenantA", "userZ")

	// 平台超管操作者角色，但请求 ctx 是 tenantA：归属校验优先于角色
	err := WafAccountServiceApp.ModifyApi(ctxA, request.WafAccountEditReq{
		Id: "acc-b1", LoginAccount: "b1-hacked", Status: 1, Remarks: "hacked",
	}, enums.ROLE_SUPER_ADMIN)
	if err == nil || err.Error() != "账号不存在" {
		t.Fatalf("tenantA 编辑 tenantB 账号应报账号不存在，实际 %v", err)
	}
	var b1 model.Account
	db.Where("id = ?", "acc-b1").First(&b1)
	if b1.LoginAccount != "b1" || b1.Status != 0 {
		t.Fatalf("tenantB 账号被篡改: %+v", b1)
	}

	// 编辑自己租户的账号正常
	if err = WafAccountServiceApp.ModifyApi(ctxA, request.WafAccountEditReq{
		Id: "acc-a1", LoginAccount: "a1", Status: 0, Remarks: "改备注",
	}, enums.ROLE_TENANT_ADMIN); err != nil {
		t.Fatalf("编辑本租户账号应成功，实际 %v", err)
	}
}

// TestAccountDelTenantScope 删除规则矩阵：跨租户拒绝 / 非本租户角色拒绝 / 删自己拒绝 /
// 最后一个账号兜底 / 租户管理员可删本租户普通账号。
func TestAccountDelTenantScope(t *testing.T) {
	db := openTenantTestDB(t)
	seedAccountForTest(t, db, "acc-admin", "admin", "tenantA", enums.ROLE_TENANT_ADMIN)
	seedAccountForTest(t, db, "acc-sub", "sub", "tenantA", enums.ROLE_TENANT_ADMIN)
	seedAccountForTest(t, db, "acc-b1", "b1", "tenantB", enums.ROLE_TENANT_ADMIN)
	seedAccountForTest(t, db, "acc-leg", "leg", "tenantA", enums.ROLE_SECURITY_ADMIN)
	ctxA := wafctx.WithTenant(context.Background(), "tenantA", "userZ")

	// 跨租户：A 删 B 的账号 → 记录不存在
	if err := WafAccountServiceApp.DelApi(ctxA, request.WafAccountDelReq{Id: "acc-b1"}, enums.ROLE_TENANT_ADMIN, "admin"); err == nil {
		t.Fatal("tenantA 删除 tenantB 账号应失败")
	}

	// 删自己 → 拒绝
	if err := WafAccountServiceApp.DelApi(ctxA, request.WafAccountDelReq{Id: "acc-admin"}, enums.ROLE_TENANT_ADMIN, "admin"); err == nil ||
		err.Error() != "不能删除当前登录账号自身" {
		t.Fatalf("删自己应被拒绝，实际 %v", err)
	}

	// 删本租户但角色非 tenantAdmin 的账号（legacy 角色号）→ 拒绝
	if err := WafAccountServiceApp.DelApi(ctxA, request.WafAccountDelReq{Id: "acc-leg"}, enums.ROLE_TENANT_ADMIN, "admin"); err == nil {
		t.Fatal("租户管理员删非 tenantAdmin 角色账号应被拒绝")
	}

	// 正常删除本租户普通账号
	if err := WafAccountServiceApp.DelApi(ctxA, request.WafAccountDelReq{Id: "acc-sub"}, enums.ROLE_TENANT_ADMIN, "admin"); err != nil {
		t.Fatalf("删除本租户普通账号应成功，实际 %v", err)
	}
	var cnt int64
	db.Model(&model.Account{}).Where("id = ?", "acc-sub").Count(&cnt)
	if cnt != 0 {
		t.Fatal("账号行应已删除")
	}

	// 其他角色（systemAdmin）删除 → 拒绝
	seedAccountForTest(t, db, "acc-sub2", "sub2", "tenantA", enums.ROLE_TENANT_ADMIN)
	if err := WafAccountServiceApp.DelApi(ctxA, request.WafAccountDelReq{Id: "acc-sub2"}, enums.ROLE_SYSTEM_ADMIN, "someone"); err == nil {
		t.Fatal("非超管/租户管理员删除应被拒绝")
	}
}

// TestAccountCreateTenantStamp 租户管理员建号：归属按请求 ctx 落列（配额/角色强制在
// API 层与服务层协同，这里验证归属盖章与配额拒绝）。
func TestAccountCreateTenantStamp(t *testing.T) {
	db := openTenantTestDB(t)
	seedTenantRow(t, db, "t-acc", enums.TenantStatusActive, "", 0, 0, 1)
	ctxT := wafctx.WithTenant(context.Background(), "t-acc", "userZ")

	// 建第 1 个账号成功（配额 1：当前租户已有 0 个）
	if err := WafAccountServiceApp.AddApi(ctxT, request.WafAccountAddReq{
		LoginAccount: "acc-new", LoginPassword: "New@2026pwd", Role: enums.ROLE_TENANT_ADMIN,
	}); err != nil {
		t.Fatalf("配额内建号应成功，实际 %v", err)
	}
	var created model.Account
	db.Where("login_account = ?", "acc-new").First(&created)
	if created.Tenant_ID != "t-acc" {
		t.Fatalf("新建账号应归属请求租户 t-acc，实际 tenant_id=%q", created.Tenant_ID)
	}

	// 配额满：第 2 个账号拒绝
	err := WafAccountServiceApp.AddApi(ctxT, request.WafAccountAddReq{
		LoginAccount: "acc-new2", LoginPassword: "New@2026pwd", Role: enums.ROLE_TENANT_ADMIN,
	})
	if err == nil || err.Error() == "" {
		t.Fatal("超配额建号应被拒绝")
	}
}

// TestAccountJSONNoPasswordHash 账号视图序列化不得包含口令哈希字段。
func TestAccountJSONNoPasswordHash(t *testing.T) {
	db := openTenantTestDB(t)
	seedAccountForTest(t, db, "acc-a1", "a1", "tenantA", enums.ROLE_TENANT_ADMIN)
	ctxA := wafctx.WithTenant(context.Background(), "tenantA", "userZ")
	list, _, err := WafAccountServiceApp.GetListApi(ctxA, request.WafAccountSearchReq{
		PageInfo: commonrequest.PageInfo{PageIndex: 1, PageSize: 10},
	})
	if err != nil || len(list) != 1 {
		t.Fatalf("列表失败: %v len=%d", err, len(list))
	}
	if list[0].LoginPassword == "" {
		t.Fatal("前置条件不成立：种子账号应有口令哈希")
	}
	raw, err := json.Marshal(list[0])
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if strings.Contains(string(raw), "login_password") || strings.Contains(string(raw), list[0].LoginPassword) {
		t.Fatalf("账号接口序列化泄漏口令哈希: %s", raw)
	}
}
