package router

import (
	"SamWaf/api"

	"github.com/gin-gonic/gin"
)

type TenantRouter struct {
}

// InitTenantRouter 租户管理（生命周期：开通/续期/编辑/注销），挂在平台域 tokenPlatformGroup
func (receiver *TenantRouter) InitTenantRouter(group *gin.RouterGroup) {
	tenantApi := api.APIGroupAPP.WafTenantApi
	router := group.Group("")
	router.POST("/api/v1/tenant/list", tenantApi.GetListApi)
	router.GET("/api/v1/tenant/detail", tenantApi.GetDetailApi)
	router.POST("/api/v1/tenant/add", tenantApi.AddApi)
	router.POST("/api/v1/tenant/edit", tenantApi.EditApi)
	router.POST("/api/v1/tenant/del", tenantApi.DelApi)
	router.POST("/api/v1/tenant/renew", tenantApi.RenewApi)
}
