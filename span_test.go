package svclib

import (
	"context"
	"errors"
	"testing"
)

func TestStartSpan_BasicUsage(t *testing.T) {
	ctx := context.Background()

	// Start a span
	newCtx, finish := StartSpan(ctx, "test.operation")

	// Verify we got a new context
	if newCtx == nil {
		t.Fatal("Expected non-nil context")
	}

	// Verify finish function exists
	if finish == nil {
		t.Fatal("Expected non-nil finish function")
	}

	// Call finish (should not panic)
	finish(nil)
}

func TestStartSpan_WithError(t *testing.T) {
	ctx := context.Background()

	newCtx, finish := StartSpan(ctx, "test.operation")

	testErr := errors.New("test error")

	// Finish with error (should not panic)
	finish(&testErr)

	if newCtx == nil {
		t.Error("Expected non-nil context")
	}
}

func TestStartSpan_WithNilErrorPointer(t *testing.T) {
	ctx := context.Background()

	_, finish := StartSpan(ctx, "test.operation")

	// Should not panic with nil pointer
	finish(nil)
}

func TestStartSpan_MultipleSpans(t *testing.T) {
	ctx := context.Background()

	// Create first span
	ctx1, finish1 := StartSpan(ctx, "operation.1")
	defer finish1(nil)

	// Create nested span
	ctx2, finish2 := StartSpan(ctx1, "operation.2")
	defer finish2(nil)

	// Create another nested span
	ctx3, finish3 := StartSpan(ctx2, "operation.3")
	defer finish3(nil)

	// Verify contexts are returned (they may be the same without Sentry initialized)
	if ctx1 == nil || ctx2 == nil || ctx3 == nil {
		t.Error("Expected non-nil contexts")
	}

	// Verify finish functions work
	if finish1 == nil || finish2 == nil || finish3 == nil {
		t.Error("Expected non-nil finish functions")
	}
}

func TestStartSpan_EmptySpanName(t *testing.T) {
	ctx := context.Background()

	// Should work with empty span name
	_, finish := StartSpan(ctx, "")

	// Should not panic
	finish(nil)
}

func TestStartSpan_WithTenantContext(t *testing.T) {
	ctx := WithTenantID(context.Background(), "test-tenant")

	newCtx, finish := StartSpan(ctx, "test.operation")
	defer finish(nil)

	// Verify tenant ID is preserved
	tenantID, ok := GetTenantID(newCtx)
	if !ok {
		t.Error("Expected tenant ID to be preserved in new context")
	}

	if tenantID != "test-tenant" {
		t.Errorf("Expected tenant ID 'test-tenant', got %q", tenantID)
	}
}

func TestStartSpan_ConcurrentCalls(t *testing.T) {
	ctx := context.Background()

	done := make(chan bool, 3)

	// Test concurrent span creation
	for i := 0; i < 3; i++ {
		go func() {
			_, finish := StartSpan(ctx, "concurrent.operation")
			defer finish(nil)
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}
}

func TestStartSpan_DeferredFinish(t *testing.T) {
	ctx := context.Background()

	err := func() error {
		_, finish := StartSpan(ctx, "test.operation")
		defer finish(nil)

		// Simulate some work
		return nil
	}()

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

func TestStartSpan_DeferredFinishWithError(t *testing.T) {
	ctx := context.Background()

	err := func() (err error) {
		_, finish := StartSpan(ctx, "test.operation")
		defer finish(&err)

		// Simulate error
		return errors.New("simulated error")
	}()

	if err == nil {
		t.Error("Expected error to be returned")
	}
}

