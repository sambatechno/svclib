package svclib

import (
	"context"
	"log"

	"github.com/getsentry/sentry-go"
)

// LogError logs an error to stdout and Sentry, automatically linking it to the current trace.
//
// The function intelligently handles different contexts:
//   - In handlers: Links error to the current span (http.server or grpc.server)
//   - In simple goroutines: Links error to parent trace via trace_id tag
//   - With StartSpan: Links error to the operation's child span
//
// Usage is the same everywhere - just pass the context:
//
//	svclib.LogError(ctx, "Failed to process", err)
//
// No special handling needed for goroutines - it just works!
func LogError(ctx context.Context, label string, err error) {
	if err == nil {
		return
	}

	log.Printf("%s: %v", label, err)

	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		sentry.CaptureException(err)
		return
	}

	// Try to get the span from context (this works for handlers and StartSpan)
	if span, ok := ctx.Value(grpcSpanContextKey{}).(*sentry.Span); ok {
		hub.WithScope(func(scope *sentry.Scope) {
			scope.SetSpan(span)
			scope.SetContext("error_context", map[string]interface{}{
				"label": label,
			})
			hub.CaptureException(err)
		})
		return
	}

	// No span object, but check if we have a trace ID in context
	// This happens when LogError is called from a simple goroutine: go funcName(ctx)
	if traceID, ok := ctx.Value(sentryTraceContextKey{}).(string); ok && traceID != "" {
		hub.WithScope(func(scope *sentry.Scope) {
			scope.SetContext("trace", map[string]interface{}{
				"trace_id": traceID,
			})
			scope.SetTag("trace_id", traceID)
			scope.SetContext("error_context", map[string]interface{}{
				"label": label,
			})
			hub.CaptureException(err)
		})
		return
	}

	// No span or trace ID in context
	// Just capture with the hub - it may still have transaction context
	hub.CaptureException(err)
}

