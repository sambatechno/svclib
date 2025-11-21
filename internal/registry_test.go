package internal

import (
	"testing"

	"github.com/getsentry/sentry-go"
)

func TestSpanRegistry_StoreAndLoad(t *testing.T) {
	traceID := "test-trace-123"
	span := &sentry.Span{}

	// Store span
	StoreSpan(traceID, span)

	// Load span
	retrieved, ok := LoadSpan(traceID)
	if !ok {
		t.Fatal("Expected span to be found")
	}

	if retrieved != span {
		t.Error("Expected to retrieve the same span instance")
	}

	// Cleanup
	DeleteSpan(traceID)
}

func TestSpanRegistry_LoadNonExistent(t *testing.T) {
	traceID := "non-existent-trace"

	// Try to load non-existent span
	_, ok := LoadSpan(traceID)
	if ok {
		t.Error("Expected span to not be found")
	}
}

func TestSpanRegistry_Delete(t *testing.T) {
	traceID := "test-trace-delete"
	span := &sentry.Span{}

	// Store and delete
	StoreSpan(traceID, span)
	DeleteSpan(traceID)

	// Verify it's gone
	_, ok := LoadSpan(traceID)
	if ok {
		t.Error("Expected span to be deleted")
	}
}

func TestSpanRegistry_MultipleSpans(t *testing.T) {
	span1 := &sentry.Span{}
	span2 := &sentry.Span{}

	// Store multiple spans
	StoreSpan("trace-1", span1)
	StoreSpan("trace-2", span2)

	// Load both
	retrieved1, ok1 := LoadSpan("trace-1")
	retrieved2, ok2 := LoadSpan("trace-2")

	if !ok1 || !ok2 {
		t.Fatal("Expected both spans to be found")
	}

	if retrieved1 != span1 || retrieved2 != span2 {
		t.Error("Expected to retrieve correct span instances")
	}

	// Cleanup
	DeleteSpan("trace-1")
	DeleteSpan("trace-2")
}

func TestSpanRegistry_Overwrite(t *testing.T) {
	traceID := "test-trace-overwrite"
	span1 := &sentry.Span{}
	span2 := &sentry.Span{}

	// Store first span
	StoreSpan(traceID, span1)

	// Overwrite with second span
	StoreSpan(traceID, span2)

	// Load and verify it's the second span
	retrieved, ok := LoadSpan(traceID)
	if !ok {
		t.Fatal("Expected span to be found")
	}

	if retrieved != span2 {
		t.Error("Expected to retrieve the overwritten span")
	}

	// Cleanup
	DeleteSpan(traceID)
}

func TestSpanRegistry_ConcurrentAccess(t *testing.T) {
	done := make(chan bool, 3)

	// Test concurrent store/load operations
	for i := 0; i < 3; i++ {
		go func(id int) {
			traceID := "concurrent-trace"
			span := &sentry.Span{}

			StoreSpan(traceID, span)
			LoadSpan(traceID)
			DeleteSpan(traceID)

			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}
}

func TestSpanRegistry_EmptyTraceID(t *testing.T) {
	span := &sentry.Span{}

	// Store with empty trace ID
	StoreSpan("", span)

	// Load with empty trace ID
	retrieved, ok := LoadSpan("")
	if !ok {
		t.Error("Expected to be able to store/retrieve with empty trace ID")
	}

	if retrieved != span {
		t.Error("Expected to retrieve the same span")
	}

	// Cleanup
	DeleteSpan("")
}

