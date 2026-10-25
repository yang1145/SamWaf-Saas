package enums

import "strings"

// 管理端权限码目录（权限组的原子授权单位，域级粒度）。
//
// 设计约定：
//   - 权限码按「业务域」划分，一个码对应 wafmangeweb/localserver.go 里的一组路由；
//     新增路由时归入既有域，确需新域时先加码再接线；
//   - 权限组（permission_groups.permissions）存储这些码的逗号分隔列表；
//   - 未绑定权限组的账号走旧四角色逻辑（见 RequirePermission 的 legacy 参数），
//     绑定了权限组的账号完全按权限码判定。
const (
	// PERM_DASHBOARD 仪表盘与总览：统计、分析、系统信息、监控、授权
	PERM_DASHBOARD = "dashboard:view"
	// PERM_HOST_MANAGE 站点与转发：主机、分组、路径规则、负载均衡、SSL 全套、
	// CA、CDN、隧道、缓存规则、拦截页、HTTP 认证、一键修改等
	PERM_HOST_MANAGE = "host:manage"
	// PERM_LOG_VIEW 访问日志：查询、导出、日志文件、IP 归属地/探查
	PERM_LOG_VIEW = "log:view"
	// PERM_SECURITY 防护策略：自定义规则、黑白名单、CC、敏感词、脱敏、IP 组、
	// OWASP、防爆破、系统防火墙联动、威胁情报、隐私信息(DLP)、统一访问认证
	PERM_SECURITY = "security:policy"
	// PERM_AUDIT 审计：系统日志、账号操作日志、登录历史、访问认证审计
	PERM_AUDIT = "audit:view"
	// PERM_SYSTEM_OPS 系统运维：数据保留策略、通知渠道/订阅、批量任务（租户隔离资源）
	PERM_SYSTEM_OPS = "system:ops"
	// PERM_SYSTEM_CONFIG 实例级系统配置：系统配置、GPT 模型配置、后台任务管理。
	// 这些资源是实例全局的（SystemConfig 平台豁免、GPT 无租户列），租户侧不应持有，
	// 从 system:ops 拆出（legacy=systemAdmin，存量账号行为不变）
	PERM_SYSTEM_CONFIG = "system:config"
	// PERM_ACCOUNT_MANAGE 账号与OTP：账号管理（租户内）、OTP 双因素
	PERM_ACCOUNT_MANAGE = "account:manage"
	// PERM_PLATFORM_ADMIN 平台级管理：开放平台 Key、SQL 查询、应用管理、AI 模型、
	// 运行诊断——仅平台侧角色（租户管理员不应持有）
	PERM_PLATFORM_ADMIN = "platform:admin"
	// PERM_GROUP_MANAGE 权限组管理本身：创建/修改/删除权限组
	PERM_GROUP_MANAGE = "permission:manage"
)

// PermissionCodeInfo 权限码的展示信息（前端渲染权限组编辑器用）。
type PermissionCodeInfo struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// AllPermissionCodes 全部权限码及中文名，顺序即前端展示顺序。
var AllPermissionCodes = []PermissionCodeInfo{
	{Code: PERM_DASHBOARD, Name: "仪表盘与总览"},
	{Code: PERM_HOST_MANAGE, Name: "站点与转发配置"},
	{Code: PERM_LOG_VIEW, Name: "访问日志查看与导出"},
	{Code: PERM_SECURITY, Name: "防护策略管理"},
	{Code: PERM_AUDIT, Name: "审计日志查看"},
	{Code: PERM_SYSTEM_OPS, Name: "系统运维（通知/保留/批量任务）"},
	{Code: PERM_SYSTEM_CONFIG, Name: "实例级系统配置（平台）"},
	{Code: PERM_ACCOUNT_MANAGE, Name: "账号管理"},
	{Code: PERM_PLATFORM_ADMIN, Name: "平台级管理"},
	{Code: PERM_GROUP_MANAGE, Name: "权限组管理"},
}

// tenantForbiddenCodes 租户侧禁止持有的权限码：平台域/实例级配置/权限组管理本身。
// 套餐（plans.permissions）与租户权限组的写入都以 AllPermissionCodes 减本集合为上限，
// 防止租户经任何通道（含平台误操作）拿到实例级权限。
var tenantForbiddenCodes = map[string]bool{
	PERM_PLATFORM_ADMIN: true,
	PERM_GROUP_MANAGE:   true,
	PERM_SYSTEM_CONFIG:  true,
}

// TenantAssignableCodes 租户可分配的权限码集合（套餐授权集的上限）。
// 每次调用现算：目录扩充时自动纳入（若新增码默认允许租户持有），
// 明确要禁租户的码须同时加进 tenantForbiddenCodes。
func TenantAssignableCodes() []string {
	codes := make([]string, 0, len(AllPermissionCodes))
	for _, info := range AllPermissionCodes {
		if !tenantForbiddenCodes[info.Code] {
			codes = append(codes, info.Code)
		}
	}
	return codes
}

// IsTenantAssignableCode 校验权限码是否合法且租户可持有（套餐写入侧 fail-closed）。
func IsTenantAssignableCode(code string) bool {
	return !tenantForbiddenCodes[code] && IsValidPermissionCode(code)
}

// IsValidPermissionCode 校验权限码是否在目录内（权限组写入侧 fail-closed）。
func IsValidPermissionCode(code string) bool {
	for _, info := range AllPermissionCodes {
		if info.Code == code {
			return true
		}
	}
	return false
}

// ParsePermissionSet 把逗号分隔的权限码串解析成集合（去空格、去空项）。
// 供权限组与 OpenAPI Key 的授权范围解析共用；返回空集合表示"未声明=全部权限"。
func ParsePermissionSet(perms string) map[string]bool {
	set := map[string]bool{}
	for _, p := range strings.Split(perms, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			set[p] = true
		}
	}
	return set
}
