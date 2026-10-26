package waf_service

import (
	"SamWaf/common/uuid"
	"SamWaf/customtype"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/request"
	"context"
	"sync"
	"time"
)

// notifyLogWriteMutex 用于串行化通知日志写入，防止大量goroutine并发写入SQLite导致崩溃
var notifyLogWriteMutex sync.Mutex

type WafNotifyLogService struct{}

var WafNotifyLogServiceApp = new(WafNotifyLogService)

// AddLog 添加通知日志（使用互斥锁串行化写入，防止并发写SQLite崩溃）。
// 租户归属由 before_create 回调按 ctx 盖章。
func (receiver *WafNotifyLogService) AddLog(ctx context.Context, channelId, channelName, channelType, messageType, messageTitle, messageContent string, recipients string, status int, errorMsg string) error {
	var bean = &model.NotifyLog{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},
		ChannelId:      channelId,
		ChannelName:    channelName,
		ChannelType:    channelType,
		MessageType:    messageType,
		MessageTitle:   messageTitle,
		MessageContent: messageContent,
		Recipients:     recipients,
		Status:         status,
		ErrorMsg:       errorMsg,
		SendTime:       time.Now().Format("2006-01-02 15:04:05"),
	}

	notifyLogWriteMutex.Lock()
	defer notifyLogWriteMutex.Unlock()
	return global.GWAF_LOCAL_LOG_DB.WithContext(ctx).Create(bean).Error
}

// AddLogDetail 添加通知日志（结构化入参，支持订阅ID/模板来源等新字段），返回日志ID。
//
// 无 ctx 兼容入口：调用方是消息队列驱动的通知发送管线（waf_notify_sender.go），
// 队列里没有请求上下文，显式落全局租户归属（该管线按订阅分发，租户化属于通知
// 工作流的后续任务）。请求链路写通知日志请走 AddLogDetailCtx。
func (receiver *WafNotifyLogService) AddLogDetail(bean model.NotifyLog) (string, error) {
	return receiver.AddLogDetailCtx(context.Background(), bean)
}

// AddLogDetailCtx 添加通知日志（带租户上下文，行归属由回调按 ctx 盖章）
func (receiver *WafNotifyLogService) AddLogDetailCtx(ctx context.Context, bean model.NotifyLog) (string, error) {
	bean.BaseOrm = baseorm.BaseOrm{
		Id:          uuid.GenUUID(),
		CREATE_TIME: customtype.JsonTime(time.Now()),
		UPDATE_TIME: customtype.JsonTime(time.Now()),
	}
	if bean.SendTime == "" {
		bean.SendTime = time.Now().Format("2006-01-02 15:04:05")
	}

	notifyLogWriteMutex.Lock()
	defer notifyLogWriteMutex.Unlock()
	return bean.Id, global.GWAF_LOCAL_LOG_DB.WithContext(ctx).Create(&bean).Error
}

// AddSuppressLog 记录一条"被抑制"的通知日志
//
// 频控引擎保证一个抑制窗口只会调用一次，所以这里不会因为抑制量大而刷库。
// 调用方（通知发送管线）无请求上下文，行归属回退全局租户，见 AddLogDetail 注释。
func (receiver *WafNotifyLogService) AddSuppressLog(bean model.NotifyLog) (string, error) {
	bean.Status = model.NotifyLogStatusSuppress
	if bean.SuppressCount <= 0 {
		bean.SuppressCount = 1
	}
	return receiver.AddLogDetail(bean)
}

// UpdateSuppressCount 回填抑制条数（抑制窗口结束时调用）。
// 调用方（通知发送管线）无请求上下文；UPDATE 按 id 定位，行归属随发送链路，暂不加租户过滤。
func (receiver *WafNotifyLogService) UpdateSuppressCount(id string, count int) error {
	if id == "" || count <= 0 {
		return nil
	}
	notifyLogWriteMutex.Lock()
	defer notifyLogWriteMutex.Unlock()
	return global.GWAF_LOCAL_LOG_DB.Model(&model.NotifyLog{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"SuppressCount": count,
			"UPDATE_TIME":   customtype.JsonTime(time.Now()),
		}).Error
}

// GetListApi 获取列表（按 ctx 租户过滤）
func (receiver *WafNotifyLogService) GetListApi(ctx context.Context, req request.WafNotifyLogSearchReq) ([]model.NotifyLog, int64, error) {
	var list []model.NotifyLog
	var total int64 = 0

	var whereField = ""
	var whereValues []interface{}

	if len(req.ChannelId) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " channel_id = ? "
		whereValues = append(whereValues, req.ChannelId)
	}

	if len(req.MessageType) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " message_type = ? "
		whereValues = append(whereValues, req.MessageType)
	}

	if req.Status > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " status = ? "
		whereValues = append(whereValues, req.Status)
	}

	if len(req.StartTime) > 0 && len(req.EndTime) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " send_time >= ? and send_time <= ? "
		whereValues = append(whereValues, req.StartTime, req.EndTime)
	}

	global.GWAF_LOCAL_LOG_DB.WithContext(ctx).Model(&model.NotifyLog{}).Where(whereField, whereValues...).Order("create_time desc").Limit(req.PageSize).Offset(req.PageSize * (req.PageIndex - 1)).Find(&list)
	global.GWAF_LOCAL_LOG_DB.WithContext(ctx).Model(&model.NotifyLog{}).Where(whereField, whereValues...).Count(&total)

	return list, total, nil
}

// GetDetailApi 获取详情（按 ctx 租户过滤）
func (receiver *WafNotifyLogService) GetDetailApi(ctx context.Context, req request.WafNotifyLogDetailReq) model.NotifyLog {
	var bean model.NotifyLog
	global.GWAF_LOCAL_LOG_DB.WithContext(ctx).Where("id=?", req.Id).Find(&bean)
	return bean
}

// DelApi 删除（DELETE 随 ctx 租户过滤，只能删到本租户的行）
func (receiver *WafNotifyLogService) DelApi(ctx context.Context, req request.WafNotifyLogDelReq) error {
	return global.GWAF_LOCAL_LOG_DB.WithContext(ctx).Where("id = ?", req.Id).Delete(&model.NotifyLog{}).Error
}
