package middleware

import (
	"SamWaf/common/zlog"
	"SamWaf/enums"
	"SamWaf/model/common/response"
	"SamWaf/service/waf_service"

	"github.com/gin-gonic/gin"
)

// RequireRole 基于角色的访问控制中间件（三权分立强制点）。
// 规则：
//   - 超级管理员(superAdmin) 恒通过（含空角色兜底，保证向后兼容）
//   - OpenAPI Key 调用（is_openapi）放行：API Key 已经过独立校验，
//     其细粒度授权属于开放平台 Key 体系，不在账号角色范围内，避免影响既有自动化
//   - 角色命中 allowedRoles 通过，否则返回 403
//
// 必须挂载在 Auth() 之后（依赖其写入的 userRole）。
func RequireRole(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// OpenAPI Key 调用放行（保持既有能力，不被账号角色限制）
		if isOpenApi, ok := c.Get("is_openapi"); ok {
			if b, _ := isOpenApi.(bool); b {
				c.Next()
				return
			}
		}

		roleVal, _ := c.Get("userRole")
		role, _ := roleVal.(string)
		role = enums.NormalizeRole(role)

		// 超级管理员恒通过
		if role == enums.ROLE_SUPER_ADMIN {
			c.Next()
			return
		}

		for _, allowed := range allowedRoles {
			if role == allowed {
				c.Next()
				return
			}
		}

		response.ForbiddenWithMessage("当前账号角色无权访问该功能", c)
		c.Abort()
	}
}

// permissionVerdict 单码判定的三种结果（evaluatePermissionCode 的返回值）。
type permissionVerdict int

const (
	// permissionGranted 权限码放行
	permissionGranted permissionVerdict = iota
	// permissionRefused 已被套餐天花板/权限组明确拒绝，不再回落 legacy 角色
	permissionRefused
	// permissionNoGroup 账号未绑定权限组，交由调用方的 legacyRoles 参数判定
	permissionNoGroup
)

// evaluatePermissionCode 单码判定的共享实现（RequirePermission/RequireAnyPermission 共用）：
// superAdmin → 套餐天花板 → 权限组。不写响应、不处理 OpenAPI Key 与 legacy 角色，
// 这两者由调用方按各自参数处理。
//
// 组权限读取带缓存（权限组增改时由服务层失效缓存）；查询失败 fail-closed。
func evaluatePermissionCode(c *gin.Context, code string) (permissionVerdict, string) {
	roleVal, _ := c.Get("userRole")
	role, _ := roleVal.(string)
	if enums.NormalizeRole(role) == enums.ROLE_SUPER_ADMIN {
		return permissionGranted, ""
	}

	// 套餐天花板（防御纵深）：租户账号请求平台未授权的域时直接拒绝，
	// 与权限组判定相互独立——先挡一层，组内容再挡一层
	if ceilingVal, ok := c.Get("tenantPermCeiling"); ok {
		if ceiling, ok2 := ceilingVal.(map[string]bool); ok2 && !ceiling[code] {
			return permissionRefused, "当前套餐未包含该功能，请联系平台管理员升级"
		}
	}

	groupId, _ := c.Get("userGroupId")
	groupIdStr, _ := groupId.(string)
	if groupIdStr == "" {
		return permissionNoGroup, ""
	}
	has, err := waf_service.WafPermissionGroupServiceApp.HasPermission(c.Request.Context(), groupIdStr, code)
	if err != nil {
		zlog.Error("权限组判定查询失败，fail-closed", "group_id", groupIdStr, "code", code, "error", err.Error())
		return permissionRefused, "权限信息获取失败，请稍后重试"
	}
	if has {
		return permissionGranted, ""
	}
	return permissionRefused, "当前账号所属权限组无权访问该功能"
}

// handleOpenApiPermission OpenAPI Key 通道的判定（两个中间件共用）。
// 返回 true 表示已处理完（已放行并 c.Next，或已写拒绝响应并 Abort），调用方应立即 return。
//
// Key 授权范围（审计 M4 收口）：空集合=全部权限（存量 Key 兼容），非空集合要求
// codes 中任一命中——单码端点只有一个候选码，多码端点按「任一」语义。
func handleOpenApiPermission(c *gin.Context, codes []string) bool {
	isOpenApi, ok := c.Get("is_openapi")
	if !ok {
		return false
	}
	if b, _ := isOpenApi.(bool); !b {
		return false
	}
	if permsVal, ok := c.Get("openapi_perms"); ok {
		if set, ok2 := permsVal.(map[string]bool); ok2 && len(set) > 0 {
			for _, code := range codes {
				if set[code] {
					c.Next()
					return true
				}
			}
			response.ForbiddenWithMessage("当前 API Key 无权访问该接口", c)
			c.Abort()
			return true
		}
	}
	c.Next()
	return true
}

// roleInLegacyRoles 判断当前账号的 legacy 角色是否命中允许列表。
func roleInLegacyRoles(c *gin.Context, legacyRoles []string) bool {
	roleVal, _ := c.Get("userRole")
	role, _ := roleVal.(string)
	role = enums.NormalizeRole(role)
	for _, allowed := range legacyRoles {
		if role == allowed {
			return true
		}
	}
	return false
}

// RequirePermission 权限组判定中间件（多租户自定义角色）。
//
// 判定顺序：
//  1. OpenAPI Key 调用放行（与 RequireRole 同口径，见上）；
//  2. superAdmin（含空角色兜底）恒通过；
//  3. 套餐权限天花板（Auth 写入的 tenantPermCeiling）：租户绑定了套餐时，
//     权限码必须同时在天花板内——即使租户的权限组被改大也无法越出套餐边界；
//     平台自身/未绑套餐/OpenAPI Key 无天花板，行为不变；
//  4. 账号绑定了权限组（Auth 写入的 userGroupId 非空）→ 按组的权限码判定，
//     不再回落 legacy 角色——权限组的授权边界是完整替代，不是叠加；
//  5. 未绑定权限组 → 走 legacyRoles（旧四角色），存量账号行为不变。
//
// 组权限读取带缓存（权限组增改时由服务层失效缓存）；查询失败 fail-closed 返回 403。
// 必须挂载在 Auth() 之后。
func RequirePermission(code string, legacyRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if handleOpenApiPermission(c, []string{code}) {
			return
		}
		verdict, reason := evaluatePermissionCode(c, code)
		switch verdict {
		case permissionGranted:
			c.Next()
			return
		case permissionRefused:
			response.ForbiddenWithMessage(reason, c)
			c.Abort()
			return
		}
		if roleInLegacyRoles(c, legacyRoles) {
			c.Next()
			return
		}
		response.ForbiddenWithMessage("当前账号角色无权访问该功能", c)
		c.Abort()
	}
}

// RequireAnyPermission 多码任一命中即放行（OR 语义）。
//
// 用于被多个权限域共用的端点，避免页面功能被单一域“卡死”：
//   - /permission/group/codes（静态权限码目录）：权限组管理与套餐管理共用；
//   - /permission/group/options（本租户权限组下拉）：权限组管理与账号绑定共用。
//
// 判定顺序与 RequirePermission 一致；候选码逐个走「天花板 → 权限组」，
// 任一码通过即放行。天花板对每个码独立判定，不存在“一个码的天花板放行另一个码”；
// 未绑定权限组的账号在全部候选码都无组授权时按 legacyRoles 兜底。
func RequireAnyPermission(codes []string, legacyRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if handleOpenApiPermission(c, codes) {
			return
		}
		firstReason := ""
		noGroup := false
		for _, code := range codes {
			verdict, reason := evaluatePermissionCode(c, code)
			switch verdict {
			case permissionGranted:
				c.Next()
				return
			case permissionNoGroup:
				noGroup = true
			default:
				if firstReason == "" {
					firstReason = reason
				}
			}
		}
		if noGroup && roleInLegacyRoles(c, legacyRoles) {
			c.Next()
			return
		}
		if firstReason == "" {
			firstReason = "当前账号角色无权访问该功能"
		}
		response.ForbiddenWithMessage(firstReason, c)
		c.Abort()
	}
}
