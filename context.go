package svclib

import (
	"context"

	"github.com/getsentry/sentry-go"
)

// TenantContextKey is the standard context key for storing tenant ID across all services.
// This key should be used consistently for both database queries and Sentry tagging.
//
// Example usage:
//
//	ctx := svclib.WithTenantID(r.Context(), tenantID)
//	// Later in code:
//	tenantID, ok := svclib.GetTenantID(ctx)
type TenantContextKey struct{}

// WithTenantID adds a tenant ID to the context.
// This is the standard way to store tenant information that can be used
// for both database queries and Sentry tagging.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, TenantContextKey{}, tenantID)
}

// GetTenantID retrieves the tenant ID from the context.
// Returns the tenant ID and true if found, empty string and false otherwise.
func GetTenantID(ctx context.Context) (string, bool) {
	tenantID, ok := ctx.Value(TenantContextKey{}).(string)
	return tenantID, ok
}

// Internal context keys (not exported)
type (
	// grpcSpanContextKey stores the current gRPC span in the handler context.
	grpcSpanContextKey struct{}

	// sentryTraceContextKey stores the trace ID string in the context.
	sentryTraceContextKey struct{}
)

// SpanFromContext returns the Sentry span attached to ctx, or nil when there is
// none. A context can carry a span under two keys — the one this package's
// interceptor/StartSpan store, and Sentry's own (set by sentry.StartSpan /
// span.Context()) — and the innermost of the two wins:
//
//   - only one present: that one
//   - one is a direct child of the other: the child
//   - both on the same trace: Sentry's own key (each sentry.StartSpan re-stores
//     it, so it tracks the innermost span)
//   - unrelated traces: the interceptor's span, so a detached transaction
//     someone started on the side never captures the request's logs
func SpanFromContext(ctx context.Context) *sentry.Span {
	if ctx == nil {
		return nil
	}
	grpcSpan, _ := ctx.Value(grpcSpanContextKey{}).(*sentry.Span)
	sentrySpan := sentry.SpanFromContext(ctx)
	switch {
	case sentrySpan == nil:
		return grpcSpan
	case grpcSpan == nil || grpcSpan == sentrySpan:
		return sentrySpan
	case grpcSpan.ParentSpanID == sentrySpan.SpanID:
		return grpcSpan
	case grpcSpan.TraceID == sentrySpan.TraceID:
		return sentrySpan
	default:
		return grpcSpan
	}
}

// TraceIDFromContext returns the distributed trace ID for ctx, or "" when the
// context carries no trace. It reads the current span first and falls back to
// the trace ID string StartSpan / UnaryServerInterceptor store on the context,
// which is what survives into a goroutine that only kept the context.
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if span := SpanFromContext(ctx); span != nil {
		return span.TraceID.String()
	}
	if traceID, ok := ctx.Value(sentryTraceContextKey{}).(string); ok {
		return traceID
	}
	return ""
}
