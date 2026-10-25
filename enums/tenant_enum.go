package enums

// 租户生命周期状态（tenants.status）
const (
	TenantStatusActive    = 1  // 正常
	TenantStatusSuspended = 0  // 停用（管理员手动暂停，禁登录禁请求）
	TenantStatusExpired   = 2  // 已过期（每日任务推进；宽限期内只读，超宽限期禁登录）
	TenantStatusCancelled = -1 // 已注销（保留数据，禁登录禁请求）
)

// 租户配额计量维度（tenants.max_* 字段）
const (
	TenantQuotaKindHost    = "host"    // 站点数上限（tenants.max_hosts，0=不限）
	TenantQuotaKindAccount = "account" // 账号数上限（tenants.max_accounts，0=不限）
)

// CACHE_TENANT_STATUS 请求期租户策略的缓存键前缀（键=租户码，值=RequestPolicy 快照）。
// 登录走实时校验；请求期用缓存，策略变化最迟 5 分钟生效。
const CACHE_TENANT_STATUS = "tenant_status:"

// CACHE_TENANT_PERM_CEILING 租户权限天花板的缓存键前缀（键=租户码，值=套餐权限集
// 的 map[string]bool 快照，nil 值经 Found 语义区分）。套餐/租户编辑时主动失效；
// RequirePermission 热路径读取。
const CACHE_TENANT_PERM_CEILING = "tenant_perm_ceiling:"
