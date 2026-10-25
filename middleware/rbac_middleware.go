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

// RequirePermission 权限组判定中间件（多租户自定义角色）。
//
// 判定顺序：
//  1. OpenAPI Key 调用放行（与 RequireRole 同口径，见上）；
//  2. superAdmin（含空角色兜底）恒通过；
//  3. 账号绑定了权限组（Auth 写入的 userGroupId 非空）→ 按组的权限码判定，
//     不再回落 legacy 角色——权限组的授权边界是完整替代，不是叠加；
//  4. 未绑定权限组 → 走 legacyRoles（旧四角色），存量账号行为不变。
//
// 组权限读取带缓存（权限组增改时由服务层失效缓存）；查询失败 fail-closed 返回 403。
// 必须挂载在 Auth() 之后。
func RequirePermission(code string, legacyRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if isOpenApi, ok := c.Get("is_openapi"); ok {
			if b, _ := isOpenApi.(bool); b {
				// Key 授权范围判定（审计 M4 收口）：空集合=全部权限（存量 Key 兼容），
				// 非空集合按权限码判定——Key 不再无条件绕过角色/权限门
				if permsVal, ok := c.Get("openapi_perms"); ok {
					if set, ok2 := permsVal.(map[string]bool); ok2 && len(set) > 0 {
						if set[code] {
							c.Next()
							return
						}
						response.ForbiddenWithMessage("当前 API Key 无权访问该接口", c)
						c.Abort()
						return
					}
				}
				c.Next()
				return
			}
		}

		roleVal, _ := c.Get("userRole")
		role, _ := roleVal.(string)
		role = enums.NormalizeRole(role)
		if role == enums.ROLE_SUPER_ADMIN {
			c.Next()
			return
		}

		groupId, _ := c.Get("userGroupId")
		groupIdStr, _ := groupId.(string)
		if groupIdStr != "" {
			has, err := waf_service.WafPermissionGroupServiceApp.HasPermission(c.Request.Context(), groupIdStr, code)
			if err != nil {
				zlog.Error("权限组判定查询失败，fail-closed", "group_id", groupIdStr, "code", code, "error", err.Error())
				response.ForbiddenWithMessage("权限信息获取失败，请稍后重试", c)
				c.Abort()
				return
			}
			if has {
				c.Next()
				return
			}
			response.ForbiddenWithMessage("当前账号所属权限组无权访问该功能", c)
			c.Abort()
			return
		}

		for _, allowed := range legacyRoles {
			if role == allowed {
				c.Next()
				return
			}
		}

		response.ForbiddenWithMessage("当前账号角色无权访问该功能", c)
		c.Abort()
	}
}
