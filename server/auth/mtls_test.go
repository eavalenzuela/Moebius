package auth

import (
	"context"
	"testing"
)

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, ContextKeyAgentID, "dev_abc123")
	ctx = context.WithValue(ctx, ContextKeyTenantID, "ten_xyz789")

	if got := AgentIDFromContext(ctx); got != "dev_abc123" {
		t.Errorf("AgentIDFromContext = %q, want %q", got, "dev_abc123")
	}
	if got := TenantIDFromContext(ctx); got != "ten_xyz789" {
		t.Errorf("TenantIDFromContext = %q, want %q", got, "ten_xyz789")
	}
}

func TestContextHelpers_Empty(t *testing.T) {
	ctx := context.Background()
	if got := AgentIDFromContext(ctx); got != "" {
		t.Errorf("AgentIDFromContext = %q, want empty", got)
	}
	if got := TenantIDFromContext(ctx); got != "" {
		t.Errorf("TenantIDFromContext = %q, want empty", got)
	}
}
