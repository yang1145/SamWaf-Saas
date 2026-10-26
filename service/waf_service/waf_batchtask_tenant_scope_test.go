package waf_service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"SamWaf/common/uuid"
	"SamWaf/common/wafctx"
	"SamWaf/enums"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	commonreq "SamWaf/model/common/request"
	"SamWaf/model/request"
	"SamWaf/wafdb"

	sqlite "github.com/samwafgo/sqlitedriver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 批量任务/安全审计的租户隔离用例：
//  1. 批量任务 CRUD 随请求租户过滤：A 建的任务 B 看不见、改不到、删不掉，平台作用域全可见；
//  2. 批量处理器同款写入模式（不写死租户 + WithContext(任务租户)）落行归属正确；
//  3. 安全审计（平台豁免模型）手工限定：A/B 各看各的，平台视角全量监督。

// openBatchTenantTestDB 独立临时库 + 生产同款租户回调。
// 全局租户指向数据中不存在的 tenantZ：任何命中都只能来自显式构造的租户上下文。
func openBatchTenantTestDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "batch_tenant_test.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	wafdb.RegisterTenantScopeCallbacks(db)
	if err := db.AutoMigrate(models...); err != nil {
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
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"
	return db
}

// batchTenantAddReq 构造一条合法的本地来源批量任务入参
func batchTenantAddReq(t *testing.T, name string) request.BatchTaskAddReq {
	t.Helper()
	dir := t.TempDir()
	oldDirs := global.GCONFIG_BATCH_IMPORT_ALLOWED_DIRS
	global.GCONFIG_BATCH_IMPORT_ALLOWED_DIRS = dir
	t.Cleanup(func() { global.GCONFIG_BATCH_IMPORT_ALLOWED_DIRS = oldDirs })
	p := filepath.Join(dir, "ip.txt")
	if err := os.WriteFile(p, []byte("1.2.3.4\n"), 0o644); err != nil {
		t.Fatalf("准备来源文件失败: %v", err)
	}
	return request.BatchTaskAddReq{
		BatchTaskName:      name,
		BatchType:          enums.BATCHTASK_IPDENY,
		BatchSourceType:    enums.BATCHTASK_SOURCETYPE_LOCAL,
		BatchTriggerType:   enums.BATCHTASK_TRIGGERTYPE_CRON,
		BatchExecuteMethod: enums.BATCHTASK_EXECUTEMETHODAPPEND,
		BatchSource:        p,
	}
}

// TestBatchTaskTenantScope 批量任务的租户归属与跨租户不可见/不可改/不可删。
func TestBatchTaskTenantScope(t *testing.T) {
	db := openBatchTenantTestDB(t, &model.BatchTask{})
	pctx := wafctx.PlatformScope(context.Background())
	ctxA := wafctx.WithTenant(context.Background(), "tenant-a", "userA")
	ctxB := wafctx.WithTenant(context.Background(), "tenant-b", "userB")

	// 租户 A 建任务 → 行盖 A 的租户标识（而非全局 tenantZ）
	if err := WafBatchServiceApp.AddApi(ctxA, batchTenantAddReq(t, "deny-list")); err != nil {
		t.Fatalf("租户 A 建任务失败: %v", err)
	}
	var rowA model.BatchTask
	if err := db.WithContext(pctx).Where("batch_task_name = ?", "deny-list").First(&rowA).Error; err != nil {
		t.Fatalf("任务行不存在: %v", err)
	}
	if rowA.Tenant_ID != "tenant-a" || rowA.USER_CODE != "userA" {
		t.Fatalf("任务行应归属租户 A，实际 tenant=%q user=%q", rowA.Tenant_ID, rowA.USER_CODE)
	}

	// 同名任务：租户 B 的重名检查按 B 的作用域查不到 A 的行 → 允许 B 建同名任务
	if err := WafBatchServiceApp.AddApi(ctxB, batchTenantAddReq(t, "deny-list")); err != nil {
		t.Fatalf("租户 B 建同名任务应放行（重名检查已按租户隔离）: %v", err)
	}

	// 列表/详情：各看各的
	listA, totalA, _ := WafBatchServiceApp.GetListApi(ctxA, request.BatchTaskSearchReq{PageInfo: commonreq.PageInfo{PageIndex: 1, PageSize: 10}})
	if totalA != 1 || len(listA) != 1 || listA[0].Tenant_ID != "tenant-a" {
		t.Fatalf("租户 A 列表应只见自己的 1 条，实际 total=%d list=%d", totalA, len(listA))
	}
	if bean := WafBatchServiceApp.GetDetailByIdApi(ctxB, rowA.Id); bean.Id != "" {
		t.Fatalf("租户 B 不应查到 A 的任务详情")
	}

	// 修改：B 改 A 的任务被拒（fail-closed，明确报错而非静默成功）
	errB := WafBatchServiceApp.ModifyApi(ctxB, request.BatchTaskEditReq{
		Id: rowA.Id, BatchTaskName: "hijack", BatchType: enums.BATCHTASK_IPDENY,
		BatchSourceType: enums.BATCHTASK_SOURCETYPE_LOCAL, BatchTriggerType: enums.BATCHTASK_TRIGGERTYPE_CRON,
		BatchExecuteMethod: enums.BATCHTASK_EXECUTEMETHODAPPEND, BatchSource: rowA.BatchSource,
	})
	if errB == nil {
		t.Fatalf("租户 B 修改 A 的任务必须被拒")
	}
	var nameAfter model.BatchTask
	_ = db.WithContext(pctx).Where("id = ?", rowA.Id).First(&nameAfter).Error
	if nameAfter.BatchTaskName != "deny-list" {
		t.Fatalf("被拒的跨租户修改不应落库，实际任务名=%q", nameAfter.BatchTaskName)
	}

	// 删除：B 删 A 的任务被拒
	if err := WafBatchServiceApp.DelApi(ctxB, request.BatchTaskDeleteReq{Id: rowA.Id}); err == nil {
		t.Fatalf("租户 B 删除 A 的任务必须被拒")
	}
	var cntA int64
	db.WithContext(pctx).Model(&model.BatchTask{}).Where("id = ?", rowA.Id).Count(&cntA)
	if cntA != 1 {
		t.Fatalf("跨租户删除后 A 的任务行应仍在，实际 cnt=%d", cntA)
	}

	// A 删自己的任务成功
	if err := WafBatchServiceApp.DelApi(ctxA, request.BatchTaskDeleteReq{Id: rowA.Id}); err != nil {
		t.Fatalf("租户 A 删除自己的任务失败: %v", err)
	}

	// cron 装载（平台作用域）：跨租户装载全部定时任务
	cronList, cronTotal, _ := WafBatchServiceApp.GetAllCronListInner(pctx)
	if cronTotal != 1 || len(cronList) != 1 || cronList[0].Tenant_ID != "tenant-b" {
		t.Fatalf("平台作用域 cron 装载应见租户 B 的 1 条，实际 total=%d list=%d", cronTotal, len(cronList))
	}
	cronA, cronATotal, _ := WafBatchServiceApp.GetAllCronListInner(ctxA)
	if cronATotal != 0 || len(cronA) != 0 {
		t.Fatalf("租户 A 作用域 cron 装载不应见 B 的任务，实际 %d 条", cronATotal)
	}
}

// TestBatchProcessorWriteStamp 批量处理器同款写入模式：结构体不写死租户 + WithContext(任务租户)，
// 行落任务归属租户（对应 waftask/batch 各处理器改造后的落库方式）。
func TestBatchProcessorWriteStamp(t *testing.T) {
	db := openBatchTenantTestDB(t, &model.IPAllowList{})
	pctx := wafctx.PlatformScope(context.Background())
	// 模拟任务行自带租户标识（BatchTaskTenantCtx 的还原结果）
	taskCtx := wafctx.WithTenant(context.Background(), "tenant-a", "userA")

	bean := model.IPAllowList{
		BaseOrm: baseorm.BaseOrm{Id: uuid.GenUUID()},
		HostCode: "h-a",
		Ip:       "9.9.9.9",
		IpType:   model.IPEntryTypeIP,
		IsEnable: 1,
	}
	if err := global.GWAF_LOCAL_DB.WithContext(taskCtx).Create(&bean).Error; err != nil {
		t.Fatalf("写入白名单行失败: %v", err)
	}
	var row model.IPAllowList
	if err := db.WithContext(pctx).Where("id = ?", bean.Id).First(&row).Error; err != nil {
		t.Fatalf("白名单行不存在: %v", err)
	}
	if row.Tenant_ID != "tenant-a" || row.USER_CODE != "userA" {
		t.Fatalf("白名单行应盖任务租户标识，实际 tenant=%q user=%q", row.Tenant_ID, row.USER_CODE)
	}
	// 他租户（含全局回退）看不见这行 → 不会成为全引擎共享白名单
	ctxB := wafctx.WithTenant(context.Background(), "tenant-b", "userB")
	var cntB, cntGlobal int64
	db.WithContext(ctxB).Model(&model.IPAllowList{}).Where("host_code = ?", "h-a").Count(&cntB)
	db.Model(&model.IPAllowList{}).Where("host_code = ?", "h-a").Count(&cntGlobal)
	if cntB != 0 || cntGlobal != 0 {
		t.Fatalf("他租户/全局回退不应见到 A 的白名单行，实际 B=%d global=%d", cntB, cntGlobal)
	}
}

// TestSecurityAuditListTenantScope 安全审计（平台豁免模型）的手工租户限定：
// 写侧按请求租户落列，读侧租户只见自己、平台视角全量监督。
func TestSecurityAuditListTenantScope(t *testing.T) {
	logDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "audit_tenant_test.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开测试日志库失败: %v", err)
	}
	if err := logDB.AutoMigrate(&model.SecurityAuditLog{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	oldDB := global.GWAF_LOCAL_LOG_DB
	t.Cleanup(func() {
		global.GWAF_LOCAL_LOG_DB = oldDB
		if sqlDB, e := logDB.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	global.GWAF_LOCAL_LOG_DB = logDB

	ctxA := wafctx.WithTenant(context.Background(), "tenant-a", "userA")
	ctxB := wafctx.WithTenant(context.Background(), "tenant-b", "userB")
	pctx := wafctx.PlatformScope(context.Background())

	// A、B 各落一条（WriteCtx），兼容入口 Write 落平台（全局）桶
	WafSecurityAuditServiceApp.WriteCtx(ctxA, AuditEntry{Event: model.AccessEventLoginOK, AccountName: "a-admin"})
	WafSecurityAuditServiceApp.WriteCtx(ctxB, AuditEntry{Event: model.AccessEventLoginOK, AccountName: "b-admin"})
	WafSecurityAuditServiceApp.Write(AuditEntry{Event: model.AuditEventConfigBatchTaskRun, AccountName: "system(cron)"})

	// 写侧落列正确（豁免模型回调不代劳，必须手工盖章）
	var rowA, rowB, rowP model.SecurityAuditLog
	if err := logDB.Where("account_name = ?", "a-admin").First(&rowA).Error; err != nil {
		t.Fatalf("租户 A 审计行不存在: %v", err)
	}
	if err := logDB.Where("account_name = ?", "b-admin").First(&rowB).Error; err != nil {
		t.Fatalf("租户 B 审计行不存在: %v", err)
	}
	if err := logDB.Where("account_name = ?", "system(cron)").First(&rowP).Error; err != nil {
		t.Fatalf("平台审计行不存在: %v", err)
	}
	if rowA.Tenant_ID != "tenant-a" || rowB.Tenant_ID != "tenant-b" || rowP.Tenant_ID != global.GWAF_TENANT_ID {
		t.Fatalf("审计行归属不符: A=%q B=%q platform=%q", rowA.Tenant_ID, rowB.Tenant_ID, rowP.Tenant_ID)
	}

	// 读侧：租户只见自己名下（不再是他租户可见的单一全局桶）
	page := request.WafAccessAuditSearchReq{PageInfo: commonreq.PageInfo{PageIndex: 1, PageSize: 10}}
	listA, totalA, err := WafSecurityAuditServiceApp.GetListApi(ctxA, page)
	if err != nil || totalA != 1 || len(listA) != 1 || listA[0].AccountName != "a-admin" {
		t.Fatalf("租户 A 应只见自己的 1 条审计，实际 total=%d err=%v", totalA, err)
	}
	listB, totalB, _ := WafSecurityAuditServiceApp.GetListApi(ctxB, page)
	if totalB != 1 || len(listB) != 1 || listB[0].AccountName != "b-admin" {
		t.Fatalf("租户 B 应只见自己的 1 条审计，实际 total=%d", totalB)
	}
	// 平台视角（平台作用域 / 无上下文）全量监督
	listP, totalP, _ := WafSecurityAuditServiceApp.GetListApi(pctx, page)
	if totalP != 3 || len(listP) != 3 {
		t.Fatalf("平台作用域应见全部 3 条审计，实际 total=%d", totalP)
	}
	listN, totalN, _ := WafSecurityAuditServiceApp.GetListApi(context.Background(), page)
	if totalN != 3 || len(listN) != 3 {
		t.Fatalf("无上下文（平台视角）应见全部 3 条审计，实际 total=%d", totalN)
	}
}
