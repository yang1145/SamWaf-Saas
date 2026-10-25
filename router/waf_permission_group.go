package router

import (
	"SamWaf/api"

	"github.com/gin-gonic/gin"
)

type PermissionGroupRouter struct {
}

func (receiver *PermissionGroupRouter) InitPermissionGroupRouter(group *gin.RouterGroup) {
	permissionGroupApi := api.APIGroupAPP.WafPermissionGroupApi
	router := group.Group("")
	router.POST("/api/v1/permission/group/list", permissionGroupApi.GetListApi)
	router.GET("/api/v1/permission/group/detail", permissionGroupApi.GetDetailApi)
	router.GET("/api/v1/permission/group/options", permissionGroupApi.GetOptionsApi)
	router.GET("/api/v1/permission/group/codes", permissionGroupApi.GetPermissionCodesApi)
	router.POST("/api/v1/permission/group/add", permissionGroupApi.AddApi)
	router.POST("/api/v1/permission/group/edit", permissionGroupApi.ModifyApi)
	router.POST("/api/v1/permission/group/del", permissionGroupApi.DelApi)
}
