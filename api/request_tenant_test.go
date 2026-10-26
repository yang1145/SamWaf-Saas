package api

import (
	"context"
	"testing"

	"SamWaf/common/wafctx"
	"SamWaf/global"
)

// TestRequestTenantID 操作反馈消息的租户标注：有请求租户带请求租户，
// 平台侧请求回落平台租户（WS 侧会归一到平台连接，等价于不标注）。
func TestRequestTenantID(t *testing.T) {
	oldTenant, oldUser := global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	defer func() { global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser }()
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"

	if got := requestTenantID(wafctx.WithTenant(context.Background(), "t-a", "userA")); got != "t-a" {
		t.Fatalf("租户请求应标注 t-a，实际 %q", got)
	}
	if got := requestTenantID(context.Background()); got != "tenantZ" {
		t.Fatalf("平台请求应回落平台租户 tenantZ，实际 %q", got)
	}
	if got := requestTenantID(wafctx.WithTenant(context.Background(), "", "userA")); got != "tenantZ" {
		t.Fatalf("空租户码应回落平台租户 tenantZ，实际 %q", got)
	}
}
