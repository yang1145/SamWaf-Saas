package router

import (
	"SamWaf/global"
	"SamWaf/middleware"
	"SamWaf/wafsec"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
)

const appFeatureStatusPath = "/api/v1/application/app/feature_status"

func TestAppFeatureStatusAndDisabledList(t *testing.T) {
	priorEnabled, priorLegacy, priorKey := global.GWAF_CAN_APP_MANAGE, global.GCONFIG_COMM_LEGACY_KEY, global.GWAF_COMMUNICATION_KEY
	global.GCONFIG_COMM_LEGACY_KEY = true
	global.GWAF_COMMUNICATION_KEY = []byte("0123456789abcdef0123456789abcdef")
	t.Cleanup(func() {
		global.GWAF_CAN_APP_MANAGE, global.GCONFIG_COMM_LEGACY_KEY, global.GWAF_COMMUNICATION_KEY = priorEnabled, priorLegacy, priorKey
	})

	r := gin.New()
	new(WafAppRouter).InitWafAppRouter(r.Group(""))
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			global.GWAF_CAN_APP_MANAGE = enabled
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, appFeatureStatusPath, nil))
			var reply struct {
				Code int    `json:"code"`
				Data string `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || rec.Code != http.StatusOK || reply.Code != 0 {
				t.Fatalf("feature status response=%s status=%d err=%v", rec.Body.String(), rec.Code, err)
			}
			plain, err := wafsec.AesDecrypt(reply.Data, global.GWAF_COMMUNICATION_KEY)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]bool
			if err := json.Unmarshal(plain, &fields); err != nil {
				t.Fatal(err)
			}
			got, exists := fields["enabled"]
			if len(fields) != 1 || !exists || got != enabled {
				t.Fatalf("feature status data=%s want only enabled=%v", plain, enabled)
			}
		})
	}

	global.GWAF_CAN_APP_MANAGE = false
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/application/app/list", nil))
	var reply struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || rec.Code != http.StatusForbidden || reply.Code != 403 {
		t.Fatalf("disabled list response=%s status=%d err=%v", rec.Body.String(), rec.Code, err)
	}
}

func TestAppFeatureStatusRejectsAPIKey(t *testing.T) {
	r := gin.New()
	group := r.Group("", middleware.TokenOnlyAuth())
	new(WafAppRouter).InitWafAppRouter(group)
	req := httptest.NewRequest(http.MethodGet, appFeatureStatusPath, nil)
	req.Header.Set("X-API-Key", "test-api-key")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var reply struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || reply.Code != -999 {
		t.Fatalf("API key response=%s err=%v", rec.Body.String(), err)
	}
}

func TestAppRouterProductionTokenOnlyMount(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "../wafmangeweb/localserver.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	mounts := 0
	tokenOnlyAuth, platformPerm, systemAdmin := false, false, false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "InitWafAppRouter" {
			mounts++
			if len(call.Args) != 1 {
				t.Fatal("application router must receive its authenticated group")
			}
			group, ok := call.Args[0].(*ast.Ident)
			if !ok || group.Name != "tokenPlatformGroup" {
				t.Fatal("application router must be mounted on tokenPlatformGroup (platform:admin domain)")
			}
		}
		group, ok := sel.X.(*ast.Ident)
		if !ok || sel.Sel.Name != "Use" {
			return true
		}
		for _, arg := range call.Args {
			mw, ok := arg.(*ast.CallExpr)
			if !ok {
				continue
			}
			fn, ok := mw.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			pkg, ok := fn.X.(*ast.Ident)
			if !ok || pkg.Name != "middleware" {
				continue
			}
			// TokenOnlyAuth 在父组 TokenOnlyRouterGroup 上（对全部 TokenOnly 路由生效）
			if group.Name == "TokenOnlyRouterGroup" && fn.Sel.Name == "TokenOnlyAuth" {
				tokenOnlyAuth = true
			}
			// 权限码与角色判定在子组 tokenPlatformGroup 上
			if group.Name != "tokenPlatformGroup" || fn.Sel.Name != "RequirePermission" || len(mw.Args) < 1 {
				continue
			}
			if code, ok := mw.Args[0].(*ast.SelectorExpr); ok {
				pkg, ok := code.X.(*ast.Ident)
				platformPerm = ok && pkg.Name == "enums" && code.Sel.Name == "PERM_PLATFORM_ADMIN"
			}
			if len(mw.Args) >= 2 {
				if role, ok := mw.Args[1].(*ast.SelectorExpr); ok {
					pkg, ok := role.X.(*ast.Ident)
					systemAdmin = ok && pkg.Name == "enums" && role.Sel.Name == "ROLE_SYSTEM_ADMIN"
				}
			}
		}
		return true
	})
	if mounts != 1 || !tokenOnlyAuth || !platformPerm || !systemAdmin {
		t.Fatalf("application mounts=%d TokenOnlyAuth=%v platform:admin=%v system admin=%v",
			mounts, tokenOnlyAuth, platformPerm, systemAdmin)
	}
}
