package router

import (
	"SamWaf/api"

	"github.com/gin-gonic/gin"
)

type PermissionGroupRouter struct {
}

// InitPermissionGroupRouter 权限组的写操作与详情/列表（permission:manage 域）。
func (receiver *PermissionGroupRouter) InitPermissionGroupRouter(group *gin.RouterGroup) {
	permissionGroupApi := api.APIGroupAPP.WafPermissionGroupApi
	router := group.Group("")
	router.POST("/api/v1/permission/group/list", permissionGroupApi.GetListApi)
	router.GET("/api/v1/permission/group/detail", permissionGroupApi.GetDetailApi)
	router.POST("/api/v1/permission/group/add", permissionGroupApi.AddApi)
	router.POST("/api/v1/permission/group/edit", permissionGroupApi.ModifyApi)
	router.POST("/api/v1/permission/group/del", permissionGroupApi.DelApi)
}

// InitPermissionCodesRouter 权限码目录（只读静态表）：权限组编辑器与套餐编辑器共用，
// 因此挂 RequireAnyPermission(permission:manage, platform:admin)，不属于单一域。
func (receiver *PermissionGroupRouter) InitPermissionCodesRouter(group *gin.RouterGroup) {
	group.GET("/api/v1/permission/group/codes", api.APIGroupAPP.WafPermissionGroupApi.GetPermissionCodesApi)
}

// InitPermissionGroupOptionsRouter 启用中的权限组下拉（只读，按 ctx 租户过滤）：
// 账号编辑绑组（account:manage）与权限组管理共用。
func (receiver *PermissionGroupRouter) InitPermissionGroupOptionsRouter(group *gin.RouterGroup) {
	group.GET("/api/v1/permission/group/options", api.APIGroupAPP.WafPermissionGroupApi.GetOptionsApi)
}
