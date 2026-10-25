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

type WafWhiteUrlService struct{}

var WafWhiteUrlServiceApp = new(WafWhiteUrlService)

func (receiver *WafWhiteUrlService) AddApi(ctx context.Context, req request.WafAllowUrlAddReq) error {
	isEnable := 1
	if req.IsEnable != nil {
		isEnable = *req.IsEnable
	}
	var bean = &model.URLAllowList{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   global.GWAF_TENANT_ID,
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},
		HostCode:    req.HostCode,
		CompareType: req.CompareType,
		Url:         req.Url,
		Remarks:     req.Remarks,
		IsEnable:    isEnable,
	}
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Create(bean)
	return nil
}

// ToggleApi 切换条目启用状态，返回所属网站编码供调用方通知引擎
func (receiver *WafWhiteUrlService) ToggleApi(ctx context.Context, req request.WafAllowUrlToggleReq) (string, error) {
	var bean model.URLAllowList
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	if err := db.Where("id = ?", req.Id).First(&bean).Error; err != nil {
		return "", err
	}
	err := db.Model(model.URLAllowList{}).Where("id = ?", req.Id).Updates(map[string]interface{}{
		"is_enable":   *req.IsEnable,
		"UPDATE_TIME": customtype.JsonTime(time.Now()),
	}).Error
	return bean.HostCode, err
}

func (receiver *WafWhiteUrlService) CheckIsExistApi(ctx context.Context, req request.WafAllowUrlAddReq) error {
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	return db.First(&model.URLAllowList{}, "host_code = ? and url= ?", req.HostCode,
		req.Url).Error
}
func (receiver *WafWhiteUrlService) ModifyApi(ctx context.Context, req request.WafAllowUrlEditReq) error {
	var ipWhite model.URLAllowList
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Where("host_code = ? and url= ?", req.HostCode,
		req.Url).Find(&ipWhite)
	if ipWhite.Id != "" && ipWhite.Url != req.Url {
		return errors.New("当前网站和url已经存在")
	}
	ipWhiteMap := map[string]interface{}{
		"host_code":    req.HostCode,
		"compare_type": req.CompareType,
		"Url":          req.Url,
		"Remarks":      req.Remarks,
		"UPDATE_TIME":  customtype.JsonTime(time.Now()),
	}
	if req.IsEnable != nil {
		ipWhiteMap["is_enable"] = *req.IsEnable
	}
	err := db.Model(model.URLAllowList{}).Where("id = ?", req.Id).Updates(ipWhiteMap).Error

	return err
}
func (receiver *WafWhiteUrlService) GetDetailApi(ctx context.Context, req request.WafAllowUrlDetailReq) model.URLAllowList {
	var bean model.URLAllowList
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Where("id=?", req.Id).Find(&bean)
	return bean
}
func (receiver *WafWhiteUrlService) GetDetailByIdApi(ctx context.Context, id string) model.URLAllowList {
	var bean model.URLAllowList
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Where("id=?", id).Find(&bean)
	return bean
}
func (receiver *WafWhiteUrlService) GetListApi(ctx context.Context, req request.WafAllowUrlSearchReq) ([]model.URLAllowList, int64, error) {
	var list []model.URLAllowList
	var total int64 = 0
	/*where条件*/
	var whereField = ""
	var whereValues []interface{}
	//where字段
	whereField = ""
	if len(req.HostCode) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " host_code=? "
	}
	if len(req.Url) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " url =? "
	}
	//where字段赋值
	if len(req.HostCode) > 0 {
		whereValues = append(whereValues, req.HostCode)
	}
	if len(req.Url) > 0 {
		whereValues = append(whereValues, req.Url)
	}

	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Model(&model.URLAllowList{}).Where(whereField, whereValues...).Limit(req.PageSize).Offset(req.PageSize * (req.PageIndex - 1)).Find(&list)
	db.Model(&model.URLAllowList{}).Where(whereField, whereValues...).Count(&total)

	return list, total, nil
}
func (receiver *WafWhiteUrlService) DelApi(ctx context.Context, req request.WafAllowUrlDelReq) error {
	var ipWhite model.URLAllowList
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	err := db.Where("id = ?", req.Id).First(&ipWhite).Error
	if err != nil {
		return err
	}
	err = db.Where("id = ?", req.Id).Delete(model.URLAllowList{}).Error
	return err
}

// 批量删除方法
func (receiver *WafWhiteUrlService) BatchDelApi(ctx context.Context, req request.WafAllowUrlBatchDelReq) error {
	// 添加用户和租户验证
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	err := db.Where("id IN ? AND user_code = ? AND tenant_id = ?", req.Ids, global.GWAF_USER_CODE, global.GWAF_TENANT_ID).Delete(&model.URLAllowList{}).Error
	return err
}

// 全部删除方法
func (receiver *WafWhiteUrlService) DelAllApi(ctx context.Context, req request.WafAllowUrlDelAllReq) error {
	var whereCondition string
	var whereValues []interface{}

	if len(req.HostCode) > 0 {
		whereCondition = "host_code = ? AND user_code = ? AND tenant_id = ?"
		whereValues = append(whereValues, req.HostCode, global.GWAF_USER_CODE, global.GWAF_TENANT_ID)
	} else {
		whereCondition = "user_code = ? AND tenant_id = ?"
		whereValues = append(whereValues, global.GWAF_USER_CODE, global.GWAF_TENANT_ID)
	}

	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	// 先检查是否存在记录
	var count int64
	err := db.Model(&model.URLAllowList{}).Where(whereCondition, whereValues...).Count(&count).Error
	if err != nil {
		return err
	}

	if count == 0 {
		return errors.New("没有URL白名单记录")
	}

	// 执行删除
	err = db.Where(whereCondition, whereValues...).Delete(&model.URLAllowList{}).Error
	return err
}

// GetHostCodesByIds 根据ID数组获取对应的HostCode列表
func (receiver *WafWhiteUrlService) GetHostCodesByIds(ctx context.Context, ids []string) ([]string, error) {
	var hostCodes []string
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	err := db.Model(&model.URLAllowList{}).Where("id IN ?", ids).Distinct("host_code").Pluck("host_code", &hostCodes).Error
	return hostCodes, err
}

// GetHostCodes 获取所有HostCode列表
func (receiver *WafWhiteUrlService) GetHostCodes(ctx context.Context) ([]string, error) {
	var hostCodes []string
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	err := db.Model(&model.URLAllowList{}).Where("user_code = ? AND tenant_id = ?", global.GWAF_USER_CODE, global.GWAF_TENANT_ID).Distinct("host_code").Pluck("host_code", &hostCodes).Error
	return hostCodes, err
}
