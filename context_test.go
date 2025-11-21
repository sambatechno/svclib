package svclib

import (
	"context"
	"testing"
)

func TestWithTenantID(t *testing.T) {
	ctx := context.Background()
	tenantID := "test-tenant-123"

	// Add tenant ID to context
	ctx = WithTenantID(ctx, tenantID)

	// Retrieve it
	retrieved, ok := GetTenantID(ctx)
	if !ok {
		t.Fatal("Expected tenant ID to be present in context")
	}

	if retrieved != tenantID {
		t.Errorf("Expected tenant ID %q, got %q", tenantID, retrieved)
	}
}

func TestGetTenantID_NotPresent(t *testing.T) {
	ctx := context.Background()

	// Try to get tenant ID from empty context
	_, ok := GetTenantID(ctx)
	if ok {
		t.Error("Expected tenant ID to be absent from context")
	}
}

func TestGetTenantID_EmptyString(t *testing.T) {
	ctx := context.Background()

	// Add empty string as tenant ID
	ctx = WithTenantID(ctx, "")

	retrieved, ok := GetTenantID(ctx)
	if !ok {
		t.Fatal("Expected tenant ID to be present in context")
	}

	if retrieved != "" {
		t.Errorf("Expected empty string, got %q", retrieved)
	}
}

func TestTenantContextKey_TypeSafety(t *testing.T) {
	ctx := context.Background()

	// Use the context key directly
	ctx = context.WithValue(ctx, TenantContextKey{}, "direct-tenant")

	// Should be retrievable with GetTenantID
	tenantID, ok := GetTenantID(ctx)
	if !ok {
		t.Fatal("Expected tenant ID to be present")
	}

	if tenantID != "direct-tenant" {
		t.Errorf("Expected %q, got %q", "direct-tenant", tenantID)
	}
}

func TestWithTenantID_Chaining(t *testing.T) {
	ctx := context.Background()

	// Add first tenant
	ctx1 := WithTenantID(ctx, "tenant-1")
	tenant1, _ := GetTenantID(ctx1)

	// Replace with second tenant
	ctx2 := WithTenantID(ctx1, "tenant-2")
	tenant2, _ := GetTenantID(ctx2)

	// Verify they're different
	if tenant1 == tenant2 {
		t.Error("Expected different tenant IDs in chained contexts")
	}

	if tenant2 != "tenant-2" {
		t.Errorf("Expected tenant-2, got %q", tenant2)
	}
}

func TestContextKey_Isolation(t *testing.T) {
	ctx := context.Background()

	// Add tenant ID
	ctx = WithTenantID(ctx, "test-tenant")

	// Verify internal keys are not accessible
	if ctx.Value(grpcSpanContextKey{}) != nil {
		t.Error("Internal grpcSpanContextKey should not be set by WithTenantID")
	}

	if ctx.Value(sentryTraceContextKey{}) != nil {
		t.Error("Internal sentryTraceContextKey should not be set by WithTenantID")
	}
}
