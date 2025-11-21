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

//Basic example showing svclib integration for error tracking and distributed tracing.
//
// This example demonstrates:
// - Sentry initialization
// - gRPC interceptors (client + server)
// - HTTP middleware for tenant tagging
// - Context-aware error logging
//
// Run with: go run main.go [optional-mock-tenant]

func main() {
	// 1. Initialize Sentry
	sentryHandler, err := svclib.Init(svclib.Config{
		DSN:              "https://your-dsn@sentry.io/project",
		Environment:      "development",
		TracesSampleRate: 1.0, // 100% for development
		EnableTracing:    true,
		Repanic:          true,
	})
	if err != nil {
		log.Fatalf("Sentry init failed: %v", err)
	}
	defer sentry.Flush(2 * time.Second)

	// 2. Parse mock tenant from command line (for testing)
	mockedTenant := svclib.ParseMockTenant()
	log.Printf("Starting service (mock tenant: %s)\n", mockedTenant)

	// 3. Setup gRPC server with Sentry interceptor
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			svclib.UnaryServerInterceptor(),
		),
	)

	// Register your gRPC services here
	// pb.RegisterYourServiceServer(grpcServer, &YourService{})

	lis, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	// Start gRPC server
	go func() {
		log.Println("gRPC server listening on :8080")
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	// 4. Setup gRPC client with Sentry interceptor (for grpc-gateway)
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

	// 5. Setup grpc-gateway mux with standard header matcher
	grpcMux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(svclib.DefaultHeaderMatcher()),
	)

	// Register your gateway handlers here
	// pb.RegisterYourServiceHandler(context.Background(), grpcMux, conn)

	// 6. Setup HTTP handlers with middleware chain
	mux := http.NewServeMux()
	mux.Handle("/", grpcMux)

	// Add a test endpoint
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		// Set tenant ID in context (normally done by auth middleware)
		ctx := svclib.WithTenantID(r.Context(), "tenant-123")

		// Simulate some work
		err := processRequest(ctx)
		if err != nil {
			// Error is automatically linked to the current trace
			svclib.LogError(ctx, "Failed to process request", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Request processed successfully")
	})

	// Wrap with Sentry middleware chain
	handler := sentryHandler.Handle(
		svclib.TenantTaggingMiddleware(mux),
	)

	// 7. Start HTTP server
	log.Println("HTTP server listening on :8081")
	if err := http.ListenAndServe(":8081", handler); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}

func processRequest(ctx context.Context) error {
	// Simulate processing
	log.Println("Processing request...")

	// Get tenant from context
	tenantID, ok := svclib.GetTenantID(ctx)
	if !ok {
		return fmt.Errorf("tenant ID not found in context")
	}

	log.Printf("Processing for tenant: %s\n", tenantID)

	// Simulate potential error
	// Uncomment to test error tracking:
	// return fmt.Errorf("simulated processing error")

	return nil
}

