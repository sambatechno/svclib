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
//   - Creates a child span linked to the parent (innermost span in the context)
//   - Runs the operation on a private clone of the hub, so the caller's hub scope
//     is never repointed at this operation's span
//   - Sets span status based on error (OK or InternalError)
//   - Captures errors to Sentry if error pointer is provided
//
// Returns the new context and a finish function that accepts an optional error pointer.
func StartSpan(ctx context.Context, spanName string) (context.Context, func(*error)) {
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		// No Sentry context, return noop
		return ctx, func(*error) {}
	}

	// Always work on a private clone. ConfigureScope below sets the child span
	// on the scope, and doing that on the caller's hub would repoint whatever
	// else shares it — the handler still running on the request hub, a panic
	// recovery, a plain hub.CaptureException — at this operation's span.
	hub = hub.Clone()

	// Detect if we're in a goroutine context (context.Background means the
	// caller deliberately detached from the request)
	isGoroutine := ctx == context.Background()

	// Get parent span from context (innermost-wins across both span keys)
	parentSpan := SpanFromContext(ctx)

	// Base context for the operation, carrying the private hub
	base := ctx
	if isGoroutine {
		base = context.Background()
		// Copy tenant ID if present
		if tenantID, ok := GetTenantID(ctx); ok {
			base = WithTenantID(base, tenantID)
		}
	}
	base = sentry.SetHubOnContext(base, hub)

	// Create the child span — always from a context that carries the CLONE.
	// sentry.StartSpan repoints the scope of whatever hub is on the context it
	// is given (hub.Scope().SetSpan), and StartChild starts from the parent's
	// stored context, which carries the caller's hub: creating the child there
	// would silently redirect the request hub at this operation's span.
	var childSpan *sentry.Span
	if parentSpan != nil {
		childSpan = sentry.StartSpan(sentry.SetHubOnContext(parentSpan.Context(), hub), spanName)
	} else {
		childSpan = sentry.StartSpan(base, spanName)
	}
	childSpan.Description = spanName

	newCtx := context.WithValue(base, grpcSpanContextKey{}, childSpan)
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
