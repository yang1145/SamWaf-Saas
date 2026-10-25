package api

import (
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/common/response"
	"SamWaf/utils"
	"time"

	"github.com/gin-gonic/gin"
)

type WafSysInfoApi struct {
}

// SysVersionApi 获取系统版本信息
// @Summary      获取系统版本信息
// @Description  返回当前 SamWaf 的版本号、版本名称及发布状态
// @Tags         系统信息
// @Produce      json
// @Success      200  {object}  response.Response{data=model.VersionInfo}  "获取成功"
// @Security     ApiKeyAuth
// @Router       /sysinfo/version [get]
func (w *WafSysInfoApi) SysVersionApi(c *gin.Context) {
	response.OkWithDetailed(model.VersionInfo{
		Version:        global.GWAF_RELEASE_VERSION,
		VersionName:    global.GWAF_RELEASE_VERSION_NAME,
		VersionRelease: global.GWAF_RELEASE,
		WebVersion:     global.GWAF_WEB_VERSION,
	}, "获取成功", c)
}

// SysRuntimeInfoApi 获取系统运行环境信息
// @Summary      获取系统运行环境信息
// @Description  返回操作系统类型/发行版及版本、内核、容器(docker/k8s)、虚拟化、运行时长与软件版本，便于反馈问题时定位环境
// @Tags         系统信息
// @Produce      json
// @Success      200  {object}  response.Response{data=model.RuntimeSystemInfo}  "获取成功"
// @Security     ApiKeyAuth
// @Router       /sysinfo/runtimeinfo [get]
func (w *WafSysInfoApi) SysRuntimeInfoApi(c *gin.Context) {
	//探测过程全部容错：任何一项拿不到就留空，保证接口一定有返回
	detail := utils.GetOSDetail()

	info := model.RuntimeSystemInfo{
		Version:        global.GWAF_RELEASE_VERSION,
		VersionName:    global.GWAF_RELEASE_VERSION_NAME,
		VersionRelease: global.GWAF_RELEASE,
		WebVersion:     global.GWAF_WEB_VERSION,

		OS:        detail.GOOS,
		Arch:      detail.GOARCH,
		GoVersion: detail.GoVersion,

		OSName:          detail.OSName,
		Platform:        detail.Platform,
		PlatformVersion: detail.PlatformVersion,
		KernelVersion:   detail.KernelVersion,
		KernelArch:      detail.KernelArch,

		IsWindows:      detail.IsWindows,
		IsWin7Build:    global.GWAF_RUNTIME_WIN7_VERSION == "true",
		IsWin7Kernel:   detail.IsWin7Kernel,
		Container:      detail.Container,
		InKubernetes:   detail.InKubernetes,
		IsWSL:          detail.IsWSL,
		Virtualization: detail.Virtualization,
		RunAsService:   global.GWAF_RUNTIME_SERVER_TYPE,

		SystemUptimeSeconds:  int64(utils.GetSystemUptimeSeconds()),
		ProcessUptimeSeconds: int64(time.Since(global.GWAF_RUNTIME_PROCESS_START_TIME).Seconds()),
	}
	response.OkWithDetailed(info, "获取成功", c)
}

// SystemParamsApi 返回认证后才能获取的系统参数（可扩展）
// GET /api/v1/sysinfo/systemparams
func (w *WafSysInfoApi) SystemParamsApi(c *gin.Context) {
	// 当前数据库（sqlite|mysql|postgres），服务端数据库额外给出连接目标（不含密码）
	database := gin.H{"driver": global.GWAF_DB_DRIVER}
	switch global.GWAF_DB_DRIVER {
	case "mysql":
		database["host"] = global.GWAF_MYSQL_HOST
		database["port"] = global.GWAF_MYSQL_PORT
	case "postgres":
		database["host"] = global.GWAF_PG_HOST
		database["port"] = global.GWAF_PG_PORT
	}
	// 当前缓存（memory|redis），redis 额外给出连接目标（不含密码）
	cache := gin.H{"type": global.GCACHE_TYPE}
	if global.GCACHE_TYPE == "redis" {
		cache["host"] = global.GCACHE_REDIS_HOST
		cache["port"] = global.GCACHE_REDIS_PORT
	}
	response.OkWithDetailed(gin.H{
		"emergency_path": "/" + global.GWAF_SECURITY_EMERGENCY_PATH,
		"database":       database,
		"cache":          cache,
	}, "获取成功", c)
}
