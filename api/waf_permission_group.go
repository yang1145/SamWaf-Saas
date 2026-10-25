package api

import (
	"SamWaf/enums"
	"SamWaf/model/common/response"
	"SamWaf/model/request"

	"github.com/gin-gonic/gin"
)

type WafPermissionGroupApi struct {
}

// AddApi 新增权限组
// @Summary      新增权限组
// @Description  新增一个权限组（多租户自定义角色），权限码逐个校验，非法码整单拒绝
// @Tags         权限组管理
// @Accept       json
// @Produce      json
// @Param        data  body      request.WafPermissionGroupAddReq  true  "权限组信息"
// @Success      200   {object}  response.Response  "添加成功"
// @Security     ApiKeyAuth
// @Router       /permission/group/add [post]
func (w *WafPermissionGroupApi) AddApi(c *gin.Context) {
	var req request.WafPermissionGroupAddReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	bean, err := wafPermissionGroupService.AddApi(c.Request.Context(), req)
	if err != nil {
		response.FailWithMessage(err.Error(), c)
		return
	}
	response.OkWithDetailed(bean, "添加成功", c)
}

// ModifyApi 编辑权限组
// @Summary      编辑权限组
// @Description  系统预置组不允许修改组名，其余字段可改；改完失效判定缓存立即生效
// @Tags         权限组管理
// @Accept       json
// @Produce      json
// @Param        data  body      request.WafPermissionGroupEditReq  true  "权限组信息"
// @Success      200   {object}  response.Response  "编辑成功"
// @Security     ApiKeyAuth
// @Router       /permission/group/edit [post]
func (w *WafPermissionGroupApi) ModifyApi(c *gin.Context) {
	var req request.WafPermissionGroupEditReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	bean, err := wafPermissionGroupService.ModifyApi(c.Request.Context(), req)
	if err != nil {
		response.FailWithMessage(err.Error(), c)
		return
	}
	response.OkWithDetailed(bean, "编辑成功", c)
}

// DelApi 删除权限组
// @Summary      删除权限组
// @Description  系统预置组拒删；仍有账号绑定时拒删并提示先解绑
// @Tags         权限组管理
// @Produce      json
// @Param        id  query     string  true  "记录ID"
// @Success      200  {object}  response.Response  "删除成功"
// @Security     ApiKeyAuth
// @Router       /permission/group/del [post]
func (w *WafPermissionGroupApi) DelApi(c *gin.Context) {
	var req request.WafPermissionGroupDelReq
	if err := c.ShouldBind(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	if req.Id == "" {
		response.FailWithMessage("参数不完整", c)
		return
	}
	if err := wafPermissionGroupService.DelApi(c.Request.Context(), req); err != nil {
		response.FailWithMessage(err.Error(), c)
		return
	}
	response.OkWithMessage("删除成功", c)
}

// GetDetailApi 获取权限组详情
// @Summary      获取权限组详情
// @Tags         权限组管理
// @Produce      json
// @Param        id  query     string  true  "记录ID"
// @Success      200  {object}  response.Response  "获取成功"
// @Security     ApiKeyAuth
// @Router       /permission/group/detail [get]
func (w *WafPermissionGroupApi) GetDetailApi(c *gin.Context) {
	var req request.WafPermissionGroupDetailReq
	if err := c.ShouldBind(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	response.OkWithDetailed(wafPermissionGroupService.GetDetailByIdApi(c.Request.Context(), req.Id), "获取成功", c)
}

// GetListApi 获取权限组列表
// @Summary      获取权限组列表
// @Description  分页查询权限组，支持组名模糊筛选
// @Tags         权限组管理
// @Accept       json
// @Produce      json
// @Param        data  body      request.WafPermissionGroupSearchReq  true  "分页查询参数"
// @Success      200   {object}  response.Response{data=response.PageResult}  "获取成功"
// @Security     ApiKeyAuth
// @Router       /permission/group/list [post]
func (w *WafPermissionGroupApi) GetListApi(c *gin.Context) {
	var req request.WafPermissionGroupSearchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	list, total, _ := wafPermissionGroupService.GetListApi(c.Request.Context(), req)
	response.OkWithDetailed(response.PageResult{
		List:      list,
		Total:     total,
		PageIndex: req.PageIndex,
		PageSize:  req.PageSize,
	}, "获取成功", c)
}

// GetOptionsApi 获取全部启用中的权限组
// @Summary      获取权限组下拉选项
// @Description  不分页，仅启用中的组，供账号编辑的权限组绑定下拉
// @Tags         权限组管理
// @Produce      json
// @Success      200  {object}  response.Response  "获取成功"
// @Security     ApiKeyAuth
// @Router       /permission/group/options [get]
func (w *WafPermissionGroupApi) GetOptionsApi(c *gin.Context) {
	list, err := wafPermissionGroupService.GetOptionsApi(c.Request.Context())
	if err != nil {
		response.FailWithMessage(err.Error(), c)
		return
	}
	response.OkWithDetailed(list, "获取成功", c)
}

// GetPermissionCodesApi 获取全部权限码
// @Summary      获取权限码目录
// @Description  返回全部权限码及中文名，前端权限组编辑器按此渲染勾选列表
// @Tags         权限组管理
// @Produce      json
// @Success      200  {object}  response.Response  "获取成功"
// @Security     ApiKeyAuth
// @Router       /permission/group/codes [get]
func (w *WafPermissionGroupApi) GetPermissionCodesApi(c *gin.Context) {
	response.OkWithDetailed(enums.AllPermissionCodes, "获取成功", c)
}
