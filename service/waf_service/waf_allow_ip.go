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

type WafWhiteIpService struct{}

var WafWhiteIpServiceApp = new(WafWhiteIpService)

func (receiver *WafWhiteIpService) AddApi(ctx context.Context, wafWhiteIpAddReq request.WafAllowIpAddReq) error {
	isEnable := 1
	if wafWhiteIpAddReq.IsEnable != nil {
		isEnable = *wafWhiteIpAddReq.IsEnable
	}
	var wafHost = &model.IPAllowList{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   global.GWAF_TENANT_ID,
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},
		HostCode:  wafWhiteIpAddReq.HostCode,
		Ip:        wafWhiteIpAddReq.Ip,
		Remarks:   wafWhiteIpAddReq.Remarks,
		IpType:    wafWhiteIpAddReq.IpType,
		GroupCode: wafWhiteIpAddReq.GroupCode,
		IsEnable:  isEnable,
	}
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Create(wafHost)
	return nil
}

// ToggleApi 切换条目启用状态，返回所属网站编码供调用方通知引擎
func (receiver *WafWhiteIpService) ToggleApi(ctx context.Context, req request.WafAllowIpToggleReq) (string, error) {
	var bean model.IPAllowList
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	if err := db.Where("id = ?", req.Id).First(&bean).Error; err != nil {
		return "", err
	}
	err := db.Model(model.IPAllowList{}).Where("id = ?", req.Id).Updates(map[string]interface{}{
		"is_enable":   *req.IsEnable,
		"UPDATE_TIME": customtype.JsonTime(time.Now()),
	}).Error
	return bean.HostCode, err
}

// CheckIsExistApi 判重。唯一性由 (host_code, ip_type, ip, group_code) 共同决定：
// 同一站点重复引用同一个 IP 组也算重复。
// ip_type 用 IN (”, 'ip') 兼容存量行的空串。
func (receiver *WafWhiteIpService) CheckIsExistApi(ctx context.Context, wafWhiteIpAddReq request.WafAllowIpAddReq) error {
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	if wafWhiteIpAddReq.IpType == model.IPEntryTypeGroup {
		return db.First(&model.IPAllowList{},
			"host_code = ? and ip_type = ? and group_code = ?",
			wafWhiteIpAddReq.HostCode, model.IPEntryTypeGroup, wafWhiteIpAddReq.GroupCode).Error
	}
	return db.First(&model.IPAllowList{},
		"host_code = ? and ip = ? and (ip_type is null or ip_type in (?, ?))",
		wafWhiteIpAddReq.HostCode, wafWhiteIpAddReq.Ip, "", model.IPEntryTypeIP).Error
}
func (receiver *WafWhiteIpService) ModifyApi(ctx context.Context, wafWhiteIpEditReq request.WafAllowIpEditReq) error {
	// 同一站点下不能出现重复条目（排除自身）
	var dup model.IPAllowList
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	if wafWhiteIpEditReq.IpType == model.IPEntryTypeGroup {
		db.Where("host_code = ? and ip_type = ? and group_code = ? and id <> ?",
			wafWhiteIpEditReq.HostCode, model.IPEntryTypeGroup, wafWhiteIpEditReq.GroupCode, wafWhiteIpEditReq.Id).Find(&dup)
	} else {
		db.Where("host_code = ? and ip = ? and (ip_type is null or ip_type in (?, ?)) and id <> ?",
			wafWhiteIpEditReq.HostCode, wafWhiteIpEditReq.Ip, "", model.IPEntryTypeIP, wafWhiteIpEditReq.Id).Find(&dup)
	}
	if dup.Id != "" {
		return errors.New("当前网站和IP已经存在")
	}
	ipWhiteMap := map[string]interface{}{
		"host_code":   wafWhiteIpEditReq.HostCode,
		"Ip":          wafWhiteIpEditReq.Ip,
		"Remarks":     wafWhiteIpEditReq.Remarks,
		"ip_type":     wafWhiteIpEditReq.IpType,
		"group_code":  wafWhiteIpEditReq.GroupCode,
		"UPDATE_TIME": customtype.JsonTime(time.Now()),
	}
	if wafWhiteIpEditReq.IsEnable != nil {
		ipWhiteMap["is_enable"] = *wafWhiteIpEditReq.IsEnable
	}
	err := db.Model(model.IPAllowList{}).Where("id = ?", wafWhiteIpEditReq.Id).Updates(ipWhiteMap).Error

	return err
}
func (receiver *WafWhiteIpService) GetDetailApi(ctx context.Context, req request.WafAllowIpDetailReq) model.IPAllowList {
	var ipWhite model.IPAllowList
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Where("id=?", req.Id).Find(&ipWhite)
	return ipWhite
}
func (receiver *WafWhiteIpService) GetDetailByIdApi(ctx context.Context, id string) model.IPAllowList {
	var ipWhite model.IPAllowList
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Where("id=?", id).Find(&ipWhite)
	return ipWhite
}
func (receiver *WafWhiteIpService) GetDetailByIPApi(ctx context.Context, ip string, hostCode string) model.IPAllowList {
	var ipWhite model.IPAllowList
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Where("ip=? and host_code=?", ip, hostCode).Find(&ipWhite)
	return ipWhite
}
func (receiver *WafWhiteIpService) GetListApi(ctx context.Context, req request.WafAllowIpSearchReq) ([]model.IPAllowList, int64, error) {
	var ipWhites []model.IPAllowList
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
	if len(req.Ip) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " ip =? "
	}
	//按引用的IP组筛选：ip 是精确匹配，组引用行的 ip 为空，用 ip 条件永远查不到
	if len(req.GroupCode) > 0 {
		if len(whereField) > 0 {
			whereField = whereField + " and "
		}
		whereField = whereField + " group_code =? "
	}
	//where字段赋值
	if len(req.HostCode) > 0 {
		whereValues = append(whereValues, req.HostCode)
	}
	if len(req.Ip) > 0 {
		whereValues = append(whereValues, req.Ip)
	}
	if len(req.GroupCode) > 0 {
		whereValues = append(whereValues, req.GroupCode)
	}

	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	db.Model(&model.IPAllowList{}).Where(whereField, whereValues...).Limit(req.PageSize).Offset(req.PageSize * (req.PageIndex - 1)).Find(&ipWhites)
	db.Model(&model.IPAllowList{}).Where(whereField, whereValues...).Count(&total)

	return ipWhites, total, nil
}
func (receiver *WafWhiteIpService) DelApi(ctx context.Context, req request.WafAllowIpDelReq) error {
	var ipWhite model.IPAllowList
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	err := db.Where("id = ?", req.Id).First(&ipWhite).Error
	if err != nil {
		return err
	}
	err = db.Where("id = ?", req.Id).Delete(model.IPAllowList{}).Error
	return err
}

// BatchDelApi 批量删除指定ID的IP白名单
func (receiver *WafWhiteIpService) BatchDelApi(ctx context.Context, req request.WafAllowIpBatchDelReq) error {
	if len(req.Ids) == 0 {
		return errors.New("删除ID列表不能为空")
	}

	// 先检查所有ID是否存在
	var count int64
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	err := db.Model(&model.IPAllowList{}).Where("id IN ?", req.Ids).Count(&count).Error
	if err != nil {
		return err
	}

	if count != int64(len(req.Ids)) {
		return errors.New("部分ID不存在")
	}

	// 执行批量删除
	err = db.Where("id IN ?", req.Ids).Delete(&model.IPAllowList{}).Error
	return err
}

// DelAllApi 删除指定网站的所有IP白名单
func (receiver *WafWhiteIpService) DelAllApi(ctx context.Context, req request.WafAllowIpDelAllReq) error {
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
	err := db.Model(&model.IPAllowList{}).Where(whereCondition, whereValues...).Count(&count).Error
	if err != nil {
		return err
	}

	if count == 0 {
		return errors.New("没有IP白名单记录")
	}

	// 执行删除
	err = db.Where(whereCondition, whereValues...).Delete(&model.IPAllowList{}).Error
	return err
}

// GetHostCodesByIds 根据ID数组获取对应的HostCode列表
func (receiver *WafWhiteIpService) GetHostCodesByIds(ctx context.Context, ids []string) ([]string, error) {
	var hostCodes []string
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	err := db.Model(&model.IPAllowList{}).Where("id IN ?", ids).Distinct("host_code").Pluck("host_code", &hostCodes).Error
	return hostCodes, err
}

// GetHostCodes 获取所有HostCode列表
func (receiver *WafWhiteIpService) GetHostCodes(ctx context.Context) ([]string, error) {
	var hostCodes []string
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	err := db.Model(&model.IPAllowList{}).Where("user_code = ? AND tenant_id = ?", global.GWAF_USER_CODE, global.GWAF_TENANT_ID).Distinct("host_code").Pluck("host_code", &hostCodes).Error
	return hostCodes, err
}
