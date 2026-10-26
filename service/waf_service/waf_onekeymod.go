package waf_service

import (
	"context"

	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/request"
)

type WafOneKeyModService struct{}

var WafOneKeyModServiceApp = new(WafOneKeyModService)

// 一键修改记录（LOG 库，BaseOrm 租户隔离）：查询/删除随请求 ctx 走租户过滤，
// 租户只见/只删自己发起的记录；记录归属由执行链路（wafonekey.oneKeyModScope）落定。
func (receiver *WafOneKeyModService) GetDetailApi(ctx context.Context, req request.WafOneKeyModDetailReq) model.OneKeyMod {
	var bean model.OneKeyMod
	global.GWAF_LOCAL_LOG_DB.WithContext(ctx).Where("id=?", req.Id).Find(&bean)
	return bean
}
func (receiver *WafOneKeyModService) GetListApi(ctx context.Context, req request.WafOneKeyModSearchReq) ([]model.OneKeyMod, int64, error) {
	var list []model.OneKeyMod
	var total int64 = 0

	/*where条件*/
	var whereField = ""
	var whereValues []interface{}
	//where字段
	whereField = ""

	db := global.GWAF_LOCAL_LOG_DB.WithContext(ctx).Model(&model.OneKeyMod{})
	if err := db.Where(whereField, whereValues...).Limit(req.PageSize).Offset(req.PageSize * (req.PageIndex - 1)).Order("create_time desc").Find(&list).Error; err != nil {
		return nil, 0, err
	}
	if err := global.GWAF_LOCAL_LOG_DB.WithContext(ctx).Model(&model.OneKeyMod{}).Where(whereField, whereValues...).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	return list, total, nil
}
func (receiver *WafOneKeyModService) DelApi(ctx context.Context, req request.WafOneKeyModDelReq) error {
	db := global.GWAF_LOCAL_LOG_DB.WithContext(ctx)
	var bean model.OneKeyMod
	err := db.Where("id = ?", req.Id).First(&bean).Error
	if err != nil {
		return err
	}
	err = db.Where("id = ?", req.Id).Delete(model.OneKeyMod{}).Error
	return err
}
