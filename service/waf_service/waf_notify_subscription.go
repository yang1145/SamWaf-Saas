package waf_service

import (
	"SamWaf/common/uuid"
	"SamWaf/common/wafctx"
	"SamWaf/customtype"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/request"
	"SamWaf/wafdb/dialect"
	"context"
	"errors"
	"time"
)

// 本文件分两类入口：
//  - 管理端 CRUD（AddApi/ModifyApi/DelApi/Get*Api/CheckIsExistApi）：已接入请求 ctx，
//    读写均随请求租户过滤，租户只能看到/操作自己的通知订阅；
//  - 配置与发送侧（GetById/SaveConfig*/GetSubscriptions*）：GetSubscriptionsForEvent
//    已按事件归属租户取数（租户桶 + 平台桶），其余配置读取仍走调用方传入的 ctx。

type WafNotifySubscriptionService struct{}

var WafNotifySubscriptionServiceApp = new(WafNotifySubscriptionService)

// AddApi 添加通知订阅。租户归属由 before_create 回调按 ctx 盖章。
func (receiver *WafNotifySubscriptionService) AddApi(ctx context.Context, req request.WafNotifySubscriptionAddReq) error {
	var bean = &model.NotifySubscription{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},
		ChannelId:   req.ChannelId,
		MessageType: req.MessageType,
		Recipients:  req.Recipients,
		Status:      req.Status,
		FilterJSON:  req.FilterJSON,
		Remarks:     req.Remarks,
	}
	return global.GWAF_LOCAL_DB.WithContext(ctx).Create(bean).Error
}

// CheckIsExistApi 检查是否存在（按 ctx 租户判定重复订阅）
func (receiver *WafNotifySubscriptionService) CheckIsExistApi(ctx context.Context, req request.WafNotifySubscriptionAddReq) error {
	return global.GWAF_LOCAL_DB.WithContext(ctx).First(&model.NotifySubscription{}, "channel_id = ? and message_type = ? ", req.ChannelId, req.MessageType).Error
}

// ModifyApi 修改通知订阅（UPDATE 随 ctx 租户过滤，改不到他租户行时显式报错）
func (receiver *WafNotifySubscriptionService) ModifyApi(ctx context.Context, req request.WafNotifySubscriptionEditReq) error {
	var exist model.NotifySubscription
	if err := global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", req.Id).First(&exist).Error; err != nil {
		return errors.New("订阅不存在或无权操作")
	}
	editMap := map[string]interface{}{
		"ChannelId":   req.ChannelId,
		"MessageType": req.MessageType,
		"Recipients":  req.Recipients,
		"Status":      req.Status,
		"FilterJSON":  req.FilterJSON,
		"Remarks":     req.Remarks,
		"UPDATE_TIME": customtype.JsonTime(time.Now()),
	}
	return global.GWAF_LOCAL_DB.WithContext(ctx).Model(model.NotifySubscription{}).Where("id = ?", req.Id).Updates(editMap).Error
}

// GetDetailApi 获取通知订阅详情（按 ctx 租户过滤）
func (receiver *WafNotifySubscriptionService) GetDetailApi(ctx context.Context, req request.WafNotifySubscriptionDetailReq) model.NotifySubscription {
	var bean model.NotifySubscription
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("id=?", req.Id).Find(&bean)
	return bean
}

// GetListApi 获取通知订阅列表（按 ctx 租户过滤）
func (receiver *WafNotifySubscriptionService) GetListApi(ctx context.Context, req request.WafNotifySubscriptionSearchReq) ([]model.NotifySubscription, int64, error) {
	var list []model.NotifySubscription
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

	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.NotifySubscription{}).Where(whereField, whereValues...).Limit(req.PageSize).Order("create_time desc").Offset(req.PageSize * (req.PageIndex - 1)).Find(&list)
	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.NotifySubscription{}).Where(whereField, whereValues...).Count(&total)

	return list, total, nil
}

// DelApi 删除通知订阅（DELETE 随 ctx 租户过滤，只能删到本租户的行）
func (receiver *WafNotifySubscriptionService) DelApi(ctx context.Context, req request.WafNotifySubscriptionDelReq) error {
	return global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", req.Id).Delete(&model.NotifySubscription{}).Error
}

// GetById 按主键取订阅（无 ctx 版本：通知发送侧使用，全局回退）
func (receiver *WafNotifySubscriptionService) GetById(id string) (model.NotifySubscription, error) {
	return receiver.GetByIdCtx(context.Background(), id)
}

// GetByIdCtx 按主键取订阅（随请求 ctx 走租户过滤：租户只能取到自己的订阅，
// 跨租户 id 按"订阅不存在"处理，不泄露存在性）
func (receiver *WafNotifySubscriptionService) GetByIdCtx(ctx context.Context, id string) (model.NotifySubscription, error) {
	var bean model.NotifySubscription
	err := global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", id).First(&bean).Error
	return bean, err
}

// SaveConfigApi 保存单个订阅的精细化配置（频控/模板/过滤）
//
// 刻意与 ModifyApi 分开：ModifyApi 会被前端的开关切换整包提交，
// 合在一起的话漏传一个字段就会把用户配好的模板清空。
func (receiver *WafNotifySubscriptionService) SaveConfigApi(id, throttleMode, throttleJSON, filterJSON,
	titleTemplate, contentTemplate string) error {
	editMap := map[string]interface{}{
		"ThrottleMode":    throttleMode,
		"ThrottleJSON":    throttleJSON,
		"FilterJSON":      filterJSON,
		"TitleTemplate":   titleTemplate,
		"ContentTemplate": contentTemplate,
		"UPDATE_TIME":     customtype.JsonTime(time.Now()),
	}
	return receiver.SaveConfigPartialApiCtx(context.Background(), id, editMap)
}

// SaveConfigPartialApiCtx 按需保存部分配置（批量套用时使用）。
// UPDATE 随 ctx 走租户过滤：租户请求改不到他租订阅（before_update 仅在 ctx
// 携带租户时追加过滤，平台维护无 ctx 保持全量可达）
func (receiver *WafNotifySubscriptionService) SaveConfigPartialApiCtx(ctx context.Context, id string, editMap map[string]interface{}) error {
	if len(editMap) == 0 {
		return nil
	}
	editMap["UPDATE_TIME"] = customtype.JsonTime(time.Now())
	return global.GWAF_LOCAL_DB.WithContext(ctx).Model(model.NotifySubscription{}).Where("id = ?", id).Updates(editMap).Error
}

// GetSubscriptionsByChannelType 取某类渠道下的全部订阅（批量套用配置用，无 ctx：发送侧）
func (receiver *WafNotifySubscriptionService) GetSubscriptionsByChannelType(channelType string) []model.NotifySubscription {
	return receiver.GetSubscriptionsByChannelTypeCtx(context.Background(), channelType)
}

// GetSubscriptionsByChannelTypeCtx 随请求 ctx 按租户过滤（请求侧批量套用只触达本租户订阅）
func (receiver *WafNotifySubscriptionService) GetSubscriptionsByChannelTypeCtx(ctx context.Context, channelType string) []model.NotifySubscription {
	var subs []model.NotifySubscription
	var channels []model.NotifyChannel
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("type = ?", channelType).Find(&channels)
	if len(channels) == 0 {
		return subs
	}
	ids := make([]string, 0, len(channels))
	for _, ch := range channels {
		ids = append(ids, ch.Id)
	}
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("channel_id in ?", ids).Find(&subs)
	return subs
}

// GetSubscriptionsByMessageTypeAll 取某消息类型的全部订阅（含已禁用，批量套用配置用，无 ctx：发送侧）
func (receiver *WafNotifySubscriptionService) GetSubscriptionsByMessageTypeAll(messageType string) []model.NotifySubscription {
	return receiver.GetSubscriptionsByMessageTypeAllCtx(context.Background(), messageType)
}

// GetSubscriptionsByMessageTypeAllCtx 随请求 ctx 按租户过滤
func (receiver *WafNotifySubscriptionService) GetSubscriptionsByMessageTypeAllCtx(ctx context.Context, messageType string) []model.NotifySubscription {
	var subs []model.NotifySubscription
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("message_type = ?", messageType).Find(&subs)
	return subs
}

// GetSubscriptionsByMessageType 根据消息类型获取启用中的订阅（随 ctx 租户过滤）
func (receiver *WafNotifySubscriptionService) GetSubscriptionsByMessageType(ctx context.Context, messageType string) []model.NotifySubscription {
	var subscriptions []model.NotifySubscription
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("message_type = ? and "+dialect.Q("status")+" = ?", messageType, 1).Find(&subscriptions)
	return subscriptions
}

// GetSubscriptionsForEvent 按事件归属租户取启用中的订阅。
//
// 租户事件同时投给「该租户的订阅」和「平台侧订阅」：平台管理员配的订阅是跨租户监管，
// 口径与 WebSocket「平台侧连接可总览」一致；只投平台桶会让租户收不到自己的告警，
// 只投租户桶又会让平台的监管订阅噤声。
// 事件未携带租户（=平台事件或无归属）时只取平台桶，维持失败关闭。
func (receiver *WafNotifySubscriptionService) GetSubscriptionsForEvent(tenantID, messageType string) []model.NotifySubscription {
	scopes := []context.Context{context.Background()}
	if tenantID != "" && tenantID != global.GWAF_TENANT_ID {
		scopes = []context.Context{
			wafctx.WithTenant(context.Background(), tenantID, ""),
			context.Background(),
		}
	}
	seen := make(map[string]struct{})
	var list []model.NotifySubscription
	for _, ctx := range scopes {
		for _, sub := range receiver.GetSubscriptionsByMessageType(ctx, messageType) {
			if _, ok := seen[sub.Id]; ok {
				continue
			}
			seen[sub.Id] = struct{}{}
			list = append(list, sub)
		}
	}
	return list
}
