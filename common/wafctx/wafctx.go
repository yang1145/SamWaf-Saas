// Package wafctx 提供请求级租户上下文的读写工具。
//
// 背景：SamWaf 从「单租户实例」向「共享库多租户」演进（阶段0）。数据库层所有租户
// 过滤（wafdb.before_query 回调）由「读全局常量 global.GWAF_TENANT_ID/GWAF_USER_CODE」
// 升级为「优先读请求上下文，未声明时回退全局常量」。单租户模式下所有查询都不带
// 上下文，行为与改造前完全一致；新代码可逐请求声明租户，实现行级隔离。
//
// 使用约定：
//   - WithTenant 只应在鉴权入口（Auth 中间件、显式知道自己代表哪个租户的任务）调用，
//     不要在业务 service 里随手改写租户；
//   - 后台任务（定时/批量/通知/归档/ACME 续签）不属于任何 HTTP 请求，不携带请求 ctx，
//     自然回退到平台全局租户；平台级任务将来应显式声明平台租户，而不是依赖回退；
//   - 从请求 ctx 派生 goroutine 再查库时注意取消语义：脱离请求生命周期的工作
//     先用 context.WithoutCancel（或独立 context）再派生，避免请求结束误杀 DB 操作。
package wafctx

import "context"

// tenantKey 私有键类型，防止包外伪造/冲突。
type tenantKey struct{}

// Tenant 是一对租户标识：TenantID 是隔离主键，UserCode 是实例识别号。
// 现阶段两者一起作为行过滤条件（tenant_id + user_code），与全局变量语义保持一致。
type Tenant struct {
	TenantID string
	UserCode string
}

// WithTenant 把租户标识写入 ctx，返回派生上下文。
func WithTenant(ctx context.Context, tenantID, userCode string) context.Context {
	return context.WithValue(ctx, tenantKey{}, Tenant{TenantID: tenantID, UserCode: userCode})
}

// TenantFrom 从 ctx 取出租户标识。ok=false 表示 ctx 未声明租户（或租户为空），
// 调用方应回退到全局常量。
func TenantFrom(ctx context.Context) (tenantID, userCode string, ok bool) {
	if ctx == nil {
		return "", "", false
	}
	t, exists := ctx.Value(tenantKey{}).(Tenant)
	if !exists || t.TenantID == "" {
		return "", "", false
	}
	return t.TenantID, t.UserCode, true
}

// platformScopeKey 私有键：平台级数据面/维护任务标记。
type platformScopeKey struct{}

// PlatformScope 把 ctx 标记为平台级作用域：数据库租户回调对其跳过过滤与盖章，
// 读取/写入**全部租户**的数据。仅限两类场景使用：
//   - 数据面引擎：WAF 引擎加载全部站点的配置、证书、规则（它要为所有租户的站点转发流量）；
//   - 平台维护任务：数据密钥迁移、库表修复、批量升级等启动期/定时任务。
//
// 业务请求处理链路禁止使用；写路径（UPDATE/DELETE）本就仅在携带请求租户时过滤，
// 平台任务直接用 Background() 即可，无需此标记。
func PlatformScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, platformScopeKey{}, true)
}

// PlatformScoped 报告 ctx 是否为平台级作用域（平台维护/数据面任务）。
func PlatformScoped(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, ok := ctx.Value(platformScopeKey{}).(bool)
	return ok && v
}
