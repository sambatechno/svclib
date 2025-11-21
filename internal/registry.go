package internal

import (
	"sync"

	"github.com/getsentry/sentry-go"
)

// SpanRegistry stores spans keyed by trace ID for internal gRPC calls.
// This is needed because Go's context.Context doesn't survive gRPC network boundaries.
// The client interceptor stores spans here, and the server interceptor retrieves them
// to create proper parent-child span relationships.
var SpanRegistry = &sync.Map{}

// StoreSpan stores a span in the registry with the given trace ID.
func StoreSpan(traceID string, span *sentry.Span) {
	SpanRegistry.Store(traceID, span)
}

// LoadSpan retrieves a span from the registry by trace ID.
func LoadSpan(traceID string) (*sentry.Span, bool) {
	if val, ok := SpanRegistry.Load(traceID); ok {
		return val.(*sentry.Span), true
	}
	return nil, false
}

// DeleteSpan removes a span from the registry.
func DeleteSpan(traceID string) {
	SpanRegistry.Delete(traceID)
}

