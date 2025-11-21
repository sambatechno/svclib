package svclib

import "context"

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
