package utils

import (
	"SamWaf/global"
	"net/url"
	"runtime"
	"strings"
	"sync"
)

// ClientQuery IP库/OWASP规则集等数据文件清单请求统一携带的客户端标识。
// 原 wafupdate（程序在线更新）包已移除，本函数随数据文件升级链路迁入 utils。
func ClientQuery() string {
	return buildClientQuery()
}

// buildClientQuery 每次现拼：实例码要等 wafconfig.LoadAndInitConfig() 之后才有值，
// 整串缓存会把启动早期的空 u= 冻住。开销大的只有容器探测，那部分单独缓存。
func buildClientQuery() string {
	parts := []string{
		"v=" + url.QueryEscape(global.GWAF_RELEASE_VERSION),
		"u=" + url.QueryEscape(global.GWAF_USER_CODE),
	}
	if global.GCONFIG_UPDATE_ENV_REPORT {
		parts = append(parts,
			"os="+url.QueryEscape(runtime.GOOS),
			"arch="+url.QueryEscape(runtime.GOARCH),
			"rt="+url.QueryEscape(runtimeTag()),
		)
	}
	return strings.Join(parts, "&")
}

// runtimeTag 运行形态：容器里给容器类型(docker/podman/containerd/lxc/…)，否则 host。
// 容器探测目前只在 Linux 有效，Windows/macOS 一律记为 host。
// 探测要读多个文件，而进程存活期内结果不会变，因此只算一次。
func runtimeTag() string {
	runtimeTagOnce.Do(func() {
		if container := DetectContainerRuntime(); container != "" {
			runtimeTagCache = container
			return
		}
		runtimeTagCache = "host"
	})
	return runtimeTagCache
}

var (
	runtimeTagOnce  sync.Once
	runtimeTagCache string
)
