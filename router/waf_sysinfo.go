package router

import (
	"SamWaf/api"

	"github.com/gin-gonic/gin"
)

type WebSysInfoRouter struct {
}

func (receiver *WebSysInfoRouter) InitSysInfoRouter(group *gin.RouterGroup) {
	api := api.APIGroupAPP.WafSysInfoApi
	router := group.Group("")
	router.GET("/api/v1/sysinfo/version", api.SysVersionApi)
	router.GET("/api/v1/sysinfo/runtimeinfo", api.SysRuntimeInfoApi)
	router.GET("/api/v1/sysinfo/systemparams", api.SystemParamsApi)
}
