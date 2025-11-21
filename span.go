package svclib

import (
	"context"

	"github.com/getsentry/sentry-go"
)

// StartSpan creates a child span for the given operation.
// This is a unified helper that works for both synchronous code (service methods)
// and asynchronous code (goroutines).
//
// For service methods (synchronous):
//
//	func (s *Server) MyMethod(ctx context.Context, req *pb.Request) (resp *pb.Response, err error) {
//	    ctx, finish := svclib.StartSpan(ctx, "ServiceName.MyMethod")
//	    defer finish(&err)
//	    // ... your code using the returned ctx ...
//	    return &pb.Response{}, nil
//	}
//
// For goroutines (asynchronous):
//
//	go func() {
//	    ctx, finish := svclib.StartSpan(ctx, "background.processing")
//	    defer finish(nil) // or defer finish(&err) if you want to track errors
//	    // ... your async work ...
//	}()
//
// The function automatically:
// - Creates a child span linked to the parent
// - Handles hub cloning for goroutines (detects context.Background())
// - Sets span status based on error (OK or InternalError)
// - Captures errors to Sentry if error pointer is provided
//
// Returns the new context and a finish function that accepts an optional error pointer.
func StartSpan(ctx context.Context, spanName string) (context.Context, func(*error)) {
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		// No Sentry context, return noop
		return ctx, func(*error) {}
	}

	// Detect if we're in a goroutine context (context.Background or no existing span chain)
	// For goroutines, we need to clone the hub to avoid race conditions
	isGoroutine := false
	if ctx == context.Background() {
		isGoroutine = true
	}

	if isGoroutine {
		hub = hub.Clone()
	}

	// Get parent span from context
	var parentSpan *sentry.Span
	if span, ok := ctx.Value(grpcSpanContextKey{}).(*sentry.Span); ok {
		parentSpan = span
	} else {
		parentSpan = sentry.SpanFromContext(ctx)
	}

	// Create child span
	var childSpan *sentry.Span
	if parentSpan != nil {
		childSpan = parentSpan.StartChild(spanName)
	} else {
		childSpan = sentry.StartSpan(ctx, spanName)
	}
	childSpan.Description = spanName

	// Create new context with hub and span
	newCtx := ctx
	if isGoroutine {
		newCtx = context.Background()
		// Copy tenant ID if present
		if tenantID, ok := GetTenantID(ctx); ok {
			newCtx = WithTenantID(newCtx, tenantID)
		}
	}

	newCtx = sentry.SetHubOnContext(newCtx, hub)
	newCtx = context.WithValue(newCtx, grpcSpanContextKey{}, childSpan)
	newCtx = context.WithValue(newCtx, sentryTraceContextKey{}, childSpan.TraceID.String())

	// Configure hub scope to use this span
	hub.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetSpan(childSpan)
	})

	// Return finish function that accepts optional error pointer
	finish := func(errPtr *error) {
		if errPtr != nil && *errPtr != nil {
			childSpan.Status = sentry.SpanStatusInternalError
			// Capture the error linked to this span
			hub.WithScope(func(scope *sentry.Scope) {
				scope.SetSpan(childSpan)
				scope.SetContext("operation", map[string]interface{}{
					"name": spanName,
				})
				hub.CaptureException(*errPtr)
			})
		} else {
			childSpan.Status = sentry.SpanStatusOK
		}
		childSpan.Finish()
	}

	return newCtx, finish
}

