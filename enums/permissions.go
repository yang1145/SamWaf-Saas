package enums

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
	// PERM_SYSTEM_OPS 系统运维：系统配置、数据保留策略、通知渠道/订阅、批量任务
	PERM_SYSTEM_OPS = "system:ops"
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
	{Code: PERM_SYSTEM_OPS, Name: "系统配置与运维"},
	{Code: PERM_ACCOUNT_MANAGE, Name: "账号管理"},
	{Code: PERM_PLATFORM_ADMIN, Name: "平台级管理"},
	{Code: PERM_GROUP_MANAGE, Name: "权限组管理"},
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
