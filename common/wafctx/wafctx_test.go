package wafctx

import (
	"context"
	"testing"
)

func TestWithTenantRoundTrip(t *testing.T) {
	ctx := WithTenant(context.Background(), "tenantA", "userA")
	tenantID, userCode, ok := TenantFrom(ctx)
	if !ok {
		t.Fatal("WithTenant 后 TenantFrom 应命中")
	}
	if tenantID != "tenantA" || userCode != "userA" {
		t.Fatalf("取值不符：tenant=%q user=%q", tenantID, userCode)
	}
}

func TestTenantFromNotDeclared(t *testing.T) {
	// 背景 ctx 未声明租户
	if _, _, ok := TenantFrom(context.Background()); ok {
		t.Error("未声明的 ctx 不应命中")
	}
	// nil ctx 不应 panic
	if _, _, ok := TenantFrom(nil); ok {
		t.Error("nil ctx 不应命中")
	}
	// 声明了但租户为空，视为未声明（回退全局）
	if _, _, ok := TenantFrom(context.WithValue(context.Background(), tenantKey{}, Tenant{})); ok {
		t.Error("空租户不应命中")
	}
	// 声明了但只有 UserCode 没有 TenantID，同样视为未声明
	if _, _, ok := TenantFrom(context.WithValue(context.Background(), tenantKey{}, Tenant{UserCode: "userA"})); ok {
		t.Error("仅 UserCode 无 TenantID 不应命中")
	}
}

func TestWithTenantOverride(t *testing.T) {
	ctx := WithTenant(WithTenant(context.Background(), "tenantA", "userA"), "tenantB", "userB")
	tenantID, userCode, ok := TenantFrom(ctx)
	if !ok || tenantID != "tenantB" || userCode != "userB" {
		t.Fatalf("重复声明应取最内层：tenant=%q user=%q ok=%v", tenantID, userCode, ok)
	}
}
