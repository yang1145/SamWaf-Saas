package router

import (
	"SamWaf/api"
	"github.com/gin-gonic/gin"
)

type IPLocationRouter struct {
}

// InitIPLocationRouter IP 归属地查询类路由（保留在原 logGroup，URL 不变）。
//
// 仅保留只读端点：状态/配置读取、查询测试、升级信息与进度轮询；
// 会改动数据库文件或实例配置的端点拆到 InitIPLocationPlatformRouter 随平台域挂载。
func (receiver *IPLocationRouter) InitIPLocationRouter(group *gin.RouterGroup) {
	apiInstance := api.APIGroupAPP.WafIPLocationApi
	router := group.Group("/api/v1/iplocation")
	{
		router.GET("/status", apiInstance.GetIPDBStatusApi)
		router.GET("/config", apiInstance.GetIPDBConfigApi)
		router.POST("/test", apiInstance.TestIPLookupApi)
		router.GET("/upgrade/check", apiInstance.CheckIPDBUpgradeApi)
		router.GET("/upgrade/progress", apiInstance.GetIPDBUpgradeProgressApi)
	}
}

// InitIPLocationPlatformRouter IP 归属地数据库的实例级变更路由（URL 不变，挂平台域）。
//
// 这些端点直接改写实例上的 IP 地理库文件或全局配置：
//   - /upload：上传并替换 data 目录下的 .xdb/.mmdb/.ipdb 数据库文件；
//   - /reload：按当前配置重新加载 manager（实例全局运行态）；
//   - /config/save：写 sys_config 的 ip_v4/v6_source/format 并热切换全局数据源；
//   - /upgrade/apply：在线下载数据库文件并替换落盘；
//   - /upgrade/cancel：取消上述平台侧发起的下载任务（变更升级任务状态，与 apply 同域收口）。
//
// IP 库为全部租户共享的实例基础设施，租户侧只读，变更权收归平台侧账号。
func (receiver *IPLocationRouter) InitIPLocationPlatformRouter(group *gin.RouterGroup) {
	apiInstance := api.APIGroupAPP.WafIPLocationApi
	router := group.Group("/api/v1/iplocation")
	{
		router.POST("/config/save", apiInstance.SaveIPDBConfigApi)
		router.POST("/upload", apiInstance.UploadIPDBFileApi)
		router.POST("/reload", apiInstance.ReloadIPDBApi)
		router.POST("/upgrade/apply", apiInstance.ApplyIPDBUpgradeApi)
		router.POST("/upgrade/cancel", apiInstance.CancelIPDBUpgradeApi)
	}
}
