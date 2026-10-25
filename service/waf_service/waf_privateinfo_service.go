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

type WafPrivateInfoService struct{}

var WafPrivateInfoServiceApp = new(WafPrivateInfoService)

func (receiver *WafPrivateInfoService) AddApi(ctx context.Context, req request.WafPrivateInfoAddReq) error {
	var bean = &model.PrivateInfo{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   global.GWAF_TENANT_ID,
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},

		PrivateKey:              req.PrivateKey,
		PrivateValue:            req.PrivateValue,
		PrivateGroupName:        req.PrivateGroupName,
		PrivateGroupBelongCloud: req.PrivateGroupBelongCloud,
		Remarks:                 req.Remarks,
	}
	global.GWAF_LOCAL_DB.WithContext(ctx).Create(bean)
	return nil
}

func (receiver *WafPrivateInfoService) CheckIsExistApi(ctx context.Context, req request.WafPrivateInfoAddReq) int {
	var total int64 = 0
	/*where条件*/
	var whereField = ""
	var whereValues []interface{}
	//where字段
	whereField = ""

	if len(req.PrivateKey) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " private_key=? "
	}
	if len(req.PrivateGroupName) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " private_group_name=? "
	}
	if len(req.PrivateGroupBelongCloud) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " private_group_belong_cloud=? "
	}
	//where字段赋值

	if len(req.PrivateKey) > 0 {
		if len(whereField) > 0 {
			whereValues = append(whereValues, req.PrivateKey)
		}
	}
	if len(req.PrivateGroupName) > 0 {
		if len(whereField) > 0 {
			whereValues = append(whereValues, req.PrivateGroupName)
		}
	}
	if len(req.PrivateGroupBelongCloud) > 0 {
		if len(whereField) > 0 {
			whereValues = append(whereValues, req.PrivateGroupBelongCloud)
		}
	}

	global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.PrivateInfo{}).Where(whereField, whereValues...).Count(&total)
	return int(total)
}

func (receiver *WafPrivateInfoService) ModifyWithOutValueApi(ctx context.Context, req request.WafPrivateInfoEditReq) error {
	// 根据唯一字段生成查询条件（只有在UniFields不为空时才进行存在性检查）
	var total int64 = 0
	/*where条件*/
	var whereField = ""
	var whereValues []interface{}
	//where字段
	whereField = ""

	if len(req.PrivateKey) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " private_key=? "
	}
	if len(req.PrivateGroupName) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " private_group_name=? "
	}
	if len(req.PrivateGroupBelongCloud) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " private_group_belong_cloud=? "
	}

	//where字段赋值

	if len(req.PrivateKey) > 0 {
		whereValues = append(whereValues, req.PrivateKey)
	}
	if len(req.PrivateGroupName) > 0 {
		if len(whereField) > 0 {
			whereValues = append(whereValues, req.PrivateGroupName)
		}
	}
	if len(req.PrivateGroupBelongCloud) > 0 {
		if len(whereField) > 0 {
			whereValues = append(whereValues, req.PrivateGroupBelongCloud)
		}
	}

	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Model(&model.PrivateInfo{}).Where(whereField, whereValues...).Count(&total)
	// 查询是否已存在记录
	var bean model.PrivateInfo
	db.Model(&model.PrivateInfo{}).Where(whereField, whereValues...).Limit(1).Find(&bean)

	if int(total) > 0 && bean.PrivateKey != "" && bean.PrivateKey != req.PrivateKey {
		return errors.New("当前记录已经存在")
	}

	beanMap := map[string]interface{}{
		"PrivateKey":              req.PrivateKey,
		"Remarks":                 req.Remarks,
		"PrivateGroupName":        req.PrivateGroupName,
		"PrivateGroupBelongCloud": req.PrivateGroupBelongCloud,
		"UPDATE_TIME":             customtype.JsonTime(time.Now()),
	}
	err := db.Model(model.PrivateInfo{}).Where("private_key = ?", req.PrivateKey).Updates(beanMap).Error

	return err
}

func (receiver *WafPrivateInfoService) ModifyApi(ctx context.Context, req request.WafPrivateInfoEditReq) error {
	// 根据唯一字段生成查询条件（只有在UniFields不为空时才进行存在性检查）

	var total int64 = 0
	/*where条件*/
	var whereField = ""
	var whereValues []interface{}
	//where字段
	whereField = ""

	if len(req.PrivateKey) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " private_key=? "
	}
	if len(req.PrivateGroupName) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " private_group_name=? "
	}
	if len(req.PrivateGroupBelongCloud) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " private_group_belong_cloud=? "
	}
	//where字段赋值

	if len(req.PrivateKey) > 0 {
		whereValues = append(whereValues, req.PrivateKey)
	}

	if len(req.PrivateGroupName) > 0 {
		if len(whereField) > 0 {
			whereValues = append(whereValues, req.PrivateGroupName)
		}
	}
	if len(req.PrivateGroupBelongCloud) > 0 {
		if len(whereField) > 0 {
			whereValues = append(whereValues, req.PrivateGroupBelongCloud)
		}
	}

	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Model(&model.PrivateInfo{}).Where(whereField, whereValues...).Count(&total)
	// 查询是否已存在记录
	var bean model.PrivateInfo
	db.Model(&model.PrivateInfo{}).Where(whereField, whereValues...).Limit(1).Find(&bean)

	if int(total) > 0 && bean.PrivateKey != "" && bean.PrivateKey != req.PrivateKey {
		return errors.New("当前记录已经存在")
	}

	beanMap := map[string]interface{}{

		"PrivateKey":              req.PrivateKey,
		"PrivateValue":            req.PrivateValue,
		"PrivateGroupName":        req.PrivateGroupName,
		"PrivateGroupBelongCloud": req.PrivateGroupBelongCloud,
		"Remarks":                 req.Remarks,

		"UPDATE_TIME": customtype.JsonTime(time.Now()),
	}
	err := db.Model(model.PrivateInfo{}).Where("private_key = ?", req.PrivateKey).Updates(beanMap).Error

	return err
}
func (receiver *WafPrivateInfoService) GetDetailApi(ctx context.Context, req request.WafPrivateInfoDetailReq) model.PrivateInfo {
	var bean model.PrivateInfo
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("id=?", req.Id).Find(&bean)
	return bean
}
func (receiver *WafPrivateInfoService) GetDetailByIdApi(ctx context.Context, id string) model.PrivateInfo {
	var bean model.PrivateInfo
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("id=?", id).Find(&bean)
	return bean
}
func (receiver *WafPrivateInfoService) GetListApi(ctx context.Context, req request.WafPrivateInfoSearchReq) ([]model.PrivateInfo, int64, error) {
	var list []model.PrivateInfo
	var total int64 = 0
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Model(&model.PrivateInfo{}).Limit(req.PageSize).Offset(req.PageSize * (req.PageIndex - 1)).Find(&list)
	db.Model(&model.PrivateInfo{}).Count(&total)

	return list, total, nil
}
func (receiver *WafPrivateInfoService) DelApi(ctx context.Context, req request.WafPrivateInfoDelReq) error {
	var bean model.PrivateInfo
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	err := db.Where("id = ?", req.Id).First(&bean).Error
	if err != nil {
		return err
	}
	err = db.Where("id = ?", req.Id).Delete(model.PrivateInfo{}).Error
	return err
}

func (receiver *WafPrivateInfoService) GetListPureApi(ctx context.Context) ([]model.PrivateInfo, int64, error) {
	var list []model.PrivateInfo
	var total int64 = 0
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Model(&model.PrivateInfo{}).Find(&list)
	db.Model(&model.PrivateInfo{}).Count(&total)

	return list, total, nil
}

// GetListByGroupAndBelongCloudPureApi 依据分组名称和所属云查询
func (receiver *WafPrivateInfoService) GetListByGroupAndBelongCloudPureApi(ctx context.Context, groupName string, belongCloud string) ([]model.PrivateInfo, int64, error) {
	var list []model.PrivateInfo
	var total int64 = 0
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Model(&model.PrivateInfo{}).Where("private_group_name = ? and private_group_belong_cloud=?", groupName, belongCloud).Find(&list)
	db.Model(&model.PrivateInfo{}).Where("private_group_name = ? and private_group_belong_cloud=?", groupName, belongCloud).Count(&total)

	return list, total, nil
}
