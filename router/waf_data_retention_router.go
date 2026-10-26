package router

import (
	"SamWaf/api"
	"SamWaf/enums"
	"SamWaf/middleware"

	"github.com/gin-gonic/gin"
)

type WafDataRetentionRouter struct{}

func (receiver *WafDataRetentionRouter) InitWafDataRetentionRouter(group *gin.RouterGroup) {
	wafApi := api.APIGroupAPP.WafDataRetentionApi
	// 数据保留策略是实例级治理配置：清理任务按策略清全实例日志（平台语义，不分租户），
	// 租户可编辑策略等于能左右全实例的日志留存。整组叠加 platform:admin 门
	// （legacy=systemAdmin 保持存量账号可达；策略行本身落在平台租户桶）。
	platform := group.Group("")
	platform.Use(middleware.RequirePermission(enums.PERM_PLATFORM_ADMIN, enums.ROLE_SYSTEM_ADMIN))
	router := platform.Group("")
	router.POST("/api/v1/wafhost/dataretention/list", wafApi.GetListApi)
	router.GET("/api/v1/wafhost/dataretention/detail", wafApi.GetDetailApi)
	router.POST("/api/v1/wafhost/dataretention/edit", wafApi.ModifyApi)
}
