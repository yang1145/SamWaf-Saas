package waf_service

import (
	"SamWaf/common/uuid"
	"SamWaf/customtype"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/request"
	"context"
	"errors"
	"time"
)

type WafTaskService struct{}

var WafTaskServiceApp = new(WafTaskService)

// AddApi 新增定时任务。租户归属由 before_create 回调按 ctx 盖章，
// 不再手工写死全局值；ctx 未声明租户时回调回退全局常量。
func (receiver *WafTaskService) AddApi(ctx context.Context, req request.WafTaskAddReq) error {
	var bean = &model.Task{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},

		TaskName:          req.TaskName,
		TaskUnit:          req.TaskUnit,
		TaskValue:         req.TaskValue,
		TaskAt:            req.TaskAt,
		TaskMethod:        req.TaskMethod,
		TaskDaysOfTheWeek: req.TaskDaysOfTheWeek,
	}
	return global.GWAF_LOCAL_DB.WithContext(ctx).Create(bean).Error
}

func (receiver *WafTaskService) Add(ctx context.Context, req model.Task) error {
	var bean = &model.Task{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},

		TaskName:          req.TaskName,
		TaskUnit:          req.TaskUnit,
		TaskValue:         req.TaskValue,
		TaskAt:            req.TaskAt,
		TaskMethod:        req.TaskMethod,
		TaskDaysOfTheWeek: req.TaskDaysOfTheWeek,
	}
	return global.GWAF_LOCAL_DB.WithContext(ctx).Create(bean).Error
}

func (receiver *WafTaskService) CheckIsExistApi(ctx context.Context, req request.WafTaskAddReq) int {
	var total int64 = 0
	/*where条件*/
	var whereField = ""
	var whereValues []interface{}
	//where字段
	whereField = ""

	if len(req.TaskName) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " task_name=? "
	}

	//where字段赋值
	if len(req.TaskName) > 0 {
		if len(whereField) > 0 {
			whereValues = append(whereValues, req.TaskName)
		}
	}

	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Task{}).Where(whereField, whereValues...).Count(&total)
	return int(total)
}

func (receiver *WafTaskService) CheckIsExist(ctx context.Context, taskMethod string) int {
	var total int64 = 0
	/*where条件*/
	var whereField = ""
	var whereValues []interface{}
	//where字段
	whereField = ""

	if len(taskMethod) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " task_method=? "
	}

	//where字段赋值
	if len(taskMethod) > 0 {
		if len(whereField) > 0 {
			whereValues = append(whereValues, taskMethod)
		}
	}

	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Task{}).Where(whereField, whereValues...).Count(&total)
	return int(total)
}

func (receiver *WafTaskService) ModifyApi(ctx context.Context, req request.WafTaskEditReq) error {
	// 先按 ctx 租户确认目标任务归属本租户：UPDATE 虽有租户过滤，
	// 但提前判定可以在改不到行时给出明确错误，而不是静默"编辑成功"。
	var exist model.Task
	if err := global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", req.Id).First(&exist).Error; err != nil {
		return errors.New("任务不存在或无权操作")
	}

	// 根据唯一字段生成查询条件（只有在UniFields不为空时才进行存在性检查）
	var total int64 = 0
	/*where条件*/
	var whereField = ""
	var whereValues []interface{}
	//where字段
	whereField = ""

	if len(req.TaskName) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " task_name=? "
	}

	//where字段赋值

	if len(req.TaskName) > 0 {
		whereValues = append(whereValues, req.TaskName)
	}

	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Task{}).Where(whereField, whereValues...).Count(&total)
	// 查询是否已存在记录
	var bean model.Task
	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Task{}).Where(whereField, whereValues...).Limit(1).Find(&bean)

	if int(total) > 0 && bean.Id != "" && bean.Id != req.Id {
		return errors.New("当前记录已经存在")
	}

	beanMap := map[string]interface{}{

		"TaskName":          req.TaskName,
		"TaskUnit":          req.TaskUnit,
		"TaskAt":            req.TaskAt,
		"TaskMethod":        req.TaskMethod,
		"TaskValue":         req.TaskValue,
		"TaskDaysOfTheWeek": req.TaskDaysOfTheWeek,
		"UPDATE_TIME":       customtype.JsonTime(time.Now()),
	}
	return global.GWAF_LOCAL_DB.WithContext(ctx).Model(model.Task{}).Where("id = ?", req.Id).Updates(beanMap).Error
}
func (receiver *WafTaskService) GetDetailApi(ctx context.Context, req request.WafTaskDetailReq) model.Task {
	var bean model.Task
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("id=?", req.Id).Find(&bean)
	return bean
}
func (receiver *WafTaskService) GetDetailByIdApi(ctx context.Context, id string) model.Task {
	var bean model.Task
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("id=?", id).Find(&bean)
	return bean
}
func (receiver *WafTaskService) GetListApi(ctx context.Context, req request.WafTaskSearchReq) ([]model.Task, int64, error) {
	var list []model.Task
	var total int64 = 0
	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Task{}).Limit(req.PageSize).Offset(req.PageSize * (req.PageIndex - 1)).Find(&list)
	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Task{}).Count(&total)

	return list, total, nil
}

// GetList 全量任务清单，供调度器启动装载。
// 调度入口传 PlatformScope 装载全部租户任务，或按现状传无租户 ctx 只装载平台任务行。
func (receiver *WafTaskService) GetList(ctx context.Context) ([]model.Task, int64, error) {
	var list []model.Task
	var total int64 = 0
	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Task{}).Find(&list)
	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Task{}).Count(&total)

	return list, total, nil
}
func (receiver *WafTaskService) DelApi(ctx context.Context, req request.WafTaskDelReq) error {
	var bean model.Task
	err := global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", req.Id).First(&bean).Error
	if err != nil {
		return err
	}
	return global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", req.Id).Delete(model.Task{}).Error
}
func (receiver *WafTaskService) DelByMethod(ctx context.Context, taskMethod string) error {
	var bean model.Task
	err := global.GWAF_LOCAL_DB.WithContext(ctx).Where("task_method = ?", taskMethod).First(&bean).Error
	if err != nil {
		return err
	}
	return global.GWAF_LOCAL_DB.WithContext(ctx).Where("task_method = ?", taskMethod).Delete(model.Task{}).Error
}
