package svclib

import (
	"context"
	"errors"
	"testing"
)

func TestLogError_NilError(t *testing.T) {
	ctx := context.Background()

	// Should not panic with nil error
	LogError(ctx, "test", nil)
}

func TestLogError_WithError(t *testing.T) {
	ctx := context.Background()
	err := errors.New("test error")

	// Should not panic
	LogError(ctx, "Test error", err)
}

func TestLogError_EmptyContext(t *testing.T) {
	ctx := context.Background()
	err := errors.New("test error")

	// Should handle empty context gracefully
	LogError(ctx, "Error with empty context", err)
}

func TestLogError_WithTenantContext(t *testing.T) {
	ctx := WithTenantID(context.Background(), "test-tenant")
	err := errors.New("tenant-specific error")

	// Should not panic with tenant context
	LogError(ctx, "Error with tenant", err)
}

func TestLogError_EmptyLabel(t *testing.T) {
	ctx := context.Background()
	err := errors.New("test error")

	// Should handle empty label
	LogError(ctx, "", err)
}

func TestLogError_ConcurrentCalls(t *testing.T) {
	ctx := context.Background()
	err := errors.New("concurrent error")

	// Test concurrent calls don't cause issues
	done := make(chan bool, 3)

	for i := 0; i < 3; i++ {
		go func(id int) {
			LogError(ctx, "Concurrent test", err)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}
}

