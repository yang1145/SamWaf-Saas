package api

import (
	"SamWaf/model/common/response"
	"SamWaf/model/request"

	"github.com/gin-gonic/gin"
)

type WafPlanApi struct {
}

// AddApi 新增套餐
// @Summary      新增套餐
// @Description  创建套餐：权限码须在租户可分配集内（平台码整单拒绝），配额为该套餐新租户的默认值
// @Tags         套餐管理
// @Accept       json
// @Produce      json
// @Param        data  body      request.WafPlanAddReq  true  "套餐信息"
// @Success      200   {object}  response.Response  "添加成功"
// @Security     ApiKeyAuth
// @Router       /plan/add [post]
func (w *WafPlanApi) AddApi(c *gin.Context) {
	var req request.WafPlanAddReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	bean, err := wafPlanService.AddApi(c.Request.Context(), req)
	if err != nil {
		response.FailWithMessage(err.Error(), c)
		return
	}
	response.OkWithDetailed(bean, "添加成功", c)
}

// EditApi 编辑套餐
// @Summary      编辑套餐
// @Description  修改套餐名/权限集/配额/状态；权限集变化会同步刷新引用该套餐的全部租户的管理员权限组
// @Tags         套餐管理
// @Accept       json
// @Produce      json
// @Param        data  body      request.WafPlanEditReq  true  "套餐信息"
// @Success      200   {object}  response.Response  "编辑成功"
// @Security     ApiKeyAuth
// @Router       /plan/edit [post]
func (w *WafPlanApi) EditApi(c *gin.Context) {
	var req request.WafPlanEditReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	bean, err := wafPlanService.ModifyApi(c.Request.Context(), req)
	if err != nil {
		response.FailWithMessage(err.Error(), c)
		return
	}
	response.OkWithDetailed(bean, "编辑成功", c)
}

// DelApi 删除套餐
// @Summary      删除套餐
// @Description  仍有租户引用的套餐拒绝删除，需先为相关租户更换套餐
// @Tags         套餐管理
// @Produce      json
// @Param        id  query     string  true  "记录ID"
// @Success      200 {object}  response.Response  "删除成功"
// @Security     ApiKeyAuth
// @Router       /plan/del [post]
func (w *WafPlanApi) DelApi(c *gin.Context) {
	var req request.WafPlanDelReq
	if err := c.ShouldBind(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	if req.Id == "" {
		response.FailWithMessage("参数不完整", c)
		return
	}
	if err := wafPlanService.DelApi(c.Request.Context(), req); err != nil {
		response.FailWithMessage(err.Error(), c)
		return
	}
	response.OkWithMessage("删除成功", c)
}

// GetDetailApi 获取套餐详情
// @Summary      获取套餐详情
// @Tags         套餐管理
// @Produce      json
// @Param        id  query     string  true  "记录ID"
// @Success      200  {object}  response.Response  "获取成功"
// @Security     ApiKeyAuth
// @Router       /plan/detail [get]
func (w *WafPlanApi) GetDetailApi(c *gin.Context) {
	var req request.WafPlanDetailReq
	if err := c.ShouldBind(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	response.OkWithDetailed(wafPlanService.GetDetailByIdApi(c.Request.Context(), req.Id), "获取成功", c)
}

// GetListApi 获取套餐列表
// @Summary      获取套餐列表
// @Description  分页查询全部套餐（平台视角），支持套餐名模糊与状态筛选
// @Tags         套餐管理
// @Accept       json
// @Produce      json
// @Param        data  body      request.WafPlanSearchReq  true  "分页查询参数"
// @Success      200   {object}  response.Response{data=response.PageResult}  "获取成功"
// @Security     ApiKeyAuth
// @Router       /plan/list [post]
func (w *WafPlanApi) GetListApi(c *gin.Context) {
	var req request.WafPlanSearchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithMessage("解析失败", c)
		return
	}
	// 分页兜底：缺省 pageSize 若透传会变成 GORM 的 LIMIT 0，列表恒空
	req.PageInfo.Normalize(request.DefaultPageSize, request.MaxPageSize)
	list, total, err := wafPlanService.GetListApi(c.Request.Context(), req)
	if err != nil {
		response.FailWithMessage(err.Error(), c)
		return
	}
	response.OkWithDetailed(response.PageResult{
		List:      list,
		Total:     total,
		PageIndex: req.PageIndex,
		PageSize:  req.PageSize,
	}, "获取成功", c)
}
