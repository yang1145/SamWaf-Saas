package api

import (
	"SamWaf/model/common/response"
	"SamWaf/model/request"

	"github.com/gin-gonic/gin"
)

type WafTenantApi struct {
}

// AddApi 开通租户
// @Summary      开通租户
// @Description  创建租户行并完成 provisioning：复制平台预置权限组、创建租户管理员组与账号
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        data  body      request.WafTenantAddReq  true  "租户信息"
// @Success      200   {object}  response.Response  "开通成功"
// @Security     ApiKeyAuth
// @Router       /tenant/add [post]
func (w *WafTenantApi) AddApi(c *gin.Context) {
	var req request.WafTenantAddReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	bean, err := wafTenantService.AddTenantApi(c.Request.Context(), req)
	if err != nil {
		response.FailWithMessage(err.Error(), c)
		return
	}
	response.OkWithDetailed(bean, "开通成功", c)
}

// EditApi 编辑租户
// @Summary      编辑租户
// @Description  修改租户名/套餐/配额/宽限期/状态/到期时间，改完失效请求期策略缓存立即生效
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        data  body      request.WafTenantEditReq  true  "租户信息"
// @Success      200   {object}  response.Response  "编辑成功"
// @Security     ApiKeyAuth
// @Router       /tenant/edit [post]
func (w *WafTenantApi) EditApi(c *gin.Context) {
	var req request.WafTenantEditReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	bean, err := wafTenantService.EditTenantApi(c.Request.Context(), req)
	if err != nil {
		response.FailWithMessage(err.Error(), c)
		return
	}
	response.OkWithDetailed(bean, "编辑成功", c)
}

// RenewApi 续期租户
// @Summary      续期租户
// @Description  按绝对到期时间或顺延天数续期，状态恢复启用并失效策略缓存
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        data  body      request.WafTenantRenewReq  true  "续期信息"
// @Success      200   {object}  response.Response  "续期成功"
// @Security     ApiKeyAuth
// @Router       /tenant/renew [post]
func (w *WafTenantApi) RenewApi(c *gin.Context) {
	var req request.WafTenantRenewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	bean, err := wafTenantService.RenewTenantApi(c.Request.Context(), req)
	if err != nil {
		response.FailWithMessage(err.Error(), c)
		return
	}
	response.OkWithDetailed(bean, "续期成功", c)
}

// DelApi 注销租户
// @Summary      注销租户
// @Description  软删除：状态置为已注销并保留数据；平台自身租户禁止注销
// @Tags         租户管理
// @Produce      json
// @Param        tenant_id  query     string  true  "租户码"
// @Success      200        {object}  response.Response  "注销成功"
// @Security     ApiKeyAuth
// @Router       /tenant/del [post]
func (w *WafTenantApi) DelApi(c *gin.Context) {
	var req request.WafTenantDelReq
	if err := c.ShouldBind(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	if req.TenantID == "" {
		response.FailWithMessage("参数不完整", c)
		return
	}
	if err := wafTenantService.DelTenantApi(c.Request.Context(), req); err != nil {
		response.FailWithMessage(err.Error(), c)
		return
	}
	response.OkWithMessage("注销成功", c)
}

// GetDetailApi 获取租户详情
// @Summary      获取租户详情
// @Tags         租户管理
// @Produce      json
// @Param        id  query     string  true  "记录ID"
// @Success      200  {object}  response.Response  "获取成功"
// @Security     ApiKeyAuth
// @Router       /tenant/detail [get]
func (w *WafTenantApi) GetDetailApi(c *gin.Context) {
	var req request.WafTenantDetailReq
	if err := c.ShouldBind(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	response.OkWithDetailed(wafTenantService.GetDetailByIdApi(c.Request.Context(), req.Id), "获取成功", c)
}

// GetListApi 获取租户列表
// @Summary      获取租户列表
// @Description  分页查询全部租户（平台视角），支持租户名模糊与状态筛选
// @Tags         租户管理
// @Accept       json
// @Produce      json
// @Param        data  body      request.WafTenantSearchReq  true  "分页查询参数"
// @Success      200   {object}  response.Response{data=response.PageResult}  "获取成功"
// @Security     ApiKeyAuth
// @Router       /tenant/list [post]
func (w *WafTenantApi) GetListApi(c *gin.Context) {
	var req request.WafTenantSearchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	list, total, _ := wafTenantService.GetListApi(c.Request.Context(), req)
	response.OkWithDetailed(response.PageResult{
		List:      list,
		Total:     total,
		PageIndex: req.PageIndex,
		PageSize:  req.PageSize,
	}, "获取成功", c)
}
