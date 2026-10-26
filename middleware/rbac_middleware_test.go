package middleware

import (
	"SamWaf/enums"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// setupRBACRouter 构造一个最小路由：先注入 userRole/is_openapi，再挂 RequireRole，
// 终端 handler 写出 "reached"。据此判断请求是否被放行。
func setupRBACRouter(userRole string, isOpenApi bool, allowed ...string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if userRole != "" {
			c.Set("userRole", userRole)
		}
		if isOpenApi {
			c.Set("is_openapi", true)
		}
		c.Next()
	})
	r.GET("/t", RequireRole(allowed...), func(c *gin.Context) {
		c.String(http.StatusOK, "reached")
	})
	return r
}

func TestRequireRole(t *testing.T) {
	cases := []struct {
		name        string
		role        string
		openapi     bool
		allowed     []string
		wantReached bool
	}{
		{"super always allowed", enums.ROLE_SUPER_ADMIN, false, []string{enums.ROLE_SECURITY_ADMIN}, true},
		{"empty role falls back to super", "", false, []string{enums.ROLE_SECURITY_ADMIN}, true},
		{"invalid role falls back to super", "garbage", false, []string{enums.ROLE_SECURITY_ADMIN}, true},
		{"matching role allowed", enums.ROLE_SECURITY_ADMIN, false, []string{enums.ROLE_SECURITY_ADMIN}, true},
		{"non-matching role denied", enums.ROLE_AUDIT_ADMIN, false, []string{enums.ROLE_SECURITY_ADMIN}, false},
		{"system admin denied on security route", enums.ROLE_SYSTEM_ADMIN, false, []string{enums.ROLE_SECURITY_ADMIN}, false},
		{"openapi bypass allowed", enums.ROLE_AUDIT_ADMIN, true, []string{enums.ROLE_SECURITY_ADMIN}, true},
		{"multi allowed list hit", enums.ROLE_SYSTEM_ADMIN, false, []string{enums.ROLE_SYSTEM_ADMIN, enums.ROLE_SECURITY_ADMIN}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := setupRBACRouter(tt.role, tt.openapi, tt.allowed...)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/t", nil)
			r.ServeHTTP(w, req)
			reached := w.Body.String() == "reached"
			if reached != tt.wantReached {
				t.Errorf("reached=%v want=%v (body=%q)", reached, tt.wantReached, w.Body.String())
			}
		})
	}
}

// RequireAnyPermission 的 OR 语义：多码任一命中即放行；天花板对每个候选码独立判定
// （不能因为某个候选码被拦就整体拒绝）；legacy 角色只在账号未绑定权限组时兜底。
func TestRequireAnyPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name        string
		setup       func(c *gin.Context)
		codes       []string
		legacy      []string
		wantReached bool
	}{
		{
			name:        "superAdmin 恒通过",
			setup:       func(c *gin.Context) { c.Set("userRole", enums.ROLE_SUPER_ADMIN) },
			codes:       []string{enums.PERM_GROUP_MANAGE, enums.PERM_PLATFORM_ADMIN},
			wantReached: true,
		},
		{
			name:        "未绑定权限组的 legacy 角色兜底",
			setup:       func(c *gin.Context) { c.Set("userRole", enums.ROLE_SYSTEM_ADMIN) },
			codes:       []string{enums.PERM_GROUP_MANAGE, enums.PERM_PLATFORM_ADMIN},
			legacy:      []string{enums.ROLE_SYSTEM_ADMIN},
			wantReached: true,
		},
		{
			name:        "未绑定权限组且 legacy 不命中",
			setup:       func(c *gin.Context) { c.Set("userRole", enums.ROLE_AUDIT_ADMIN) },
			codes:       []string{enums.PERM_GROUP_MANAGE, enums.PERM_ACCOUNT_MANAGE},
			legacy:      []string{enums.ROLE_SYSTEM_ADMIN},
			wantReached: false,
		},
		{
			name: "OpenAPI Key 授权范围任一命中",
			setup: func(c *gin.Context) {
				c.Set("is_openapi", true)
				c.Set("openapi_perms", map[string]bool{enums.PERM_PLATFORM_ADMIN: true})
			},
			codes:       []string{enums.PERM_GROUP_MANAGE, enums.PERM_PLATFORM_ADMIN},
			wantReached: true,
		},
		{
			name: "OpenAPI Key 授权范围不命中",
			setup: func(c *gin.Context) {
				c.Set("is_openapi", true)
				c.Set("openapi_perms", map[string]bool{enums.PERM_LOG_VIEW: true})
			},
			codes:       []string{enums.PERM_GROUP_MANAGE, enums.PERM_PLATFORM_ADMIN},
			wantReached: false,
		},
		{
			name: "天花板拦住全部候选码时 legacy 不兜底",
			setup: func(c *gin.Context) {
				c.Set("userRole", enums.ROLE_SYSTEM_ADMIN)
				c.Set("tenantPermCeiling", map[string]bool{enums.PERM_DASHBOARD: true})
			},
			codes:       []string{enums.PERM_GROUP_MANAGE, enums.PERM_ACCOUNT_MANAGE},
			legacy:      []string{enums.ROLE_SYSTEM_ADMIN},
			wantReached: false,
		},
		{
			name: "天花板放行其中一个候选码即可",
			setup: func(c *gin.Context) {
				c.Set("userRole", enums.ROLE_SECURITY_ADMIN)
				c.Set("tenantPermCeiling", map[string]bool{enums.PERM_ACCOUNT_MANAGE: true})
			},
			codes:       []string{enums.PERM_GROUP_MANAGE, enums.PERM_ACCOUNT_MANAGE},
			legacy:      []string{enums.ROLE_SECURITY_ADMIN},
			wantReached: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				tc.setup(c)
				c.Next()
			})
			r.GET("/t", RequireAnyPermission(tc.codes, tc.legacy...), func(c *gin.Context) {
				c.String(http.StatusOK, "reached")
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/t", nil))
			reached := w.Body.String() == "reached"
			if reached != tc.wantReached {
				t.Errorf("reached=%v want=%v (status=%d body=%q)", reached, tc.wantReached, w.Code, w.Body.String())
			}
		})
	}
}

// 管理端本地证书接口（generateLocalCert / rotateLocalCa / clearLocalCert）的权限矩阵。
//
// 这几个接口都会覆盖或删除管理端证书文件，破坏面与 uploadSslCert / restartManager 同级，
// 因此挂 RequireRole(ROLE_SYSTEM_ADMIN)。这里把"谁能调、谁不能调"钉死，
// 防止后来被挪到不带 RequireRole 的共享组里。
func TestLocalCertApiRoleMatrix(t *testing.T) {
	cases := []struct {
		name        string
		role        string
		openapi     bool
		wantReached bool
	}{
		{"超级管理员可调", enums.ROLE_SUPER_ADMIN, false, true},
		{"系统管理员可调", enums.ROLE_SYSTEM_ADMIN, false, true},
		{"安全管理员不可调", enums.ROLE_SECURITY_ADMIN, false, false},
		{"审计管理员不可调", enums.ROLE_AUDIT_ADMIN, false, false},
		// 现状如实记录：RequireRole 对 OpenAPI Key 放行（见其实现内的说明），
		// 因此任意有效 Key 都能调这些破坏性接口，不受账号角色约束。
		// 这条不是断言"应该如此"，而是锁住当前事实——真要收紧时这条用例会立刻变红，
		// 提醒同步修改本注释与相关文档。
		{"OpenAPI Key 当前被放行", "", true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := setupRBACRouter(tc.role, tc.openapi, enums.ROLE_SYSTEM_ADMIN)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/t", nil))

			reached := w.Body.String() == "reached"
			if reached != tc.wantReached {
				t.Fatalf("role=%q openapi=%v 期望放行=%v，实际=%v（状态码 %d，响应 %s）",
					tc.role, tc.openapi, tc.wantReached, reached, w.Code, w.Body.String())
			}
		})
	}
}
