package router

import (
	"SamWaf/api"
	"github.com/gin-gonic/gin"
)

type LogFileWriteRouter struct {
}

// InitLogFileWriteRouter 通知日志文件写入的只读路由（保留在原 logGroup，URL 不变）。
//
// 预览/当前文件信息/备份列表/模板变量均为读取端点，保持原挂载；
// 清空日志文件的破坏性端点拆到 InitLogFileWritePlatformRouter 随平台域挂载。
func (receiver *LogFileWriteRouter) InitLogFileWriteRouter(group *gin.RouterGroup) {
	api := api.APIGroupAPP.WafLogFileWriteApi
	router := group.Group("")
	router.GET("/api/v1/logfilewrite/preview", api.GetPreviewApi)
	router.GET("/api/v1/logfilewrite/currentfile", api.GetCurrentFileInfoApi)
	router.GET("/api/v1/logfilewrite/backupfiles", api.GetBackupFilesApi)
	router.GET("/api/v1/logfilewrite/variables", api.GetTemplateVariablesApi)
}

// InitLogFileWritePlatformRouter 通知日志文件写入的破坏性路由（URL 不变，挂平台域）。
//
// /clear 会清空实例上共享的通知日志文件（GNOTIFY_LOG_FILE_WRITER 全局单实例，
// 内容混合全部租户的通知记录），属于实例级破坏性操作，租户侧不应可达。
func (receiver *LogFileWriteRouter) InitLogFileWritePlatformRouter(group *gin.RouterGroup) {
	api := api.APIGroupAPP.WafLogFileWriteApi
	router := group.Group("")
	router.POST("/api/v1/logfilewrite/clear", api.ClearLogFileApi)
}
