package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/sambatechno/svclib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Example with custom tenant extraction.
//
// This example demonstrates:
// - Custom tenant extraction using TenantTaggingMiddlewareWithExtractor
// - Useful when migrating from existing tenant context patterns
// - Shows both standard and custom approaches
//
// Run with: go run main.go [optional-mock-tenant]

// CustomTenantContextKey represents a service-specific context key
// (e.g., from database package in user-service)
type CustomTenantContextKey struct{}

func main() {
	// Initialize Sentry
	sentryHandler, err := svclib.Init(svclib.Config{
		DSN:              "https://your-dsn@sentry.io/project",
		Environment:      "development",
		TracesSampleRate: 1.0,
		EnableTracing:    true,
		Repanic:          true,
	})
	if err != nil {
		log.Fatalf("Sentry init failed: %v", err)
	}
	defer sentry.Flush(2 * time.Second)

	mockedTenant := svclib.ParseMockTenant()
	log.Printf("Starting service (mock tenant: %s)\n", mockedTenant)

	// Setup gRPC server
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			svclib.UnaryServerInterceptor(),
		),
	)

	lis, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	go func() {
		log.Println("gRPC server listening on :8080")
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	// Setup gRPC client
	conn, err := grpc.NewClient(
		"localhost:8080",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(
			svclib.UnaryClientInterceptor(),
		),
	)
	if err != nil {
		log.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	grpcMux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(svclib.DefaultHeaderMatcher()),
	)

	mux := http.NewServeMux()
	mux.Handle("/", grpcMux)

	// Example 1: Using standard svclib.TenantContextKey
	mux.HandleFunc("/standard", func(w http.ResponseWriter, r *http.Request) {
		// Use standard library context key
		ctx := svclib.WithTenantID(r.Context(), "tenant-standard")

		log.Printf("Standard approach - Tenant: %v\n", getTenantFromContext(ctx))

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Standard tenant handling")
	})

	// Example 2: Using custom context key (for migration scenarios)
	mux.HandleFunc("/custom", func(w http.ResponseWriter, r *http.Request) {
		// Service uses its own context key (e.g., database.TenantContextKey)
		ctx := context.WithValue(r.Context(), CustomTenantContextKey{}, "tenant-custom")

		log.Printf("Custom approach - Tenant: %v\n", ctx.Value(CustomTenantContextKey{}).(string))

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Custom tenant handling")
	})

	// Setup middleware with custom extractor
	// This allows using service-specific context keys while still getting Sentry tagging
	customMiddleware := svclib.TenantTaggingMiddlewareWithExtractor(
		func(r *http.Request) (tenantID, subdomain string) {
			// Try to extract from custom context key first
			if id, ok := r.Context().Value(CustomTenantContextKey{}).(string); ok {
				return id, r.Header.Get("x-sub-domain")
			}

			// Fallback to standard library key
			if id, ok := svclib.GetTenantID(r.Context()); ok {
				return id, r.Header.Get("x-sub-domain")
			}

			// Fallback to headers
			return r.Header.Get("x-tenant-id"), r.Header.Get("x-sub-domain")
		},
	)

	handler := sentryHandler.Handle(customMiddleware(mux))

	log.Println("HTTP server listening on :8081")
	log.Println("Test endpoints:")
	log.Println("  - http://localhost:8081/standard (uses svclib.TenantContextKey)")
	log.Println("  - http://localhost:8081/custom (uses CustomTenantContextKey)")
	if err := http.ListenAndServe(":8081", handler); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}

func getTenantFromContext(ctx context.Context) string {
	tenantID, ok := svclib.GetTenantID(ctx)
	if !ok {
		return ""
	}
	return tenantID
}

