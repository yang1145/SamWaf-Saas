package api

import (
	"SamWaf/model/common/response"
	"SamWaf/model/request"
	"SamWaf/utils"
	"SamWaf/waftask"
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type WafBatchTaskApi struct{}

// AddBatchTaskApi 添加自动任务
func (s *WafBatchTaskApi) AddBatchTaskApi(c *gin.Context) {
	var req request.BatchTaskAddReq
	err := c.ShouldBindJSON(&req)
	if err == nil {
		err = wafBatchTaskService.AddApi(c.Request.Context(), req)
		if err == nil {
			response.OkWithMessage("添加成功", c)
		} else {
			response.FailWithMessage("添加失败:"+err.Error(), c)
		}
		return
	} else {
		response.FailWithMessage("解析失败", c)
	}
}

// GetBatchTaskDetailApi 获取详情
func (s *WafBatchTaskApi) GetBatchTaskDetailApi(c *gin.Context) {
	var req request.BatchTaskDetailReq
	err := c.ShouldBind(&req)
	if err == nil {
		bean := wafBatchTaskService.GetDetailApi(c.Request.Context(), req)
		response.OkWithDetailed(bean, "获取成功", c)
	} else {
		response.FailWithMessage("解析失败", c)
	}
}

// GetBatchTaskListApi 获取任务列表
func (s *WafBatchTaskApi) GetBatchTaskListApi(c *gin.Context) {
	var req request.BatchTaskSearchReq
	err := c.ShouldBindJSON(&req)
	if err == nil {
		list, total, _ := wafBatchTaskService.GetListApi(c.Request.Context(), req)
		response.OkWithDetailed(response.PageResult{
			List:      list,
			Total:     total,
			PageIndex: req.PageIndex,
			PageSize:  req.PageSize,
		}, "获取成功", c)
	} else {
		response.FailWithMessage("解析失败", c)
	}
}

// DelBatchTaskApi 删除任务
func (s *WafBatchTaskApi) DelBatchTaskApi(c *gin.Context) {
	var req request.BatchTaskDeleteReq
	err := c.ShouldBind(&req)
	if err == nil {
		err = wafBatchTaskService.DelApi(c.Request.Context(), req)
		if err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithMessage("请检测参数", c)
		} else if err != nil {
			response.FailWithMessage("发生错误", c)
		} else {
			response.OkWithMessage("删除成功", c)
		}
	} else {
		response.FailWithMessage("解析失败", c)
	}
}

// ModifyBatchTaskApi 编辑任务
func (s *WafBatchTaskApi) ModifyBatchTaskApi(c *gin.Context) {
	var req request.BatchTaskEditReq
	err := c.ShouldBindJSON(&req)
	if err == nil {
		err = wafBatchTaskService.ModifyApi(c.Request.Context(), req)
		if err != nil {
			response.FailWithMessage("编辑发生错误:"+err.Error(), c)
		} else {

			response.OkWithMessage("编辑成功", c)
		}
	} else {
		response.FailWithMessage("解析失败", c)
	}
}

// ManualBatchTaskApi 手工执行任务。
// 查询随请求租户过滤：非本租户的任务在这里就是"不存在"，杜绝跨租户执行他人任务。
func (s *WafBatchTaskApi) ManualBatchTaskApi(c *gin.Context) {
	var req request.BatchTaskManualReq
	err := c.ShouldBind(&req)
	if err != nil {
		response.FailWithMessage("手工执行任务失败", c)
		return
	}
	bean := wafBatchTaskService.GetDetailByIdApi(c.Request.Context(), req.Id)
	if bean.Id == "" {
		response.FailWithMessage("手工执行任务失败:任务不存在", c)
		return
	}
	// 执行侧脱离请求生命周期写库（处理器按任务归属租户写行），
	// 用任务行自带的租户标识重新构造上下文，不复用可能随请求结束取消的 ctx。
	runErr := waftask.ExecuteBatchTask(waftask.BatchTaskTenantCtx(bean), bean)

	account, _ := c.Get("loginAccount")
	role, _ := c.Get("userRole")
	accountStr, _ := account.(string)
	roleStr, _ := role.(string)
	if roleStr != "" {
		accountStr = accountStr + "[" + roleStr + "]"
	}
	// 执行审计落请求者（而非任务归属）名下：审计要回答"是谁点的执行"
	waftask.AuditBatchTaskRun(c.Request.Context(), bean, accountStr, utils.GetManageClientIP(c), runErr)

	if runErr != nil {
		response.FailWithMessage("手工执行任务失败:"+runErr.Error(), c)
		return
	}
	response.OkWithMessage("手工执行任务成功", c)
}
