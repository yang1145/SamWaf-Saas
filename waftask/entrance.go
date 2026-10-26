package waftask

import (
	"SamWaf/common/uuid"
	"SamWaf/common/zlog"
	"SamWaf/customtype"
	"SamWaf/enums"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/service/waf_service"
	"context"
	"time"
)

var (
	wafTaskService = waf_service.WafTaskServiceApp
)

// InitTaskDb 从数据库加载任务列表
// 任务初始化已经通过 migration 管理，这里只负责加载
func InitTaskDb() []model.Task {
	zlog.Info("从数据库加载任务列表...")

	// 包内自注册任务的调度行幂等预置（按 task_method 查重，已存在跳过）。
	// 放这里而不是迁移文件：注册逻辑整体收敛在 waftask 包内，
	// 预置后返回的列表带上它，main.go 既有调度循环即按每日定时挂载。
	ensureTenantExpireTaskRow()

	// 阶段0：调度器仍只装载平台租户（全局）的任务行，与改造前一致；
	// 无租户上下文时查询回调回退全局过滤。租户自建任务的调度装载属后续阶段。
	list, total, err := wafTaskService.GetList(context.Background())
	if err != nil {
		zlog.Error("加载任务列表失败", "error", err.Error())
		return []model.Task{}
	}

	zlog.Info("任务列表加载完成", "任务总数", total)
	return list
}

// ensureTenantExpireTaskRow 幂等预置租户到期巡检任务行（每日一次），
// 与 wafdb/migrations_task.go 系统任务的口径一致：按 task_method 查重。
func ensureTenantExpireTaskRow() {
	if global.GWAF_LOCAL_DB == nil {
		zlog.Warn("本地数据库未初始化，跳过租户到期巡检任务预置")
		return
	}
	var count int64
	if err := global.GWAF_LOCAL_DB.Model(&model.Task{}).
		Where("task_method = ?", tenantExpireTaskMethod).Count(&count).Error; err != nil {
		zlog.Error("检查租户到期巡检任务失败", "error", err.Error())
		return
	}
	if count > 0 {
		return
	}
	now := customtype.JsonTime(time.Now())
	task := model.Task{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   global.GWAF_TENANT_ID,
			CREATE_TIME: now,
			UPDATE_TIME: now,
		},
		TaskName:   "每天09:40进行租户到期巡检与提醒",
		TaskUnit:   enums.TASK_DAY,
		TaskValue:  1,
		TaskAt:     "09:40",
		TaskMethod: tenantExpireTaskMethod,
	}
	if err := global.GWAF_LOCAL_DB.Create(&task).Error; err != nil {
		zlog.Error("创建租户到期巡检任务失败", "error", err.Error())
		return
	}
	zlog.Info("租户到期巡检任务创建成功", "task_method", tenantExpireTaskMethod)
}
