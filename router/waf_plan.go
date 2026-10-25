package router

import (
	"SamWaf/api"

	"github.com/gin-gonic/gin"
)

type PlanRouter struct {
}

// InitPlanRouter 套餐管理（租户差异化的配置源），挂在平台域 tokenPlatformGroup
func (receiver *PlanRouter) InitPlanRouter(group *gin.RouterGroup) {
	planApi := api.APIGroupAPP.WafPlanApi
	router := group.Group("")
	router.POST("/api/v1/plan/list", planApi.GetListApi)
	router.GET("/api/v1/plan/detail", planApi.GetDetailApi)
	router.POST("/api/v1/plan/add", planApi.AddApi)
	router.POST("/api/v1/plan/edit", planApi.EditApi)
	router.POST("/api/v1/plan/del", planApi.DelApi)
}
