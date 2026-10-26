package batch

import (
	"SamWaf/common/uuid"
	"SamWaf/common/zlog"
	"SamWaf/customtype"
	"SamWaf/enums"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/spec"
	"context"
	"fmt"
	"time"
)

// IPDenyProcessor 黑名单IP处理器
type IPDenyProcessor struct{}

// ProcessBatch 处理一批IP（ctx 携带任务归属租户，写入行落该租户名下）
func (p *IPDenyProcessor) ProcessBatch(ctx context.Context, items []string, task model.BatchTask, progress *BatchProgress) bool {
	if len(items) == 0 {
		return false
	}

	logName := "BatchTask-IPDenyBatch"
	zlog.Info(logName, fmt.Sprintf("处理黑名单批次，包含 %d 个IP", len(items)))

	// 获取已存在的记录
	existMap := p.GetExistingItems(ctx, items, task, nil)

	// 根据执行方法处理
	if task.BatchExecuteMethod == enums.BATCHTASK_EXECUTEMETHODAPPEND {
		return p.processAppendBatch(ctx, items, existMap, task, logName, progress)
	} else if task.BatchExecuteMethod == enums.BATCHTASK_EXECUTEMETHODOVERWRITE {
		return p.processOverwriteBatch(ctx, items, existMap, task, logName, progress)
	}

	return false
}

// GetExistingItems 获取已存在的IP记录（按任务归属租户过滤，去重只对该租户的行）
func (p *IPDenyProcessor) GetExistingItems(ctx context.Context, items []string, task model.BatchTask, config interface{}) map[string]interface{} {
	existMap := make(map[string]interface{})
	var existIPs []model.IPBlockList

	// 使用IN查询一次性获取所有已存在的记录
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("host_code = ? AND ip IN (?)", task.BatchHostCode, items).Find(&existIPs)

	// 将已存在的IP放入map中，方便快速查找
	for _, ip := range existIPs {
		existMap[ip.Ip] = ip
	}

	return existMap
}

// NotifyEngine 通知引擎更新（站点表单内容也限定任务归属租户的行）
func (p *IPDenyProcessor) NotifyEngine(ctx context.Context, task model.BatchTask) {
	var ipBlocks []model.IPBlockList
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("host_code = ? and is_enable = 1", task.BatchHostCode).Find(&ipBlocks)
	var chanInfo = spec.ChanCommonHost{
		HostCode: task.BatchHostCode,
		Type:     enums.ChanTypeBlockIP,
		Content:  ipBlocks,
	}
	global.GWAF_CHAN_MSG <- chanInfo
}

// processAppendBatch 处理追加模式的批次
func (p *IPDenyProcessor) processAppendBatch(ctx context.Context, items []string, existMap map[string]interface{}, task model.BatchTask, logName string, progress *BatchProgress) bool {
	// 收集需要插入的记录
	var toInsert []model.IPBlockList
	for _, ip := range items {
		// 如果IP不存在，则添加到待插入列表
		if _, exists := existMap[ip]; !exists {
			// 租户归属不手工写死：before_create 回调按 ctx（任务归属租户）盖章
			toInsert = append(toInsert, model.IPBlockList{
				BaseOrm: baseorm.BaseOrm{
					Id:          uuid.GenUUID(),
					CREATE_TIME: customtype.JsonTime(time.Now()),
					UPDATE_TIME: customtype.JsonTime(time.Now()),
				},
				HostCode: task.BatchHostCode,
				Ip:       ip,
				Remarks:  time.Now().Format("20060102") + "批量导入 任务ID:" + task.Id,
				// 显式标记为「单条IP」：批量导入不产生 IP 组引用
				IpType: model.IPEntryTypeIP,
				// 显式启用：字段无 default 标签，零值会被如实落库为停用
				IsEnable: 1,
			})
		}
	}

	// 批量插入新记录
	if len(toInsert) > 0 {
		tx := global.GWAF_LOCAL_DB.WithContext(ctx).Begin()
		if err := tx.Create(&toInsert).Error; err != nil {
			tx.Rollback()
			zlog.Error(logName, "批量插入黑名单IP失败: "+err.Error())
			return false
		}
		tx.Commit()

		zlog.Info(logName, fmt.Sprintf("成功插入 %d 条黑名单IP记录", len(toInsert)))
		// 更新进度统计
		progress.AddInserted(len(toInsert))
		return len(toInsert) > 0
	}

	return false
}

// processOverwriteBatch 处理覆写模式的批次
func (p *IPDenyProcessor) processOverwriteBatch(ctx context.Context, items []string, existMap map[string]interface{}, task model.BatchTask, logName string, progress *BatchProgress) bool {
	// 收集需要插入和更新的记录
	var toInsert []model.IPBlockList
	var toUpdate []model.IPBlockList

	for _, ip := range items {
		if existIP, exists := existMap[ip]; exists {
			// 已存在，需要更新
			ipRecord := existIP.(model.IPBlockList)
			ipRecord.Remarks = time.Now().Format("20060102") + "批量导入编辑 任务ID:" + task.Id
			ipRecord.UPDATE_TIME = customtype.JsonTime(time.Now())
			toUpdate = append(toUpdate, ipRecord)
		} else {
			// 不存在，需要插入（租户归属由回调按 ctx 盖章）
			toInsert = append(toInsert, model.IPBlockList{
				BaseOrm: baseorm.BaseOrm{
					Id:          uuid.GenUUID(),
					CREATE_TIME: customtype.JsonTime(time.Now()),
					UPDATE_TIME: customtype.JsonTime(time.Now()),
				},
				HostCode: task.BatchHostCode,
				Ip:       ip,
				Remarks:  time.Now().Format("20060102") + "批量导入 任务ID:" + task.Id,
				// 显式标记为「单条IP」：批量导入不产生 IP 组引用
				IpType: model.IPEntryTypeIP,
				// 显式启用：字段无 default 标签，零值会被如实落库为停用
				IsEnable: 1,
			})
		}
	}

	// 开始事务
	tx := global.GWAF_LOCAL_DB.WithContext(ctx).Begin()
	hasChanges := false

	// 批量插入新记录
	if len(toInsert) > 0 {
		if err := tx.Create(&toInsert).Error; err != nil {
			tx.Rollback()
			zlog.Error(logName, "批量插入黑名单IP失败: "+err.Error())
			return false
		}
		zlog.Info(logName, fmt.Sprintf("成功插入 %d 条黑名单IP记录", len(toInsert)))
		// 更新进度统计
		progress.AddInserted(len(toInsert))
		hasChanges = true
	}

	// 批量更新已存在的记录
	if len(toUpdate) > 0 {
		for _, record := range toUpdate {
			if err := tx.Model(&model.IPBlockList{}).Where("id = ?", record.Id).Updates(map[string]interface{}{
				"Remarks":     record.Remarks,
				"UPDATE_TIME": record.UPDATE_TIME,
			}).Error; err != nil {
				tx.Rollback()
				zlog.Error(logName, "批量更新黑名单IP失败: "+err.Error())
				return false
			}
		}
		zlog.Info(logName, fmt.Sprintf("成功更新 %d 条黑名单IP记录", len(toUpdate)))
		// 更新进度统计
		progress.AddUpdated(len(toUpdate))
		hasChanges = true
	}

	// 提交事务
	tx.Commit()
	return hasChanges
}
