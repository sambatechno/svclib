package svclib

import (
	"net/http"

	"github.com/getsentry/sentry-go"
)

// TenantTaggingMiddleware adds tenant and request context tags to the Sentry scope.
// It also propagates trace context to downstream gRPC calls via headers.
//
// This middleware reads the tenant ID from the context using TenantContextKey.
// Ensure the tenant ID is added to the context before this middleware runs.
//
// Example usage:
//
//	// In your middleware chain, after setting tenant in context:
//	handler := sentryHandler.Handle(
//	    svclib.TenantTaggingMiddleware(yourHandler),
//	)
func TenantTaggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub := sentry.GetHubFromContext(r.Context())
		if hub != nil {
			hub.Scope().SetTag("http.method", r.Method)
			hub.Scope().SetTag("http.route", r.URL.Path)

			// Extract tenant_id from context (set by commonServiceMiddleware)
			if tenantID, ok := GetTenantID(r.Context()); ok && tenantID != "" {
				hub.Scope().SetTag("tenant.id", tenantID)
			}

			// Extract subdomain from header
			if subDomain := r.Header.Get("x-sub-domain"); subDomain != "" {
				hub.Scope().SetTag("tenant.subdomain", subDomain)
			}

			// Propagate Sentry trace context to gRPC metadata for downstream calls
			span := sentry.SpanFromContext(r.Context())
			if span != nil {
				// Get trace headers from the span
				traceParent := span.ToSentryTrace()

				// Add trace context to request headers
				// The grpc-gateway will forward these as metadata
				r.Header.Set("sentry-trace", traceParent)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// TenantTaggingMiddlewareWithExtractor allows custom tenant extraction logic.
// Use this variant when you need special handling for tenant extraction.
//
// Example usage:
//
//	middleware := svclib.TenantTaggingMiddlewareWithExtractor(
//	    func(r *http.Request) (tenantID, subdomain string) {
//	        // Custom extraction logic
//	        return extractTenantFromHeader(r), r.Header.Get("x-sub-domain")
//	    },
//	)
//	handler := sentryHandler.Handle(middleware(yourHandler))
func TenantTaggingMiddlewareWithExtractor(
	extractTenant func(*http.Request) (tenantID, subdomain string),
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hub := sentry.GetHubFromContext(r.Context())
			if hub != nil {
				hub.Scope().SetTag("http.method", r.Method)
				hub.Scope().SetTag("http.route", r.URL.Path)

				// Use custom extractor
				tenantID, subdomain := extractTenant(r)
				if tenantID != "" {
					hub.Scope().SetTag("tenant.id", tenantID)
				}
				if subdomain != "" {
					hub.Scope().SetTag("tenant.subdomain", subdomain)
				}

				// Propagate Sentry trace context to gRPC metadata for downstream calls
				span := sentry.SpanFromContext(r.Context())
				if span != nil {
					traceParent := span.ToSentryTrace()
					r.Header.Set("sentry-trace", traceParent)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

