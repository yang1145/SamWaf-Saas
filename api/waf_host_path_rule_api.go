package api

import (
	"SamWaf/enums"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/common/response"
	"SamWaf/model/request"
	"SamWaf/model/spec"
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type WafHostPathRuleApi struct{}

// AddApi 新增路径规则
func (w *WafHostPathRuleApi) AddApi(c *gin.Context) {
	var req request.WafHostPathRuleAddReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	if wafHostPathRuleService.CheckIsExistApi(c.Request.Context(), req) > 0 {
		response.FailWithMessage("当前记录已经存在", c)
		return
	}
	if err := wafHostPathRuleService.AddApi(c.Request.Context(), req); err != nil {
		response.FailWithMessage("添加失败", c)
		return
	}
	w.NotifyWaf(c.Request.Context(), req.HostCode)
	response.OkWithMessage("添加成功", c)
}

// GetDetailApi 获取路径规则详情
func (w *WafHostPathRuleApi) GetDetailApi(c *gin.Context) {
	var req request.WafHostPathRuleDetailReq
	if err := c.ShouldBind(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	bean := wafHostPathRuleService.GetDetailApi(c.Request.Context(), req)
	response.OkWithDetailed(bean, "获取成功", c)
}

// GetListApi 获取路径规则列表
func (w *WafHostPathRuleApi) GetListApi(c *gin.Context) {
	var req request.WafHostPathRuleSearchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	list, total, _ := wafHostPathRuleService.GetListApi(c.Request.Context(), req)
	response.OkWithDetailed(response.PageResult{
		List:      list,
		Total:     total,
		PageIndex: req.PageIndex,
		PageSize:  req.PageSize,
	}, "获取成功", c)
}

// DelApi 删除路径规则
func (w *WafHostPathRuleApi) DelApi(c *gin.Context) {
	var req request.WafHostPathRuleDelReq
	if err := c.ShouldBind(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	bean := wafHostPathRuleService.GetDetailByIdApi(c.Request.Context(), req.Id)
	if bean.Id == "" {
		response.FailWithMessage("未找到信息", c)
		return
	}
	err := wafHostPathRuleService.DelApi(c.Request.Context(), req)
	if err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
		response.FailWithMessage("请检测参数", c)
	} else if err != nil {
		response.FailWithMessage("发生错误", c)
	} else {
		w.NotifyWaf(c.Request.Context(), bean.HostCode)
		response.OkWithMessage("删除成功", c)
	}
}

// ModifyApi 编辑路径规则
func (w *WafHostPathRuleApi) ModifyApi(c *gin.Context) {
	var req request.WafHostPathRuleEditReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	//编辑前先取旧记录，拿到可能被本次编辑改掉的旧 host_code(issue #898)
	bean := wafHostPathRuleService.GetDetailByIdApi(c.Request.Context(), req.Id)
	if err := wafHostPathRuleService.ModifyApi(c.Request.Context(), req); err != nil {
		response.FailWithMessage("编辑发生错误"+err.Error(), c)
		return
	}
	notifyWafHostChanged(func(hostCode string) { w.NotifyWaf(c.Request.Context(), hostCode) }, bean.HostCode, req.HostCode)
	response.OkWithMessage("编辑成功", c)
}

// NotifyWaf 通知 WAF 引擎实时生效
func (w *WafHostPathRuleApi) NotifyWaf(ctx context.Context, hostCode string) {
	var list []model.HostPathRule
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("host_code = ?", hostCode).Order("priority asc, create_time asc").Find(&list)
	chanInfo := spec.ChanCommonHost{
		HostCode: hostCode,
		Type:     enums.ChanTypeHostPathRule,
		Content:  list,
	}
	global.GWAF_CHAN_MSG <- chanInfo
}
