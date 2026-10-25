package waf_service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"SamWaf/cache"
	"SamWaf/common/wafctx"
	"SamWaf/enums"
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

// openPermissionGroupTestDB 独立临时库 + 生产同款租户回调，替换全局 core 库与缓存。
// 全局租户指向测试数据中不存在的 tenantZ：任何命中都只能来自请求上下文。
func openPermissionGroupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "perm_group_test.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	wafdb.RegisterTenantScopeCallbacks(db)
	if err := db.AutoMigrate(&model.PermissionGroup{}, &model.Account{}); err != nil {
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

func permGroupStatusPtr(v int) *int {
	return &v
}

// TestPermissionGroupService 覆盖权限组 CRUD 的租户隔离、写入侧 fail-closed 校验、
// 预置组保护与 HasPermission 的缓存失效语义。
func TestPermissionGroupService(t *testing.T) {
	db := openPermissionGroupTestDB(t)

	ctxA := wafctx.WithTenant(context.Background(), "tenantA", "userA")
	ctxB := wafctx.WithTenant(context.Background(), "tenantB", "userB")

	t.Run("两个租户各建同名组互不可见", func(t *testing.T) {
		gA, err := WafPermissionGroupServiceApp.AddApi(ctxA, request.WafPermissionGroupAddReq{
			GroupName: "同名组", Permissions: enums.PERM_DASHBOARD,
		})
		if err != nil {
			t.Fatalf("tenantA 建组失败: %v", err)
		}
		// 跨租户同名不冲突
		gB, err := WafPermissionGroupServiceApp.AddApi(ctxB, request.WafPermissionGroupAddReq{
			GroupName: "同名组", Permissions: enums.PERM_LOG_VIEW,
		})
		if err != nil {
			t.Fatalf("tenantB 同名建组不应被 tenantA 挡住: %v", err)
		}
		// 同租户再建同名要被查重拦下
		if _, err := WafPermissionGroupServiceApp.AddApi(ctxA, request.WafPermissionGroupAddReq{
			GroupName: "同名组", Permissions: enums.PERM_DASHBOARD,
		}); err == nil {
			t.Fatalf("tenantA 内同名建组应被拒绝")
		}

		req := request.WafPermissionGroupSearchReq{PageInfo: commonrequest.PageInfo{PageIndex: 1, PageSize: 10}}
		listA, totalA, err := WafPermissionGroupServiceApp.GetListApi(ctxA, req)
		if err != nil {
			t.Fatalf("tenantA 列表失败: %v", err)
		}
		if totalA != 1 || len(listA) != 1 || listA[0].Id != gA.Id {
			t.Fatalf("tenantA 期望只见自己的组，实际 len=%d total=%d", len(listA), totalA)
		}
		listB, totalB, err := WafPermissionGroupServiceApp.GetListApi(ctxB, req)
		if err != nil {
			t.Fatalf("tenantB 列表失败: %v", err)
		}
		if totalB != 1 || len(listB) != 1 || listB[0].Id != gB.Id {
			t.Fatalf("tenantB 期望只见自己的组，实际 len=%d total=%d", len(listB), totalB)
		}
		// 详情点查跨租户不可见
		if bean := WafPermissionGroupServiceApp.GetDetailByIdApi(ctxA, gB.Id); bean.GroupName != "" {
			t.Fatalf("tenantA 点查 tenantB 组应返回零值，实际 %q", bean.GroupName)
		}
	})

	t.Run("非法权限码新增被拒", func(t *testing.T) {
		if _, err := WafPermissionGroupServiceApp.AddApi(ctxA, request.WafPermissionGroupAddReq{
			GroupName: "坏码组", Permissions: enums.PERM_DASHBOARD + ",nonsense:code",
		}); err == nil || !strings.Contains(err.Error(), "非法权限码") {
			t.Fatalf("非法权限码应整单拒绝，实际 err=%v", err)
		}
		// 空权限码同样拒绝（fail-closed：没有授权边界的组不允许存在）
		if _, err := WafPermissionGroupServiceApp.AddApi(ctxA, request.WafPermissionGroupAddReq{
			GroupName: "空码组", Permissions: " , ",
		}); err == nil {
			t.Fatalf("空权限码应被拒绝")
		}
		// 被拒的请求不能留下半条记录
		_, total, err := WafPermissionGroupServiceApp.GetListApi(ctxA, request.WafPermissionGroupSearchReq{
			GroupName: "坏码组", PageInfo: commonrequest.PageInfo{PageIndex: 1, PageSize: 10}})
		if err != nil || total != 0 {
			t.Fatalf("被拒新增不应落库，实际 total=%d err=%v", total, err)
		}
	})

	t.Run("预置组拒删且被账号引用时拒删", func(t *testing.T) {
		builtin := model.PermissionGroup{
			BaseOrm:     baseorm.BaseOrm{Id: "pg-builtin-a", USER_CODE: "userA", Tenant_ID: "tenantA"},
			GroupName:   "预置组A",
			Permissions: enums.PERM_DASHBOARD,
			Status:      1,
			BuiltIn:     1,
		}
		if err := db.Create(&builtin).Error; err != nil {
			t.Fatalf("写入预置组种子失败: %v", err)
		}
		// 系统预置组拒删
		if err := WafPermissionGroupServiceApp.DelApi(ctxA, request.WafPermissionGroupDelReq{Id: builtin.Id}); err == nil {
			t.Fatalf("系统预置组应拒删")
		}
		// 预置组连组名都不让改
		if _, err := WafPermissionGroupServiceApp.ModifyApi(ctxA, request.WafPermissionGroupEditReq{
			Id: builtin.Id, GroupName: "改名组", Permissions: enums.PERM_DASHBOARD, Status: permGroupStatusPtr(1),
		}); err == nil {
			t.Fatalf("系统预置组应拒改组名")
		}

		// 普通组被账号引用时拒删
		normal, err := WafPermissionGroupServiceApp.AddApi(ctxA, request.WafPermissionGroupAddReq{
			GroupName: "待删组", Permissions: enums.PERM_LOG_VIEW,
		})
		if err != nil {
			t.Fatalf("建普通组失败: %v", err)
		}
		account := model.Account{
			BaseOrm:     baseorm.BaseOrm{Id: "acc-pg-a", USER_CODE: "userA", Tenant_ID: "tenantA"},
			LoginAccount: "operatorA",
			GroupId:     normal.Id,
		}
		if err := db.Create(&account).Error; err != nil {
			t.Fatalf("写入引用账号失败: %v", err)
		}
		if err := WafPermissionGroupServiceApp.DelApi(ctxA, request.WafPermissionGroupDelReq{Id: normal.Id}); err == nil || !strings.Contains(err.Error(), "解绑") {
			t.Fatalf("有账号引用的组应拒删并提示解绑，实际 err=%v", err)
		}
		// 解绑后可删，删除后判定立即失效（缓存已清）
		if err := db.Model(&model.Account{}).Where("id = ?", account.Id).Update("group_id", "").Error; err != nil {
			t.Fatalf("解绑失败: %v", err)
		}
		if err := WafPermissionGroupServiceApp.DelApi(ctxA, request.WafPermissionGroupDelReq{Id: normal.Id}); err != nil {
			t.Fatalf("解绑后删除应成功: %v", err)
		}
		has, err := WafPermissionGroupServiceApp.HasPermission(ctxA, normal.Id, enums.PERM_LOG_VIEW)
		if err != nil || has {
			t.Fatalf("已删组判定应 false，实际 has=%v err=%v", has, err)
		}
	})

	t.Run("HasPermission含码不含码禁用与缓存失效", func(t *testing.T) {
		grp, err := WafPermissionGroupServiceApp.AddApi(ctxA, request.WafPermissionGroupAddReq{
			GroupName: "判定组", Permissions: enums.PERM_DASHBOARD + "," + enums.PERM_LOG_VIEW,
			Status: permGroupStatusPtr(1),
		})
		if err != nil {
			t.Fatalf("建判定组失败: %v", err)
		}

		// 组含码 → true（首次查库并写缓存）
		has, err := WafPermissionGroupServiceApp.HasPermission(ctxA, grp.Id, enums.PERM_DASHBOARD)
		if err != nil || !has {
			t.Fatalf("组含码应 true，实际 has=%v err=%v", has, err)
		}
		// 组不含码 → false（走缓存路径）
		has, err = WafPermissionGroupServiceApp.HasPermission(ctxA, grp.Id, enums.PERM_SECURITY)
		if err != nil || has {
			t.Fatalf("组不含码应 false，实际 has=%v err=%v", has, err)
		}

		// 修改组移除授权码后，缓存失效立即生效
		if _, err = WafPermissionGroupServiceApp.ModifyApi(ctxA, request.WafPermissionGroupEditReq{
			Id: grp.Id, GroupName: "判定组", Permissions: enums.PERM_LOG_VIEW, Status: permGroupStatusPtr(1),
		}); err != nil {
			t.Fatalf("修改组失败: %v", err)
		}
		has, err = WafPermissionGroupServiceApp.HasPermission(ctxA, grp.Id, enums.PERM_DASHBOARD)
		if err != nil || has {
			t.Fatalf("改组后移除的码应立即 false，实际 has=%v err=%v", has, err)
		}
		has, err = WafPermissionGroupServiceApp.HasPermission(ctxA, grp.Id, enums.PERM_LOG_VIEW)
		if err != nil || !has {
			t.Fatalf("改组后保留的码应 true，实际 has=%v err=%v", has, err)
		}

		// 禁用组 → false；负结果也进缓存，重新启用（改组清缓存）后立即恢复
		grp2, err := WafPermissionGroupServiceApp.AddApi(ctxA, request.WafPermissionGroupAddReq{
			GroupName: "停用组", Permissions: enums.PERM_DASHBOARD,
			Status: permGroupStatusPtr(0),
		})
		if err != nil {
			t.Fatalf("建停用组失败: %v", err)
		}
		has, err = WafPermissionGroupServiceApp.HasPermission(ctxA, grp2.Id, enums.PERM_DASHBOARD)
		if err != nil || has {
			t.Fatalf("禁用组应 false，实际 has=%v err=%v", has, err)
		}
		if _, err = WafPermissionGroupServiceApp.ModifyApi(ctxA, request.WafPermissionGroupEditReq{
			Id: grp2.Id, GroupName: "停用组", Permissions: enums.PERM_DASHBOARD, Status: permGroupStatusPtr(1),
		}); err != nil {
			t.Fatalf("重新启用组失败: %v", err)
		}
		has, err = WafPermissionGroupServiceApp.HasPermission(ctxA, grp2.Id, enums.PERM_DASHBOARD)
		if err != nil || !has {
			t.Fatalf("重新启用后应立即 true，实际 has=%v err=%v", has, err)
		}
	})

	t.Run("InitDefaultPermissionGroups幂等播种", func(t *testing.T) {
		// 全局回退租户 tenantZ：播种走全局标识，幂等重跑不重复
		if err := WafPermissionGroupServiceApp.InitDefaultPermissionGroups(); err != nil {
			t.Fatalf("首次播种失败: %v", err)
		}
		if err := WafPermissionGroupServiceApp.InitDefaultPermissionGroups(); err != nil {
			t.Fatalf("重复播种失败: %v", err)
		}
		ctxZ := wafctx.WithTenant(context.Background(), "tenantZ", "userZ")
		list, total, err := WafPermissionGroupServiceApp.GetListApi(ctxZ, request.WafPermissionGroupSearchReq{
			PageInfo: commonrequest.PageInfo{PageIndex: 1, PageSize: 10}})
		if err != nil {
			t.Fatalf("查询播种结果失败: %v", err)
		}
		if total != 3 || len(list) != 3 {
			t.Fatalf("幂等播种后应恰好 3 个预置组，实际 len=%d total=%d", len(list), total)
		}
		for _, g := range list {
			if g.BuiltIn != 1 || g.Status != 1 {
				t.Fatalf("预置组应为 BuiltIn=1 Status=1，实际 %+v", g)
			}
			if !strings.Contains(g.Remarks, "系统预置") {
				t.Fatalf("预置组备注应注明系统预置，实际 %q", g.Remarks)
			}
			if _, err := parsePermissionCodes(g.Permissions); err != nil {
				t.Fatalf("预置组权限码非法: %v (%s)", err, g.Permissions)
			}
		}
	})
}
