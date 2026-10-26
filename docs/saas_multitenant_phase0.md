# 共享库多租户（方案B）—— 阶段0 与 阶段2试点 设计说明

> 状态：阶段0 机制已落地；阶段2 试点（主机管理 + IP/URL 白名单两条链路的请求级
> 上下文传播）已落地并通过端到端隔离测试。本文记录设计决策、当前边界与后续任务。
> 租户差异化轮次（套餐体系 + 权限降权 + 账号域收敛）已落地，见「套餐与租户权限差异化」章。

## 背景与目标

SamWaf 由「单租户单实例」演进为「共享库多租户」（方案B，已评审选定）。
阶段0的目标是**验证核心机制**：把租户过滤从"进程级全局常量"升级为"请求级上下文"，
同时保证单租户模式下行为与改造前完全一致（零回归）。

## 核心机制

### 取值优先级

租户过滤条件 `(tenant_id, user_code)` 的取值优先级：

```
请求上下文（wafctx，随 db.WithContext 传入）  >  全局常量（global.GWAF_TENANT_ID / GWAF_USER_CODE）
```

关键事实：所有表通过 `model/baseorm.BaseOrm` 已带 `tenant_id`/`user_code` 列；
三种数据库（SQLite/MySQL/PostgreSQL）初始化时统一注册 GORM 全局查询回调
`before_query`（`wafdb/localdb.go`），自动为每条查询追加租户过滤。
**因此查询侧隔离对存量代码免费生效，无需改动 919 处直接 DB 调用点。**

### 改动清单（阶段0）

| 文件 | 内容 |
|---|---|
| `common/wafctx/wafctx.go` | 新包：`WithTenant` / `TenantFrom`，私有 context key |
| `wafdb/localdb.go` | `before_query` 回调：优先读 `db.Statement.Context` 中的租户，未声明时回退全局常量；上下文只带租户不带识别号时，识别号单独回退全局 |
| `middleware/auth_api_check.go` | Auth 中间件在令牌校验通过后，把 `TokenInfo` 里的 `Tenant_ID`/`USER_CODE` 写入请求上下文（`c.Request.WithContext`），并 `c.Set("tenantId")` 供下游取用 |

登录链路无需改动：`TokenInfo` 内嵌 BaseOrm，登录时已写入 `Tenant_ID`/`USER_CODE`
（`service/waf_service/waf_token_info.go`），并随令牌整体序列化进缓存，Auth 可原样读回。

### 兼容性保证

1. **存量查询**：所有不带 ctx 的 DB 调用走全局回退，与改造前逐字节一致（测试：
   `TestTenantCtxScopeQuery/无上下文回退全局常量(向后兼容)`）。
2. **存量令牌缓存**：升级部署后旧缓存令牌 JSON 里没有租户字段（`Tenant_ID` 为空），
   Auth 不注入上下文，同样走全局回退。
3. **识别号缺省**：上下文声明了租户但 `USER_CODE` 为空时，识别号回退全局值，
   保证行键 `(tenant_id, user_code)` 完整（测试覆盖）。

### 测试

- `common/wafctx/wafctx_test.go`：上下文读写、nil/空值/覆盖语义。
- `wafdb/tenant_ctx_scope_test.go`：SQLite 上验证「上下文优先于全局」「租户互不可见」
  「识别号回退」「无 ctx 回退全局」四组语义；复用 `tenant_scope_test.go` 的表与种子数据。
- 已验证：`go build ./...`（CGO_ENABLED=1）通过；
  `wafdb`、`middleware`、`common/wafctx` 全部测试通过。
  注：`api/`、`service/waf_service/` 存在与本改动无关的既有环境性失败（在未改动代码上同样失败）。

## 权限组（多租户自定义角色）

在旧四角色（superAdmin/systemAdmin/securityAdmin/auditAdmin 三权分立）之上叠加
租户级自定义角色，解决"租户管理员需要给运营人员开受限账号"的问题。

### 模型与判定

- **权限码**（`enums/permissions.go`，域级粒度，10 个）：`dashboard:view`、
  `host:manage`、`log:view`、`security:policy`、`audit:view`、`system:ops`、
  `system:config`、`account:manage`、`platform:admin`、`permission:manage`——
  一个码对应 localserver.go 里的一组路由。
- **PermissionGroup**（`permission_groups` 表，BaseOrm 租户隔离）：组名（租户内唯一）、
  权限码列表（逗号分隔）、状态、BuiltIn 预置标记。
- **Account.GroupId**：空 = 走旧四角色判定（存量账号行为不变）；绑定后**完全按
  权限码判定**（替代而非叠加），superAdmin 恒通过。
- **判定顺序**（`middleware.RequirePermission(code, legacyRoles...)`）：
  OpenAPI Key 放行 → superAdmin 恒通过 → 有组按组码（缓存 5 分钟，改组即失效，
  查询失败 fail-closed 403）→ 无组走 legacyRoles。
- **登录链**：Account.GroupId 随 TokenInfo 落缓存，Auth 写入 `userGroupId`；
  **账号改绑组会强制下线重登**（令牌里的组是登录时快照）。

### 路由域 → 权限码映射（localserver.go）

| 域 | 权限码 | legacy（未绑组账号） |
|---|---|---|
| 仪表盘与总览（stat/analysis/sysinfo/monitor/license/common） | dashboard:view | 全部四角色 |
| 站点与转发（hosts/group/pathrule/lb/ssl 全套/ca/cdn/tunnel/cacherule/blockingpage/httpauth/onekeymod/vpconfig/hostconn/file） | host:manage | 全部四角色 |
| 访问日志（log/logfilewrite/iplocation/iplookup） | log:view | 全部四角色 |
| 系统运维（batchtask/notify*/dataretention） | system:ops | 全部四角色 |
| 实例级配置（gpt/task） | system:config | 全部四角色 |
| 系统配置（systemconfig） | system:config | SYSTEM_ADMIN |
| 防护策略（原安全域 + 防篡改/隐私DLP/防爆破/防火墙联动/威胁情报） | security:policy | SECURITY_ADMIN |
| 审计（syslog/accountlog/login_history/access_audit） | audit:view | AUDIT_ADMIN |
| 账号与开放平台（TokenOnly 组） | account:manage | SYSTEM_ADMIN |
| 权限组管理（/api/v1/permission/group/*） | permission:manage | SYSTEM_ADMIN |

基础组（登出/WebSocket/UI偏好/升级须知/引擎角标/自助改密）仅要求登录态，不设码。
旧共享域拆分时 legacy=全角色，**存量账号行为零变化**；防篡改等 5 个路由并入安全域
对旧账号也保持可达（securityAdmin 可达，其他角色经共享域遗留可达性不变——
legacy 参数含全部角色）。

### 预置组

启动时幂等播种（`InitDefaultPermissionGroups`，按组名查重）：只读巡检
（dashboard+log+audit）、安全运维（dashboard+host+security+log）、运维管理
（dashboard+host+system_ops+log）。BuiltIn=1 不可删、不可改名。
租户开通流程落地后应为新租户复制同一套（租户隔离使预置组按租户各自一份）。

## 租户生命周期（已落地 MVP）

`tenants` 表（`model.Tenant`，平台豁免清单内——注册表本身跨租户可读）：
租户码、名称、状态机（Active/Suspended/Expired/Cancelled）、套餐码（预留）、
配额（max_hosts/max_accounts，0=不限）、到期时间（空=永久）、宽限天数。

### 生命周期流转

- **开通**（`POST /api/v1/tenant/add`，平台域 `platform:admin`）：PlatformScope
  身份依次落库——租户行（Tenant_ID=新租户码）→ 复制三个 BuiltIn 预置组到新租户 →
  建"租户管理员"组（除 platform:admin/permission:manage 外全部权限码）→
  建租户管理员账号（Role=tenantAdmin，首登强制改密）。
  跨租户写入的身份问题由 PlatformScope（跳过盖章）+ 手工 BaseOrm 三要素解决。
- **登录校验**（实时查库）：Suspended/Cancelled/过期超宽限 → 拒绝登录；
  宽限期内放行（请求期只读）；租户行不存在 → 视为平台自身（存量实例向后兼容）。
- **请求期执行**（Auth 中间件，策略缓存 5 分钟，改状态/续期主动失效）：
  Suspended/Cancelled/过期超宽限 → 全部请求 403；宽限期内 → 非 GET/HEAD 一律 403。
- **到期巡检**（每日任务 `task_tenant_expire`，PlatformScope）：Active+已过期 →
  落库 Expired；剩余 7/3/1 天经消息队列发提醒。
- **续期**（`/api/v1/tenant/renew`）：顺延 ExpireTime（或绝对时间）+ 恢复 Active +
  立即失效策略缓存。
- **注销**：软删除（Status=Cancelled，数据保留）；平台自身租户不可删。

### 配额

建站/建账号入口（host/account AddApi）经 `CheckTenantQuota(ctx, kind)` 校验
max_hosts/max_accounts。注意：Account 在平台豁免清单内，配额统计对 hosts/accounts
都显式携带租户条件（不能依赖回调）。

### 已知实现细节（审计关注点）

- `model.Tenant` 外层 `TenantID` 与内嵌 `BaseOrm.Tenant_ID` 同映射 `tenant_id` 列
  （字段遮蔽）：读回时值在外层 `TenantID` 字段，业务判断归属一律取外层字段。
- 策略缓存 TTL 5 分钟意味着停用/超期最迟 5 分钟生效（登录期是实时校验，
  缓存后端故障 fail-open 由登录期兜底）。
- 计费聚合（按租户的用量汇总/账单输出）未实现：套餐码与 stats 库按租户的列
  已预留，`attachTrafficMeter` 为流量计量挂钩点。
- 引擎数据面对到期租户的站点**继续转发**（商业默认）；如需"到期即停代理"，
  在引擎配置装载处按租户状态过滤即可（hook 点：GetAllHostApi 的 PlatformScope 读）。

## 套餐与租户权限差异化（已落地）

解决"租户权限过高且差异化不明显"：此前每个租户开通即得 7/9 权限码、
`plan_code` 不驱动任何行为、账号域还存在跨租户越权面。本轮引入三条主线：
**套餐（差异化引擎）+ 权限码再划分（降权）+ 套餐天花板（集中强制点）**。

### 套餐（plans 表，平台注册表）

- `model.Plan`（迁移 202610260002，平台豁免清单成员，`plan_code` 实例级全局唯一）：
  权限集（逗号分隔，须 ⊆ 租户可分配集）、max_hosts/max_accounts/grace_days 默认值、状态。
- 平台域 API `/api/v1/plan/{list,detail,add,edit,del}`（tokenPlatformGroup，platform:admin）；
  被租户引用的套餐禁删；权限集写入 fail-closed（平台码/非法码整单拒绝）。
- 启动幂等播种 4 个预置套餐（`InitDefaultPlans`，按套餐码查重）：体验版 free
  （dashboard/host/log，3 站 2 号）、基础版 basic（+security/audit，10 站 5 号）、
  专业版 pro（+system:ops/account:manage，50 站 20 号）、旗舰版 enterprise
  （租户可分配全集，不限）。播种只补缺，平台后续编辑不被覆盖。
- **开通集成**（AddTenantApi）：带 plan_code 时管理员组授权集=套餐权限集，
  配额/宽限期未传（nil）取套餐默认，显式传值（含 0=不限）优先——为此
  `WafTenantAddReq` 配额字段改为指针。套餐不存在/停用则整单拒绝。
- **换套餐**（EditTenantApi）：plan_code 变化时重写「租户管理员」BuiltIn 组授权集
  （清除套餐回落默认全集）+ 失效组缓存与天花板缓存；配额未显式传值时重置为新套餐默认。
- **套餐编辑传播**（propagatePlanPermissions）：套餐权限集变化同步到引用该套餐的
  全部租户的管理员组并失效缓存。配额/宽限期变化**不**回写存量租户（快照语义，
  经租户编辑单独调整）。
- 同步原语：`syncTenantAdminGroupPermissions(db, tenantID, perms)`——组 ID 不变，
  令牌无需重签（RBAC 按组 ID 现查缓存/库）。

### 权限码再划分与租户可分配集

- 新增 `system:config`（实例级系统配置/GPT/任务管理，从 system:ops 拆出）：
  SystemConfig 平台豁免=全局生效、GPT 无租户列，租户不应触达。legacy 可达性逐路由
  保持（系统配置仅 systemAdmin，GPT/任务三角色），存量账号行为不变。
- `enums.TenantAssignableCodes()` / `IsTenantAssignableCode`：全目录减
  `platform:admin`、`permission:manage`、`system:config`——套餐与一切租户侧
  权限集写入以此为上限。
- 未绑套餐的存量租户：管理员组维持原 7 码不动、无天花板，行为与旧版一致。

### 账号域租户收敛（修越权 + 自助管理）

Account 在平台豁免清单里，租户回调不给它追加过滤，账号读写必须**手工限定归属**
（`accountTenantScope(ctx)`：请求租户非平台时返回租户码，否则空=平台视角不限定）：

- 列表/详情/编辑/删除全部带 `tenant_id = ?`，跨租户目标按"账号不存在"处理
  （不泄露存在性）；`ResetPwdApi` 补 ctx 同样收敛。
- 租户管理员自助建号/删号（原 superAdmin-only 收窄为场景化）：
  建号角色**强制 tenantAdmin**（防提权）、归属按请求 ctx 落列、配额生效、
  只能绑本租户可见的权限组；删号限本租户普通账号、不可删自己、租户内至少保留一号；
  删号后强制清理该账号全部会话。平台 superAdmin 行为不变。
- `Account.LoginPassword` 加 `json:"-"`：口令哈希不再随列表/详情接口外泄。
- OTP 重置维持仅超管（Otp 按用户名全局删除，租户侧开放前需先租户化 Otp 表）。

### 套餐天花板（防御纵深）

- `WafTenantService.TenantPermissionCeiling(tenantID)`：租户→套餐权限集快照，
  5 分钟缓存（与策略缓存同口径），套餐/租户编辑时主动失效；无租户行/未绑套餐
  返回 nil（无天花板）。数据库故障 fail-open（差异化控制不是隔离边界，
  隔离由 GORM 回调保证）。
- `Auth()` 把快照写入 `c.Set("tenantPermCeiling")`（OpenAPI Key/平台自身不设）；
  `RequirePermission` 在 superAdmin 判定后强制：权限码不在天花板 → 403
  （提示"当前套餐未包含该功能"）。**即使租户权限组被手工改大也越不出套餐边界**。
- 判定顺序：OpenAPI Key（按 Key 授权范围）→ superAdmin → 套餐天花板 →
  权限组 → legacy 角色。

### 测试锚定

- `service/waf_service/waf_tenant_plan_test.go`：套餐 CRUD 校验/开通按套餐下发/
  换套餐同步/套餐编辑传播/禁删/天花板矩阵/播种幂等。
- `service/waf_service/waf_account_tenant_scope_test.go`：账号列表/详情/编辑/删除
  租户限定、建号盖章与配额、哈希不外泄。
- `router/tenant_authz_e2e_test.go` 追加 5 个子测试：账号域收敛（HTTP 级）、
  自助建号角色强制、天花板授权集内可访问、组被改大仍 403、换受限套餐立即 403。
- `wafdb/tenant_matrix_test.go` 排除清单登记 Plan（平台注册表）。

### 待办（后续扩展点）

- 更多配额维度（规则数/日志保留天数/流量上限）——CheckTenantQuota 扩展 kind 即可。
- `permission:manage` 下放租户（专业版自助管理子账号权限组）：需先实现
  "租户侧组写入与套餐权限集取交集"，防止组内塞平台码。
- OpenAPI Key 绑定租户（oplatform_key 加租户列后切换注入点）。
- 计费聚合/按租户账单输出（ZJMF 对接形态见「业务系统对接」章）。
- 前端（SamWafWeb-Saas 仓库）：套餐管理页面、租户表单选套餐、账号管理页适配。


## 阶段0 的边界说明（历史）

> 下文为阶段0刚落地时的边界记录，其中"尚未让业务查询按租户隔离"一条已在
> 阶段2批量推广中解决，保留作演进轨迹。

阶段0 只打通"机制"，**尚未让业务查询真正按租户隔离**：
service 层目前仍直接用 `global.GWAF_LOCAL_DB` 调用，未传 `db.WithContext(ctx)`，
因此请求上下文中的租户还到不了 GORM 回调。这正是阶段2的主体工作。

## 阶段2 试点（已完成并批量推广）：请求级租户上下文传播

试点覆盖两条完整链路，验证了 ctx 传播模式在真实业务代码里成立；
随后已按同一契约批量推广到**全部业务配置类 service**（账户/系统配置/通知/
任务/统计等平台级服务留待豁免清单阶段处理）：

| 批次 | 范围 | 服务方法 | 调用方修复 |
|---|---|---|---|
| 试点 | 主机管理（WafHostService） | 26 | 55 |
| 试点 | IP/URL 白名单（WafWhiteIpService/WafWhiteUrlService） | 25 | 36 |
| 批量·防护规则类 | IP/URL 黑名单、自定义规则、防篡改、敏感词、脱敏、CC、IP组、防火墙联动等 12 个文件 | 124 | ~170 |
| 批量·SSL/站点组织类 | 证书夹/到期/申请/CA、主机分组、负载均衡、缓存规则、CDN 等 8 个文件 | 89 | 86 |
| 批量·访问认证杂项类 | HTTPAuth 会话/基线、统一访问认证配置、隧道、隐私信息/分组、路径规则、拦截页、UI 偏好等 9 个文件 | 67 | 99 |
| 收口 | api 层 NotifyWaf 系列（16 处定义，13 处带 DB 查询全部收口） | 13 | 48 |

全部调用点遵循统一规则：gin handler 传 `c.Request.Context()`；引擎/定时任务/
初始化等无请求语境传 `context.Background()`（回退全局，行为不变）；
service→service 透传同一 ctx。`NotifyWaf` 中 3 处无 DB 查询的保持签名不变。

配套改动：
- `wafdb.RegisterTenantScopeCallbacks(db)`：租户回调注册收拢为单一入口，
  三套数据库初始化流程与测试共用（原 10 处散落注册全部改为调用它）。
- `service/waf_service/waf_host_tenant_scope_test.go`：service 级端到端隔离测试
  （临时 SQLite + 生产同款回调），断言：列表按上下文租户过滤、**按精确 code 点查
  跨租户站点不可见**（详情与列表两个入口都覆盖）、无 ctx 回退全局。

### 写入侧加固（已落地）：CREATE 盖章 + UPDATE/DELETE 按请求租户过滤

`wafdb.RegisterTenantScopeCallbacks` 现在注册四类回调，语义：

| 语句 | 回调 | 语义 |
|---|---|---|
| SELECT | `before_query` | 追加 `tenant_id + user_code` 过滤；无 ctx 回退全局（与既有行为一致） |
| UPDATE | `before_update` | **仅当语句显式携带请求租户上下文时**追加过滤，防止按 code/id 改到其他租户的行 |
| DELETE | `before_delete` | 同上 |
| INSERT | `before_create` | 按租户盖章：**ctx 声明了租户时强制覆盖**业务代码写入的租户值（防止存量代码残留的全局常量赋值把行写进错误租户）；无 ctx 时只补空缺字段、不覆盖显式值 |

**写路径（UPDATE/DELETE）与查询路径取值策略不同的原因**：数据密钥迁移、库表
修复等平台维护任务在启动期无 ctx 运行，必须能改写租户列尚为空的存量行
（`TestDataKeyMigrateOTPPlaintext` 验证了这一约束）；它们的多租户语义由
后台任务治理（任务 4）显式处理，不走隐式全局回退。

公共取值逻辑：查询 `tenantScopeFrom`（ctx 优先、全局回退）；写路径
`writeScopeFrom`（仅 ctx）；语句模型不含 `tenant_id`/`user_code` 列（如裸
`Table()` 语句、map 创建）时一律跳过，不做误伤。无 ctx 的行为与改造前完全一致。

测试：`wafdb/tenant_write_scope_test.go` 覆盖更新/删除的跨租户不可达、
无 ctx 的平台维护可达语义、CREATE 的 ctx 覆盖与补空缺语义。

> 注意：INSERT 盖章的"ctx 权威覆盖"意味着 `Tenant_ID: global.GWAF_TENANT_ID`
> 这类手工赋值在请求链路上变成无效兜底——232 处手工赋值**无需紧急清理**，
> 可在后续维护中逐步移除；但未来需要"以指定租户身份写入"的平台代码
> （如代租户开通资源）不得携带请求 ctx，应显式构造目标租户的 ctx。

### ctx 传播模式规范（后续批量推广遵守）

1. 触库的导出 service 方法第一参加 `ctx context.Context`，方法名与返回值不变。
2. 方法内首次用库前 `db := global.GWAF_LOCAL_DB.WithContext(ctx)`，方法内原全局句柄
   全部改用 `db`；事务在外层包：`global.GWAF_LOCAL_DB.WithContext(ctx).Transaction(...)`
   （或 `Begin()`），`tx *gorm.DB` 形参签名不动（事务继承 ctx）。
3. 调用方：gin handler 传 `c.Request.Context()`；service→service 透传同一 ctx；
   引擎/定时任务/初始化等无请求语境传 `context.Background()`（回退全局，行为不变）。
4. 纯逻辑辅助方法与拷贝 `tx` 的私有 helper 不动。

### 平台级作用域与豁免（任务3/4/5 已落地）

**PlatformScope（平台级作用域标记）**：`wafctx.PlatformScope(ctx)` 把 ctx 标记为
平台级，数据库租户回调对其**跳过全部过滤与盖章**，读写全部租户数据。仅限两类场景：
- **数据面引擎**：WAF 引擎要为所有租户的站点转发流量，加载配置/证书/规则必须是
  全租户的——引擎与定时任务（waftask/cmd/wafssl）对 service 的调用统一传
  `wafctx.PlatformScope(context.Background())`；
- **平台维护任务**：数据密钥迁移、库表修复、批量升级等。

业务请求链路禁止使用（request ctx 携带租户时按租户隔离）。

**平台表豁免清单（任务3）**：`wafdb/localdb.go` 的 `tenantExemptModels` 按模型
类型（`schema.ModelType`，不受表名命名策略影响）豁免租户过滤/盖章，当前包含：
`Account`（管理员）、`TokenInfo`（会话）、`Otp`（2FA）、`AccountPwdHistory`、
`LoginHistory`、`SecurityAuditLog`、`SystemConfig`、`WafSysLog`、`HostLoginEvent`、
`Tenant`（租户注册表）、`Plan`（套餐注册表）。
注意：Account 虽豁免，但账号域的租户读写限定由服务层手工完成
（`accountTenantScope`，见「套餐与租户权限差异化」章）。
通知渠道、任务、开放平台 Key 等**不在豁免清单**——它们在未来多租户下应按租户隔离
（租户 A 只看到自己的渠道/任务），保持过滤是正确行为。

**OpenAPI Key 绑定（任务5）**：Key 当前为实例级凭证，鉴权通过后注入平台租户上下文
（`middleware/openapi_auth.go`），Key 调用的 DB 访问与登录会话语义一致。
未来 Key 绑定租户时，给 `oplatform_key` 增加租户列并在注入点改为 Key 归属租户。

**后台任务语义（任务4）**：现行约定——
- 无 ctx（`context.Background()`）：平台维护语义，写路径不过滤、读路径回退全局；
  适合纯维护操作；
- `wafctx.PlatformScope(Background())`：数据面/全租户任务（引擎、证书续签、健康
  检查、威胁情报同步等），读写全租户；
- 将来"按租户循环执行"的任务：`wafctx.WithTenant(context.WithoutCancel(reqCtx), ...)`
  或遍历租户注册表逐个构造 ctx；脱离请求生命周期的工作必须 `context.WithoutCancel`
  派生，防止请求结束误杀 DB 操作。

### 试点发现的新遗漏点（已收口/记入任务清单）

- ~~`NotifyWaf(host_code)`（api 层，白名单增删后通知引擎）~~：已收口——16 处定义
  中 13 处带 DB 查询的全部改为按请求租户查询。
- 白名单 `GetHostCodes/GetHostCodesByIds` 存在引擎侧调用（传 `context.Background()`）：
  未来多租户下引擎按站点加载规则时需携带站点归属租户的 ctx → 任务 8。

## 绕过通道审计（任务6 已完成）与修复

对不经 GORM 租户回调的数据通道做了专项审计（Raw/Exec、Table 通道、命令行工具、
诊断 API、队列消费侧），结论与处置：

**已修复（本轮）：**

| 编号 | 通道 | 风险 | 修复 |
|---|---|---|---|
| H1 | IP 标签列表/删除（`waf_ip_tag_log.go` 5 处 Raw/Exec 硬编码全局租户，任意登录角色可达） | 高 | 方法 ctx 化 + 新增 `rawSQLTenantScope`（原生 SQL 租户取值唯一入口，禁止直接引用全局常量）；API 层传 `c.Request.Context()` |
| H2 | 日志导出（后台 goroutine 无 ctx → 内容恒为回退租户；下载槽无归属校验） | 高 | 导出 goroutine 用 `context.WithoutCancel(c.Request.Context())` 携带发起者租户，导出内容按租户过滤；下载槽新增 `GWAF_RUNTIME_EXPORT_DB_LOG_TENANT` 归属校验，他租户下载被拒 |
| M2 | CC 阈值推荐 Raw SQL 无租户条件（靠 host 查询间接把门） | 中 | 查询计划强制携带 `tenant_id + user_code` 条件；host 门禁改为请求 ctx |

**已锚定（防重构翻车）：**

- `wafdb/tenant_bypass_channels_test.go` 锚定三类通道语义：
  **Table() 查询（nil schema）仍被回调过滤**（waf_sql_query、日志详情等通道的隔离
  依赖它）；**Raw/Exec 不经过回调**（使用方必须自带租户条件）。改变语义须同步修改依赖方。
- `wafdb/tenant_matrix_test.go`（任务7）：52 个租户级模型逐表断言隔离，
  49 通过、3 跳过（见下）。

**维持现状（低风险，已文档标注）：**

- 命令行 execsql/repairdb/migratedb/rekey（L1-L4）：仅本机 shell 可达，天然全租户，
  文档标注"绕过租户隔离"；
- 启动期迁移/补丁 SQL（L5/L6）：设计上先于回调注册执行；
- 保留期清理（L7）：平台级语义，不分租户；
- `IncrCallCount`（L9）、诊断内存画像（L10）、缓存键（L11）：无跨租户面或计量值。

**遗留（记录待办，不阻塞）：**

- ~~M4：OpenAPI Key 被 RequireRole 整体放行~~ **已收口**：Key 增加 `Permissions`
  授权范围（权限码逗号分隔，**空=全部权限，存量 Key 向后兼容**），
  `RequirePermission` 对 Key 按码判定，非法码写入侧报错（fail-closed）；
  Key 管理接口/模型已支持（迁移 202610260001）。
- ~~**跨租户唯一索引**~~ **已决策并落地**：`ip_watchlists` 的 IP 唯一性是租户内的，
  迁移 202610260001 重建为 (tenant_id, user_code, ip) 复合唯一；`waf_apps.code`、
  `waf_plugin_configs.plugin_id/key` 为**实例级平台资源**（本地二进制路径、平台域管理），
  全局唯一是正确语义，保持并在矩阵测试中标注排除。
- **跨租户越权 HTTP 级回归**：`router/tenant_authz_e2e_test.go`（真实 Auth 中间件 +
  真实路由 + 双租户令牌的端到端断言）。真正的黑盒渗透测试仍建议在预发环境做一次。

## 业务系统对接（ZJMF-CBAP 智简魔方计费）——预留，最后做

支付/套餐/订单全在业务系统侧；对接形态为 ZJMF 模块调用 SamWaf 管理端 API：
开通 = `POST /api/v1/tenant/add`（含租户管理员初始凭据），续费 = `POST /api/v1/tenant/renew`，
停用/退款 = `POST /api/v1/tenant/edit`（状态）。对接时需为 ZJMF 签发限定
`platform:admin` 范围的 OpenAPI Key（Key 授权范围机制已就绪），并补充
"按租户用量账单"的输出接口（计费聚合未实现，见租户生命周期章节）。

## 全表隔离测试矩阵（任务7 已完成）

`wafdb/tenant_matrix_test.go` 覆盖 52 个租户级模型，逐表断言"租户A 上下文只见
租户A 的行、tenantB 不可见、无 ctx 回退全局得 0 行"：**49 通过、3 跳过**（跳过
原因：`IPWatchlist`/`WafApp`/`WafPluginConfig` 的唯一索引不含租户列，零值种子
第二行必撞唯一键——该索引设计问题见上文"跨租户唯一索引"）。
平台豁免 9 表、日志/统计类、平台治理表在文件头注释中明确排除。
扩展模型清单即维护本矩阵，新表入库前先过矩阵。

## 租户隔离完整度审计与收口（2026-10 第二轮）

针对"AI 功能是否隔离 + 全系统隔离完整度"的专项审计发现并修复了以下缺口（六条
并行工作流 + 中心修复）：

### 机制级修复（影响面最大）

- **GORM Row 回调链补注册**（`wafdb/localdb.go`）：GORM 的 `Scan()/Row()` 走 Row
  回调链而非 Query 链，此前**所有 Scan 系查询静默绕过租户过滤**（统计/聚合读取
  大量使用）。现把同一 `before_query` 挂到 Row 链，语义与 Find 完全一致；
  `wafdb/tenant_row_callback_test.go` 锚定（模型 Scan / Table 无 schema Scan /
  无 ctx 回退全局）。
- **引擎数据面 PlatformScope**（`wafenginecore/wafworker.go`、`wafengine.go`、
  `anticc_limiter.go`）：LoadAllHost/LoadHost 的全部直查（主机/规则/CC/名单/缓存/
  防篡改/路径规则/敏感词等）原先无 ctx 走全局回退——**租户站点从未被引擎加载**。
  现统一 `engineLoadCtx()`（PlatformScope）。
- **日志读取链路 ctx 化**（`waf_log_query.go`/`waf_log.go`/`waf_log_payload.go`）：
  列表/详情/识别码直查/分片扇出/报文回填原先用内部 Background ctx——租户看到的
  是平台租户的日志、自己的日志不可见。现全程携带请求租户；
  `waf_log_tenant_scope_test.go` 锚定六条语义。
- **流量统计按站点归属盖章**（`waftask/task_traffic_stats.go`）：采集器原先把
  全部租户的流量都盖成平台租户戳 + 总览查询无 ctx 全量命中——**每个租户的仪表盘
  能看到全部租户的域名与流量**。现按 host_code→归属租户映射盖章（归属解析失败
  跳过并告警，fail-closed）；统计/分析读取全部 ctx 化。

### 破坏性/实例级操作收权（URL 均不变，移入 tokenPlatformGroup）

数据文件管理（可删共享日志库）、日志分片删除、IP 归属地库变更（上传/重载/配置/
升级，查询类保留）、通知日志文件清空、**主机登录防护整组**（本机 SSH 防护+系统
防火墙封禁，事件/封禁/阶梯均为实例级）、**管理端自身配置整组**（vpconfig：应急
路径/管理口 IP 白名单/可信代理/管理端证书/重启——读接口也披露安全面）、**数据
保留策略**（实例级日志治理）。前端对应菜单权限码已同步（见 SamWafWeb-Saas）。

### AI 功能隔离结论

- `/api/v1/ai/*`（模型/导出/标注）与 `/gpt/config`、`/gpt/chat`：平台域/租户禁持
  码，租户不可达；引擎 AI 检测写本租户日志行，隔离正确。
- **AI 规则生成（aigen/aiprompt）**：原挂安全域（租户可达）消耗平台全局 GPT
  凭据，现处理器层拒绝租户调用（"AI能力使用平台统一配置的模型服务，仅平台侧
  账号可用"）。
- **AI 语料作用域（已收口）**：仪表盘/训练数据导出/标注工作台统一走
  `AIScopeCtx`/`AIScopeTenant`——平台侧查询提升为 `PlatformScope`（监管全部租户
  命中，不再只看平台切片），租户侧只看本租户；标注行随作用域归属（平台标注为
  平台公共语料，租户标注归租户自己）。导出 goroutine 用 `context.WithoutCancel`
  派生并捕获发起者租户，避免后台任务落回平台桶。

### WebSocket 按租户路由

连接注册携带租户（平台租户=平台作用域连接）；`BroadcastBuildToTenant` 按消息
`TenantId` 投递（平台作用域连接可总览）；引擎事件/CC 封禁/防篡改/导出结果均带
归属租户；**无租户信息的消息 fail-closed 只发平台连接**。节点级消息（升级结果/
系统统计/系统错误）保留全局广播。`BaseMessageInfo.TenantId` 为 `json:"-"`，
前端协议不变。

### 服务层 ctx 化与盖章修复

任务/批量任务（含后台处理器按任务归属租户写入 IP 名单）、通知渠道/订阅/日志
（含订阅配置越权写入点收口：按 id 的 UPDATE/READ 随请求租户过滤）、一键修改
（记录按发起者租户）、IP 观察名单、IP 归属查询辅助名单、Excel 导入（按发起者
租户盖章+校验，且逐行过 `max_hosts` 配额——入口只查一次会被批量导入绕过；另修
两处导入自身缺陷：int64 字段落入"不支持的字段类型"分支导致任何站点行都插不进去，
`Create` 传结构体值使租户盖章反射赋值不可寻址而 panic）、安全审计日志（豁免模型
手工分桶：写盖请求租户、租户只见自己、平台总览全量）。

### 引擎请求期防篡改 ctx

`wafenginecore/tamper.go` 三处触库补齐作用域：按 id 的基线学习/篡改计数 UPDATE
随规则行自身租户过滤（`tamperRuleCtx`，行租户为空时等于未声明，保持原有无过滤
行为）；`notifyTamperReload` 改走 `engineLoadCtx()`（平台作用域，与启动加载同语义）
——此前不带 ctx 会回退全局租户常量，租户站点的规则查出空表并推给引擎，热更新把
已加载的保护静默清空。反代与静态两条链路共用同一组函数，一处修复两处生效。

### 通知 / 操作反馈 / 审计归属收口（本轮）

- **通知发送侧按事件归属租户派发**（`waf_notify_sender.go`/`waf_notify_subscription.go`）：
  所有消息结构内嵌 `BaseMessageInfo` 并提升出 `Tenant()`（字段 `json:"-"`，前端
  报文不变）；`GetSubscriptionsForEvent(tenantID, messageType)` 取「租户桶 + 平台
  桶」——只投平台桶租户收不到自己的告警，只投租户桶平台监管订阅会噤声；事件无
  归属时只取平台桶（fail-closed）。渠道读取、抑制留痕、发送日志统一用订阅行自身
  的租户 ctx（`notifySubCtx`），避免租户订阅的通知日志落进平台桶。
- **操作反馈类 WS 消息补 `TenantId`**：站点增删改/日志导出与下载/端口告警/登录
  结果/导入回执/SSL 订单结果/SSL 过期提醒/访问认证审计通知等生产方均标注事件
  归属；SSL 过期任务的行本身是平台戳，改按「域名+端口」反查站点租户（查不到
  回落平台桶）。仍保持平台级的只有实例级设施：主机登录防护、威胁 IP 同步、
  管理端证书任务、节点端口监听错误、汇总类系统通知。
- **安全审计写入归属**（`waf_security_audit_service.go`）：`AuditEntry` 增加显式
  `TenantID`，归属优先级为「请求 ctx 租户 > 调用方显式租户 > 平台桶」。引擎数据面
  没有管理端 ctx，由 `httpAuthAudit` 按 `hostTarget.Host.Tenant_ID` 带下来，一处
  覆盖网站密码访问的 7 个写入点；节流键与认证通知节流键都加入租户，避免跨租户
  互相压掉。请求链路（诊断导出、SSL 操作、HTTPAuth 会话踢人）改用 `WriteCtx`。
- **HTTPAuth 会话归属**：引擎建会话时按站点租户显式盖行戳（PlatformScope 下回调
  不代劳）；列表/踢人去掉与请求租户矛盾的硬编码全局过滤，改由回调按 ctx 限定；
  过期清理先按 `(tenant_id, user_code)` 分组，再逐租户各写一条审计。
- **测试**：新增通知订阅分桶、消息租户载体、审计归属优先级、AI 作用域四组单测；
  `cross_engine_side/stats` 已随新签名移植，crossdb 套件的租户回调桩改为生产同款
  `wafdb.RegisterTenantScopeCallbacks`（旧桩只挂 Query 且恒用全局常量，ctx 化后的
  service 写入会造出无归属行），`-tags crossdb` 的 SQLite 套件已全绿；MySQL/PG
  仍需外部测试库时补跑。

### 遗留待办（记录不阻塞）

- 存量未盖章 host 行的流量统计按"归属不可解析跳过"处理（如需可补回填迁移）。

## 阶段2 任务清单（数据面多租户化）

1. **上下文传播**：~~已完成~~——试点 + 三批推广覆盖全部业务配置类 service
   （约 320 个服务方法、490+ 调用点）；平台级服务（账户/系统配置/通知/任务/统计/
   OpenAPI Key）随任务 3/5 的豁免与绑定设计一并处理。
2. **写入侧**：~~回调已落地~~（见"写入侧加固"节：CREATE 盖章 + UPDATE/DELETE
   按请求租户过滤）；232 处手工 `Tenant_ID: global` 赋值已成为无效兜底，
   后续维护中逐步移除即可，无紧急性。
3. ~~**平台表豁免清单**~~：已落地——`wafdb/localdb.go` 的 `tenantExemptModels`
   白名单（按模型类型豁免，清单见上文）。
4. ~~**后台任务租户语义**~~：已落地——引擎/定时任务统一 `PlatformScope`（数据面
   全租户）；维护任务无 ctx（写路径不过滤）；约定见上文，"按租户循环"模式待
   租户注册表落地后启用。
5. ~~**OpenAPI Key 绑定租户**~~：已落地第一阶段（注入平台租户上下文）；Key 级
   租户绑定待 `oplatform_key` 增加租户列后切换。
6. ~~**绕过通道审计**~~：已完成——H1/H2/M2 已修复，M1 行为已测试锚定，M4
  （OpenAPI Key 角色边界）记录待办，详见"绕过通道审计"节。
7. ~~**全表隔离测试矩阵**~~：已完成——52 模型矩阵（49 过/3 跳过，见上）；
   三种数据库驱动复跑依赖 `-tags crossdb` 环境变量，待有 MySQL/PG 测试库时补跑。
8. ~~**引擎日志归属**~~：已完成——访问日志 `TenantId` 从站点归属租户取
   （`hostTarget.Host.Tenant_ID`，未知站点回退平台租户）；引擎/定时任务/启动
   初始化共 50 处触库调用统一 `wafctx.PlatformScope(context.Background())`
   （数据面全租户），基础设施 bean（启停 SysLog 等）保持平台租户。
9. ~~**api 层直查收口**~~：~~`NotifyWaf(host_code)` 等绕过 service 直接查全局 DB 的
   api 层代码~~ 已收口（见上）。后续若新增 api 层直查代码，一律走带 ctx 的 service 方法。

## 风险与对策（重点）

- **跨租户泄漏**：唯一高危项。对策：全表隔离测试矩阵 + 强制 code review 约定
  （service 层禁止裸用全局 DB 句柄）。
- **GORM 回调误伤**：回调对无租户列的查询会追加条件导致 SQL 错误；
  所有模型必须内嵌 BaseOrm（现状成立），豁免表走白名单。
- **性能**：共享库下路由快照随站点数增长，重建频率与内存需压测；
  多租户共享库建议直接使用 MySQL/PostgreSQL，SQLite 仅保留单租户私有化模式。
