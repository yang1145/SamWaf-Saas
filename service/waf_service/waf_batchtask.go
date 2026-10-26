package waf_service

import (
	"SamWaf/common/uuid"
	"SamWaf/customtype"
	"SamWaf/enums"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/request"
	"SamWaf/utils"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

type WafBatchTaskService struct{}

var WafBatchServiceApp = new(WafBatchTaskService)

// AddApi 新增批量任务。
// 租户归属不再手工写死全局值：before_create 回调按 ctx 盖章（请求侧即请求租户），
// ctx 未声明租户时回调回退全局常量，单租户实例行为不变。
func (receiver *WafBatchTaskService) AddApi(ctx context.Context, req request.BatchTaskAddReq) error {

	err := receiver.CheckIsExistApi(ctx, req.BatchTaskName)
	if err == nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("任务名已存在")
	}
	if err = receiver.checkTaskInput(req.BatchType, req.BatchSourceType, req.BatchTriggerType, req.BatchExecuteMethod, req.BatchSource); err != nil {
		return err
	}
	if err = receiver.checkExtraConfig(ctx, req.BatchType, req.BatchExtraConfig); err != nil {
		return err
	}
	var bean = &model.BatchTask{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},
		BatchTaskName:      req.BatchTaskName,
		BatchHostCode:      req.BatchHostCode,
		BatchExecuteMethod: req.BatchExecuteMethod,
		BatchSource:        req.BatchSource,
		BatchSourceType:    req.BatchSourceType,
		BatchTriggerType:   req.BatchTriggerType,
		BatchType:          req.BatchType,
		BatchExtraConfig:   req.BatchExtraConfig,
		Remark:             req.Remark,
	}
	return global.GWAF_LOCAL_DB.WithContext(ctx).Create(bean).Error
}

// checkTaskInput 保存前校验四个枚举字段与数据源。
func (receiver *WafBatchTaskService) checkTaskInput(batchType, sourceType, triggerType, executeMethod, source string) error {
	if !enums.IsValidBatchType(batchType) {
		return errors.New("任务类型非法")
	}
	if !enums.IsValidBatchSourceType(sourceType) {
		return errors.New("来源类型非法")
	}
	if !enums.IsValidBatchTriggerType(triggerType) {
		return errors.New("触发类型非法")
	}
	if !enums.IsValidBatchExecuteMethod(executeMethod) {
		return errors.New("执行方式非法")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("来源内容不能为空")
	}
	if enums.IsBatchLocalSource(sourceType) {
		return utils.PrecheckBatchLocalPath(source)
	}
	if ok, reason := utils.PrecheckOutboundURL(source); !ok {
		return errors.New("来源地址不被允许: " + reason)
	}
	return nil
}

// checkExtraConfig 保存前校验额外配置。
//
// IP组任务必须指向一个真实存在的组：留到执行时才发现配错，用户只能去翻日志
// 才知道定时任务一直在空跑，所以在这里就挡下来。
// 目标组按 ctx 租户过滤存在性，防止任务跨租户引用他人 IP 组。
func (receiver *WafBatchTaskService) checkExtraConfig(ctx context.Context, batchType string, extraConfig string) error {
	if batchType != enums.BATCHTASK_IPGROUP {
		return nil
	}
	var config struct {
		GroupCode string `json:"group_code"`
	}
	if extraConfig != "" {
		if err := json.Unmarshal([]byte(extraConfig), &config); err != nil {
			return errors.New("额外配置不是合法的JSON:" + err.Error())
		}
	}
	if config.GroupCode == "" {
		return errors.New("请选择目标IP组")
	}
	var cnt int64
	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.IPGroup{}).
		Where("group_code = ?", config.GroupCode).Count(&cnt)
	if cnt == 0 {
		return errors.New("目标IP组不存在")
	}
	return nil
}

func (receiver *WafBatchTaskService) CheckIsExistApi(ctx context.Context, batchName string) error {
	return global.GWAF_LOCAL_DB.WithContext(ctx).First(&model.BatchTask{}, "batch_task_name = ?", batchName).Error
}

func (receiver *WafBatchTaskService) ModifyApi(ctx context.Context, req request.BatchTaskEditReq) error {
	// 先按 ctx 租户确认目标任务归属本租户：后面的 UPDATE 虽有租户过滤，
	// 但提前判定可以在改不到行时给出明确错误，而不是静默"编辑成功"。
	var exist model.BatchTask
	if err := global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", req.Id).First(&exist).Error; err != nil {
		return errors.New("任务不存在或无权操作")
	}

	var bean model.BatchTask
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("batch_task_name = ?", req.BatchTaskName).Find(&bean)
	if bean.Id != "" && bean.BatchTaskName != req.BatchTaskName {
		return errors.New("该任务已经存在")
	}
	if err := receiver.checkTaskInput(req.BatchType, req.BatchSourceType, req.BatchTriggerType, req.BatchExecuteMethod, req.BatchSource); err != nil {
		return err
	}
	if err := receiver.checkExtraConfig(ctx, req.BatchType, req.BatchExtraConfig); err != nil {
		return err
	}

	beanMap := map[string]interface{}{
		"BatchTaskName":      req.BatchTaskName,
		"BatchHostCode":      req.BatchHostCode,
		"BatchExecuteMethod": req.BatchExecuteMethod,
		"BatchSource":        req.BatchSource,
		"BatchSourceType":    req.BatchSourceType,
		"BatchTriggerType":   req.BatchTriggerType,
		"BatchType":          req.BatchType,
		"BatchExtraConfig":   req.BatchExtraConfig,
		"Remark":             req.Remark,
	}
	return global.GWAF_LOCAL_DB.WithContext(ctx).Model(model.BatchTask{}).Where("id = ?", req.Id).Updates(beanMap).Error
}

// GetDetailApi
func (receiver *WafBatchTaskService) GetDetailApi(ctx context.Context, req request.BatchTaskDetailReq) model.BatchTask {
	var bean model.BatchTask
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("id=?", req.Id).Find(&bean)
	return bean
}

// GetDetailByIdApi
func (receiver *WafBatchTaskService) GetDetailByIdApi(ctx context.Context, id string) model.BatchTask {
	var bean model.BatchTask
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("id=?", id).Find(&bean)
	return bean
}
func (receiver *WafBatchTaskService) GetListApi(ctx context.Context, req request.BatchTaskSearchReq) ([]model.BatchTask, int64, error) {
	var list []model.BatchTask
	var total int64 = 0
	var whereField = ""
	var whereValues []interface{}

	if len(req.BatchTaskName) > 0 {
		whereField += "batch_task_name like ?"
		whereValues = append(whereValues, "%"+req.BatchTaskName+"%")
	}

	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.BatchTask{}).Where(whereField, whereValues...).Limit(req.PageSize).Offset(req.PageSize * (req.PageIndex - 1)).Find(&list)
	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.BatchTask{}).Where(whereField, whereValues...).Count(&total)
	return list, total, nil
}

// GetAllCronListInner 定时批量任务清单，供 cron 装载。
// cron 以 PlatformScope 调用：跨租户装载全部定时任务，执行侧按任务行自带租户逐个构 ctx。
func (receiver *WafBatchTaskService) GetAllCronListInner(ctx context.Context) ([]model.BatchTask, int64, error) {
	var list []model.BatchTask
	var total int64 = 0
	var whereField = "batch_trigger_type = ?"
	var whereValues []interface{}
	whereValues = append(whereValues, "cron")

	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.BatchTask{}).Where(whereField, whereValues...).Find(&list)
	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.BatchTask{}).Where(whereField, whereValues...).Count(&total)
	return list, total, nil
}

func (receiver *WafBatchTaskService) DelApi(ctx context.Context, req request.BatchTaskDeleteReq) error {
	var bean model.BatchTask
	err := global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", req.Id).First(&bean).Error
	if err != nil {
		return err
	}
	return global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", req.Id).Delete(model.BatchTask{}).Error
}
