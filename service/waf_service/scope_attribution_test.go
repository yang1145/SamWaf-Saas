package waf_service

import (
	"context"
	"testing"

	"SamWaf/common/wafctx"
	"SamWaf/global"
)

// TestAuditTenantOf 审计归属优先级：请求租户 > 调用方显式租户 > 平台桶。
func TestAuditTenantOf(t *testing.T) {
	oldTenant, oldUser := global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	defer func() { global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser }()
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"

	cases := []struct {
		name       string
		ctx        context.Context
		entry      AuditEntry
		wantTenant string
		wantUser   string
	}{
		{
			name: "请求租户优先于引擎归属",
			ctx:  wafctx.WithTenant(context.Background(), "t-a", "userA"),
			entry: AuditEntry{
				TenantID: "t-other",
			},
			wantTenant: "t-a", wantUser: "userA",
		},
		{
			name:       "引擎数据面无请求上下文用站点归属",
			ctx:        wafctx.PlatformScope(context.Background()),
			entry:      AuditEntry{TenantID: "t-a"},
			wantTenant: "t-a", wantUser: "userZ",
		},
		{
			name:       "请求上下文即平台租户时用显式归属",
			ctx:        wafctx.WithTenant(context.Background(), "tenantZ", "userZ"),
			entry:      AuditEntry{TenantID: "t-a"},
			wantTenant: "t-a", wantUser: "userZ",
		},
		{
			name:       "都取不到落平台桶",
			ctx:        context.Background(),
			entry:      AuditEntry{},
			wantTenant: "tenantZ", wantUser: "userZ",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tenantID, userCode := auditTenantOf(c.ctx, c.entry)
			if tenantID != c.wantTenant || userCode != c.wantUser {
				t.Fatalf("期望 %s/%s，实际 %s/%s", c.wantTenant, c.wantUser, tenantID, userCode)
			}
		})
	}
}

// TestAIScope 平台侧 AI 功能必须跨租户取数（平台作用域），租户侧只看自己。
func TestAIScope(t *testing.T) {
	oldTenant, oldUser := global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	defer func() { global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser }()
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"

	tenantCtx := wafctx.WithTenant(context.Background(), "t-a", "userA")
	scope := AIScopeCtx(tenantCtx)
	if wafctx.PlatformScoped(scope) {
		t.Fatal("租户请求不应被提升为平台作用域")
	}
	if tid, ucode, ok := wafctx.TenantFrom(scope); !ok || tid != "t-a" || ucode != "userA" {
		t.Fatalf("租户作用域应保留原租户，实际 %q/%q ok=%v", tid, ucode, ok)
	}
	if tid, ucode := AIScopeTenant(tenantCtx); tid != "t-a" || ucode != "userA" {
		t.Fatalf("租户标注应落 t-a/userA，实际 %s/%s", tid, ucode)
	}

	// 平台侧（无租户上下文，或请求租户就是平台租户）→ 跨租户平台作用域
	for _, ctx := range []context.Context{context.Background(), wafctx.WithTenant(context.Background(), "tenantZ", "userZ"), nil} {
		if !wafctx.PlatformScoped(AIScopeCtx(ctx)) {
			t.Fatalf("平台侧 AI 查询应为平台作用域，ctx=%v", ctx)
		}
		if tid, ucode := AIScopeTenant(ctx); tid != "tenantZ" || ucode != "userZ" {
			t.Fatalf("平台侧标注应落平台租户，实际 %s/%s", tid, ucode)
		}
	}

	// 租户只声明租户码、没带识别号时，识别号回退全局
	if _, ucode := AIScopeTenant(wafctx.WithTenant(context.Background(), "t-a", "")); ucode != "userZ" {
		t.Fatalf("识别号应回退全局 userZ，实际 %s", ucode)
	}
}
