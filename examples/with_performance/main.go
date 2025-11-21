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

// Example with performance tracking using StartSpan.
//
// This example demonstrates:
// - All features from basic example
// - Granular performance tracking with StartSpan
// - Creating child spans for detailed performance insights
// - Error tracking linked to specific spans
//
// Run with: go run main.go [optional-mock-tenant]

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

	// Test endpoint with performance tracking
	mux.HandleFunc("/order", func(w http.ResponseWriter, r *http.Request) {
		ctx := svclib.WithTenantID(r.Context(), "tenant-123")

		// Process order with detailed performance tracking
		err := processOrder(ctx, "order-456")
		if err != nil {
			svclib.LogError(ctx, "Failed to process order", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Order processed successfully")
	})

	handler := sentryHandler.Handle(
		svclib.TenantTaggingMiddleware(mux),
	)

	log.Println("HTTP server listening on :8081")
	if err := http.ListenAndServe(":8081", handler); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}

// processOrder demonstrates using StartSpan for performance tracking
func processOrder(ctx context.Context, orderID string) (err error) {
	// Create a span for the entire operation
	ctx, finish := svclib.StartSpan(ctx, "OrderService.ProcessOrder")
	defer finish(&err)

	log.Printf("Processing order: %s\n", orderID)

	// Validate order (sub-span)
	if err := validateOrder(ctx, orderID); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Check inventory (sub-span)
	if err := checkInventory(ctx, orderID); err != nil {
		return fmt.Errorf("inventory check failed: %w", err)
	}

	// Save to database (sub-span)
	if err := saveOrder(ctx, orderID); err != nil {
		return fmt.Errorf("save failed: %w", err)
	}

	// Send notification (async with span)
	go sendNotification(ctx, orderID)

	return nil
}

func validateOrder(ctx context.Context, orderID string) (err error) {
	ctx, finish := svclib.StartSpan(ctx, "validate_order")
	defer finish(&err)

	log.Printf("Validating order: %s\n", orderID)
	time.Sleep(50 * time.Millisecond) // Simulate work

	// Uncomment to test error tracking:
	// return fmt.Errorf("invalid order data")

	return nil
}

func checkInventory(ctx context.Context, orderID string) (err error) {
	ctx, finish := svclib.StartSpan(ctx, "check_inventory")
	defer finish(&err)

	log.Printf("Checking inventory for order: %s\n", orderID)
	time.Sleep(100 * time.Millisecond) // Simulate work

	return nil
}

func saveOrder(ctx context.Context, orderID string) (err error) {
	ctx, finish := svclib.StartSpan(ctx, "save_order_to_db")
	defer finish(&err)

	log.Printf("Saving order to database: %s\n", orderID)
	time.Sleep(75 * time.Millisecond) // Simulate work

	return nil
}

// sendNotification demonstrates async operation with span
func sendNotification(ctx context.Context, orderID string) {
	// Create span for async operation
	ctx, finish := svclib.StartSpan(ctx, "background.send_notification")
	defer finish(nil)

	log.Printf("Sending notification for order: %s\n", orderID)
	time.Sleep(200 * time.Millisecond) // Simulate work

	// Errors in goroutines are automatically linked to parent trace
	// svclib.LogError(ctx, "Failed to send notification", err)
}

