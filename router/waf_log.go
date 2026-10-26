package router

import (
	"SamWaf/api"

	"github.com/gin-gonic/gin"
)

type LogRouter struct {
}

func (receiver *LogRouter) InitLogRouter(group *gin.RouterGroup) {
	logApi := api.APIGroupAPP.WafLogAPi
	wafLogRouter := group.Group("")

	wafLogRouter.POST("/api/v1/waflog/attack/list", logApi.GetListApi)
	wafLogRouter.GET("/api/v1/waflog/attack/export", logApi.ExportDBApi)
	wafLogRouter.GET("/api/v1/waflog/attack/download", logApi.DownloadApi)
	wafLogRouter.GET("/api/v1/waflog/attack/detail", logApi.GetDetailApi)
	wafLogRouter.GET("/api/v1/waflog/attack/allsharedb", logApi.GetAllShareDbApi)
	wafLogRouter.GET("/api/v1/waflog/attack/httpcopymask", logApi.GetHttpCopyMaskApi)
	wafLogRouter.POST("/api/v1/waflog/attack/attackiplist", logApi.GetAttackIPListApi)
	wafLogRouter.GET("/api/v1/waflog/attack/alliptag", logApi.GetAllIpTagApi)
	wafLogRouter.POST("/api/v1/waflog/attack/deletetagbyname", logApi.DeleteTagByNameApi)
	wafLogRouter.GET("/api/v1/waflog/attack/iptagdbstatus", logApi.GetIPTagDBStatusApi)
	wafLogRouter.POST("/api/v1/waflog/attack/watchlist/add", logApi.AddIPWatchlistApi)
	wafLogRouter.POST("/api/v1/waflog/attack/watchlist/del", logApi.DelIPWatchlistApi)
	wafLogRouter.POST("/api/v1/waflog/attack/watchlist/list", logApi.GetIPWatchlistApi)
}

// InitLogPlatformRouter 日志域的平台级路由（URL 与原注册保持不变，仅挂载组移动）。
//
// 分片删除直接物理删除共享日志库的分片数据库文件，属于实例级破坏性操作：
// 会波及全部租户的日志数据，且无租户维度的行过滤可言，租户侧不应可达，
// 故从 InitLogRouter 拆出、随平台域（platform:admin）挂载。
func (receiver *LogRouter) InitLogPlatformRouter(group *gin.RouterGroup) {
	logApi := api.APIGroupAPP.WafLogAPi
	wafLogRouter := group.Group("")
	wafLogRouter.POST("/api/v1/waflog/attack/shard/del", logApi.DelShardApi)
}
