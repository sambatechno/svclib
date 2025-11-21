# Sentry Integration Documentation

## Part 1: Distributed Tracing for HTTP + gRPC Gateway Architecture

### Overview

This document details the implementation of proper Sentry distributed tracing for a service architecture that uses:

- HTTP endpoints exposed via `grpc-gateway`
- Internal gRPC service handlers
- Context-based tenant/request information
- Background goroutines (error tracking)

**Scope of Part 1**:

- ✅ Fix fragmented traces (HTTP and gRPC in one trace)
- ✅ Propagate tenant context across boundaries
- ✅ Link errors to the correct trace (including goroutines)
- ✅ Basic 2-span visibility (HTTP + gRPC)
- ❌ Detailed performance spans (see Part 2)

The goal is to have **one unified trace per request** that includes the HTTP entry point and internal gRPC calls, with proper parent-child relationships and automatic error linking.

---

## Problem Statement

### Initial Issues

1. **Fragmented Traces**: Sentry was creating separate traces for HTTP requests and gRPC calls, making it impossible to see the full request flow
2. **Missing Context**: Errors in gRPC handlers lacked tenant information (tenant_id, subdomain)
3. **Generic Error Messages**: gRPC errors showed as empty messages or "internal_error" without details
4. **Context Loss**: The Sentry Hub and Span objects weren't propagating from HTTP to gRPC handlers
5. **Goroutine Isolation**: Errors in background goroutines weren't linked to the originating request trace

### Root Cause

Go's `context.Context` values **do not automatically propagate across gRPC network boundaries**, even for localhost calls made by `grpc-gateway`. The HTTP request's Sentry context (Hub, Span) was being lost when crossing into the gRPC server handler.

---

## Architecture Overview

```
HTTP Request
    ↓
[sentryhttp.Handler] - Creates transaction
    ↓
[SentryTaggingMiddleware] - Adds HTTP tags
    ↓
[grpc-gateway] - Translates HTTP → gRPC
    ↓
[SentryUnaryClientInterceptor] - Stores span in registry, adds metadata
    ↓
[gRPC Client] - Makes internal gRPC call
    ↓
[gRPC Server] - Receives call
    ↓
[SentryUnaryServerInterceptor] - Retrieves parent span, creates child
    ↓
[Service Handler] - Processes request with full context
    ↓
[Goroutines] - Background tasks (optional span or auto-linked)
```

---

## Quick Start Guide

### If You Already Have Sentry Integration

If your service already has `initSentry()` or `SetupLogUtil()` and uses `sentryhttp.Handler`, you're halfway there! Follow this simplified path:

✅ **Keep your existing `initSentry()` function** - Just verify it has:

- `EnableTracing: true`
- `TracesSampleRate: <appropriate value>`

✅ **Add the 3 new files**:

- `utils/logging.go` (update existing or create)
- `utils/sentry_tags.go` (new)
- `utils/sentry_grpc.go` (new)

✅ **Update `main.go`**:

- Add `utils.SentryUnaryServerInterceptor()` to `grpc.NewServer`
- Add `utils.SentryUnaryClientInterceptor()` to gRPC client
- Insert `utils.SentryTaggingMiddleware` in HTTP middleware chain

✅ **Update all `LogError` calls** to pass context

**Estimated time**: 3-4 hours for small services, 6-8 hours for large ones.

---

### If Starting Fresh (No Sentry Yet)

Follow the complete implementation guide below.

---

## Implementation Guide

### 1. Update Dependencies

```bash
go get -u github.com/getsentry/sentry-go@latest
```

**Current version tested**: `v0.38.0`

**Important**: The `github.com/getsentry/sentry-go/grpc` package was removed from the main module. Do not attempt to import it. We implement custom interceptors instead.

---

### 2. Update `utils/logging.go`

**Purpose**: Automatically link errors to traces, even from goroutines.

```go
package utils

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
//	utils.LogError(ctx, "Failed to process", err)
//
// No special handling needed for goroutines - it just works!
func LogError(ctx context.Context, label string, err error) {
	if err == nil {
		return // Don't log nil errors
	}

	log.Printf("%s: %v", label, err)

	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		// No hub in context, use global client
		sentry.CaptureException(err)
		return
	}

	// Try to get the span from context (this works for handlers and StartSpan)
	if span, ok := ctx.Value(grpcSpanContextKey{}).(*sentry.Span); ok {
		// We have a span - link the error to it
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
		// We have a trace ID - add it to the error context so it appears in the same trace
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
```

**Key Changes**:

- Added `ctx context.Context` as first parameter
- Intelligently links errors to spans or traces based on context
- Works automatically in goroutines with zero setup
- Falls back gracefully when no Sentry context available

**Breaking Change**: All callsites must be updated to pass context as first argument.

---

### 3. Create `utils/sentry_tags.go`

**Purpose**: HTTP middleware to add tags and propagate trace context to downstream gRPC calls.

```go
package utils

import (
	"net/http"

	"github.com/getsentry/sentry-go"
)

// SentryTaggingMiddleware adds tenant and request context tags to the Sentry scope.
// It also propagates trace context to downstream gRPC calls via headers.
func SentryTaggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub := sentry.GetHubFromContext(r.Context())
		if hub != nil {
			hub.Scope().SetTag("http.method", r.Method)
			hub.Scope().SetTag("http.route", r.URL.Path)

			// Extract tenant_id from context (set by commonServiceMiddleware)
			if tenantID, ok := r.Context().Value(TenantContextKey{}).(string); ok && tenantID != "" {
				hub.Scope().SetTag("tenant.id", tenantID)
			}

			// Extract subdomain from header (set by commonServiceMiddleware)
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
```

**Key Points**:

- Extracts tenant information from context/headers
- Adds structured tags (`tenant.id`, `tenant.subdomain`, `http.method`, `http.route`)
- Adds `sentry-trace` header that `grpc-gateway` will forward as metadata
- Must be placed **after** `sentryhttp.Handler` in middleware chain

---

### 4. Create `utils/sentry_grpc.go`

**Purpose**: Custom gRPC interceptors to maintain trace continuity across the HTTP→gRPC boundary, with automatic goroutine support.

**See full implementation in the codebase** - Key highlights:

```go
// SentryUnaryClientInterceptor - Stores span in registry and adds metadata
func SentryUnaryClientInterceptor() grpc.UnaryClientInterceptor {
	// Stores active span in registry
	// Adds sentry-trace and sentry-trace-id to gRPC metadata
}

// SentryUnaryServerInterceptor - Creates child spans for gRPC calls
func SentryUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	// Retrieves parent span from registry
	// Creates child span using parentSpan.StartChild()
	// Adds span and trace_id to context for handlers and goroutines
	// Maps gRPC status codes to Sentry status codes
}
```

**Critical Details**:

1. **Span Registry**: A `sync.Map` stores active HTTP spans temporarily
2. **Client Interceptor**: Stores span in registry and adds `sentry-trace` + `sentry-trace-id` to metadata
3. **Server Interceptor**: Retrieves parent span, creates child using `parentSpan.StartChild()`
4. **Context Preservation**: Uses `sentry.SetHubOnContext(ctx, hub)` to preserve gRPC metadata
5. **Trace ID Propagation**: Stores trace ID in context for automatic error linking in goroutines

---

### 5. Update `main.go`

**Changes**:

1. Enable tracing in Sentry initialization
2. Register gRPC interceptors
3. Chain HTTP middleware correctly

```go
import (
	"project/utils"
	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func initSentry() *sentryhttp.Handler {
	env := "local"
	tracing := 1.0
	if strings.HasPrefix(utils.Config.Stage, "prod") {
		env = "production"
		tracing = 0.1
	} else if strings.HasPrefix(utils.Config.Stage, "dev") {
		env = "development"
	}

	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              "YOUR_DSN_HERE",
		TracesSampleRate: tracing,
		EnableTracing:    true, // IMPORTANT: Must be enabled
		Environment:      env,
	}); err != nil {
		log.Printf("Sentry initialization failed: %v\n", err)
	}

	return sentryhttp.New(sentryhttp.Options{
		Repanic: true,
	})
}

func main() {
	utils.ReadConfig()
	sentryHandler := initSentry()

	// ... (other setup)

	// Register gRPC server with Sentry interceptor
	gs := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			recovery.UnaryServerInterceptor(recoveryOpt),
			utils.SentryUnaryServerInterceptor(), // Add Sentry server interceptor
		),
		grpc.ChainStreamInterceptor(recovery.StreamServerInterceptor(recoveryOpt)),
	)

	// ... (start gRPC server)

	// Register gRPC client with Sentry interceptor
	conn, err := grpc.NewClient(
		fmt.Sprintf("localhost:%d", utils.Config.PortGrpc),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(utils.SentryUnaryClientInterceptor()), // Add Sentry client interceptor
	)
	if err != nil {
		log.Fatalln("Failed to dial server:", err)
	}
	defer conn.Close()

	// Note: If using deprecated grpc.Dial, update to grpc.NewClient:
	// grpc.Dial → grpc.NewClient (deprecated since grpc-go v1.53)

	// ... (register API handlers)

	// HTTP Gateway setup - CORRECT middleware order is critical
	gwServer := &http.Server{
		Addr: fmt.Sprintf(":%d", utils.Config.PortHttp),
		Handler: handlers.CORS(
			// ... CORS config
		)(commonServiceMiddleware(
			sentryHandler.Handle(
				utils.SentryTaggingMiddleware(mux) // Add tagging middleware
			)
		)),
	}
	log.Println("Serving gRPC-Gateway on connection:", utils.Config.PortHttp)
	log.Fatalln(gwServer.ListenAndServe())
}
```

**Middleware Order** (innermost to outermost):

1. `mux` - Your routes
2. `SentryTaggingMiddleware` - Adds tags and trace headers
3. `sentryHandler.Handle` - Creates transaction and hub
4. `commonServiceMiddleware` - Your tenant extraction
5. `CORS` - CORS handling

**Important**: Update your middleware to add tenant_id to context:

```go
func commonServiceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ... extract subdomain and tenant_id ...

		// Add tenant_id to context (needed for goroutine propagation)
		ctx := context.WithValue(r.Context(), utils.TenantContextKey{}, tenantId)
		r = r.WithContext(ctx)

		next.ServeHTTP(w, r)
	})
}
```

---

### 6. Update All `LogError` Callsites

Change from `utils.LogError("label", err)` to `utils.LogError(ctx, "label", err)`

Context sources: HTTP handlers use `r.Context()`, gRPC handlers use `ctx` parameter, background jobs use `context.Background()`.

#### Migration Strategy for Large Codebases

The number of `LogError` callsites varies by service size. Here's how to approach it:

**Small Services (<200 calls)**:

- Update everything in one session (2-3 hours)
- Let the compiler find all callsites after changing the signature
- Fix them all at once

**Medium Services (200-400 calls)**:

- Day 1: Update infrastructure + high-traffic endpoints
- Day 2: Update background jobs and workers
- Day 3: Update remaining utility functions

**Large Services (400+ calls)**:

- Consider a feature branch
- Migrate module by module over 3-5 days
- Use IDE refactoring tools (Find & Replace with regex)
- Example regex: `utils\.LogError\("([^"]+)", (.+)\)` → `utils.LogError(ctx, "$1", $2)`

**Common Pattern**: Functions that call `LogError` need to accept `context.Context`:

```go
// Before
func sendEmail(email string) error {
    // ...
    if err != nil {
        utils.LogError("Failed to send email", err)
        return err
    }
}

// After - add ctx parameter
func sendEmail(ctx context.Context, email string) error {
    // ...
    if err != nil {
        utils.LogError(ctx, "Failed to send email", err)
        return err
    }
}
```

---

## Goroutine Support

### Simple Goroutines (Recommended for 99% of cases)

**No setup needed!** Just pass the context:

```go
// In your handler:
go sendPushNotification(ctx, message)

// In sendPushNotification:
func sendPushNotification(ctx context.Context, message string) {
    // Your code here
    if err != nil {
        utils.LogError(ctx, "Failed to send push", err) // Automatically linked to trace!
    }
}
```

**How it works**: The trace ID is stored in the context. `LogError` detects it and adds it as a tag, making the error easily findable in Sentry by trace_id.

**Note**: For detailed performance monitoring of goroutines (creating child spans), see **Part 2: Granular Performance Tracing**.

---

### Common Goroutine Patterns

Here are typical patterns you'll encounter and how to handle them:

#### Pattern 1: Fire-and-Forget Notifications

```go
// Common in: Push notifications, emails, webhooks
func (s *server) AcceptOrder(ctx context.Context, req *pb.OrderRequest) (*pb.OrderResponse, error) {
    // ... process order ...

    // Simple goroutine - errors auto-linked to trace
    go s.sendPushNotification(ctx, orderID)

    return response, nil
}

func (s *server) sendPushNotification(ctx context.Context, orderID string) {
    // Just pass ctx to LogError - it works!
    if err := s.pushClient.Send(orderID); err != nil {
        utils.LogError(ctx, "Failed to send push notification", err)
    }
}
```

#### Pattern 2: Background Data Processing

```go
// Common in: Image uploads, menu syncing, data imports
func (s *server) SyncMenus(ctx context.Context, req *pb.SyncRequest) (*pb.SyncResponse, error) {
    // Get menu items
    items := s.fetchMenuItems()

    // Process images in background
    go s.processMenuImages(ctx, items)

    return &pb.SyncResponse{Status: "started"}, nil
}

func (s *server) processMenuImages(ctx context.Context, items []MenuItem) {
    for _, item := range items {
        if err := s.uploadImage(item); err != nil {
            utils.LogError(ctx, "Failed to upload image", err) // Linked to trace
        }
    }
}
```

#### Pattern 3: Event Publishing

```go
// Common in: User events, analytics, audit logs
func (s *server) RegisterUser(ctx context.Context, req *pb.RegisterRequest) (*pb.UserResponse, error) {
    user := s.createUser(req)

    // Publish event asynchronously
    go s.publishUserEvent(ctx, user, "registration_success")

    return &pb.UserResponse{User: user}, nil
}

func (s *server) publishUserEvent(ctx context.Context, user *User, eventType string) {
    event := createEvent(user, eventType)
    if err := s.eventBus.Publish(event); err != nil {
        utils.LogError(ctx, "Failed to publish user event", err)
    }
}
```

#### Pattern 4: Multiple Parallel Operations

```go
// Common in: Data aggregation, parallel API calls
func (s *server) GetDashboard(ctx context.Context, req *pb.DashboardRequest) (*pb.DashboardResponse, error) {
    var wg sync.WaitGroup
    var orders []Order
    var stats Stats

    // Launch multiple goroutines
    wg.Add(2)

    go func() {
        defer wg.Done()
        var err error
        orders, err = s.fetchOrders(ctx)
        if err != nil {
            utils.LogError(ctx, "Failed to fetch orders", err)
        }
    }()

    go func() {
        defer wg.Done()
        var err error
        stats, err = s.fetchStats(ctx)
        if err != nil {
            utils.LogError(ctx, "Failed to fetch stats", err)
        }
    }()

    wg.Wait()
    return buildDashboard(orders, stats), nil
}
```

**Key Takeaway**: In all patterns, just pass `ctx` to the goroutine function. No special setup needed!

---

## Critical Gotchas and Lessons Learned

### 1. **Context Overwriting in gRPC Interceptor**

❌ **WRONG**:

```go
ctx = grpcSpan.Context()
```

✅ **CORRECT**:

```go
ctx = sentry.SetHubOnContext(ctx, hub)
```

**Why**: `grpcSpan.Context()` creates a new context with only the span, **losing all gRPC metadata** including authentication headers. This will cause "unauthorized" errors.

**Trade-off**: By using `SetHubOnContext` instead of `grpcSpan.Context()`, handlers cannot directly create child spans via `sentry.SpanFromContext(ctx)`. However, this is an acceptable trade-off because:

- Auth and tenant metadata are preserved
- Errors are still properly captured and traced
- The parent-child relationship between HTTP and gRPC spans is maintained
- Custom child spans can be created using `utils.StartSpan` if needed

**Symptom**: Endpoints suddenly return "unauthorized" or "Failed to get metadata from context" after adding Sentry interceptor.

---

### 2. **Sentry Hub vs Global Capture**

❌ **WRONG**:

```go
sentry.CaptureException(err) // Always uses global hub
```

✅ **CORRECT**:

```go
if hub := sentry.GetHubFromContext(ctx); hub != nil {
	hub.CaptureException(err) // Uses request-scoped hub
} else {
	sentry.CaptureException(err) // Fallback
}
```

**Why**: Using the hub from context ensures errors are attached to the correct transaction/trace. Global capture creates separate, disconnected events.

---

### 3. **Trace Propagation via Span Registry** (Go-Specific)

❌ **WRONG**:

```go
// Trying to rely only on context propagation without span registry
func SentryUnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, ...) error {
		span := sentry.SpanFromContext(ctx)
		// Span metadata added but not stored in registry
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
```

✅ **CORRECT**:

```go
func SentryUnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, ...) error {
		span := sentry.SpanFromContext(ctx)
		traceID := span.TraceID.String()
		spanRegistry.Store(traceID, span)  // Store in registry
		defer spanRegistry.Delete(traceID)
		// ... add metadata and invoke
	}
}
```

**Why**: Go's `context.Context` doesn't survive gRPC network boundaries. The span registry pattern bridges the HTTP→gRPC gap by temporarily storing spans in memory.

**Note**: The `TracePropagationTargets` option available in other language SDKs (JavaScript, Python) does not exist in the Go SDK v0.38.0. Go requires the explicit span registry pattern for gRPC trace continuity.

**Symptom**: Without registry, separate traces are created instead of unified parent-child relationships.

---

### 4. **Creating Child Spans**

❌ **WRONG**:

```go
// Creates a new transaction (separate trace)
grpcSpan := sentry.StartSpan(ctx, "grpc.server")
```

✅ **CORRECT**:

```go
// Creates a child span (same trace)
if parentSpan != nil {
	grpcSpan = parentSpan.StartChild("grpc.server")
} else {
	// Fallback for external calls
	grpcSpan = sentry.StartSpan(ctx, "grpc.server")
}
```

**Why**: `StartChild()` maintains the parent-child relationship and ensures spans are part of the same trace.

**Symptom**: Two separate traces in Sentry instead of one unified trace with parent and child spans.

---

### 5. **Metadata Header Prefixes**

`grpc-gateway` prefixes HTTP headers with `fwd-` (e.g., `x-tenant-id` becomes `fwd-x-tenant-id`). Check both prefixed and non-prefixed versions when extracting metadata.

---

### 6. **Registry Cleanup**

Always use `defer spanRegistry.Delete(traceID)` after storing spans to prevent memory leaks.

---

### 7. **Nil Checks**

Always check for nil before accessing Sentry objects (`hub`, `span`, `metadata`) as they may not exist for direct gRPC calls, background jobs, or if initialization fails.

---

### 8. **Error Status Mapping**

Map gRPC status codes to Sentry status codes using `grpcCodeToSentryStatus()` and set `grpc.code` + `grpc.message` data. Without this, all errors show as "internal_error".

---

### 9. **Tag Naming Convention**

Use structured, hierarchical tag names: `tenant.id`, `tenant.subdomain`, `http.method`, `http.route`, `grpc.method` (not `tenant_id`, `subdomain`, `method`).

---

### 10. **Middleware Order Matters**

Correct order (innermost to outermost): `mux` → `SentryTaggingMiddleware` → `sentryHandler.Handle` → `commonServiceMiddleware` → `CORS`

`sentryHandler` must be before `SentryTaggingMiddleware` to create the hub. `commonServiceMiddleware` must be before to set tenant headers.

---

## Verification

Expected Sentry trace structure:

```
└─ http.server GET /v1/luna/auth (200ms)
   └─ grpc.server /LightspeedService/GetAuthStatus (150ms)
```

Both spans should have same `trace_id`, parent-child relationship, and tags: `tenant.id`, `tenant.subdomain`, `http.method`, `http.route`, `grpc.method`.

**Note**: This shows the basic trace. For detailed performance breakdown with additional spans, see **Part 2: Granular Performance Tracing**.

**Common Issues**:

- **Two separate traces**: Registry not storing/retrieving span
- **Missing tags**: Middleware order incorrect
- **Unauthorized errors**: Context overwritten (use `SetHubOnContext`)
- **Empty error messages**: gRPC status not extracted
- **Goroutine errors not linked**: Make sure to pass `ctx` to goroutines

---

## Configuration Notes

**Trace Sampling**: Adjust `TracesSampleRate` in `initSentry()` based on environment:

- Local/Dev: `1.0` (100%) - trace everything for debugging
- Staging: `0.5` (50%) - balance visibility and quota
- Production: `0.1` (10%) - reduce quota usage

**Performance**: The span registry (`sync.Map`) has minimal overhead - only stores active requests and cleans up immediately after each call.

---

## Troubleshooting

### Issue: "Two separate traces instead of one unified trace"

**Symptom**: HTTP and gRPC calls appear as separate traces in Sentry.

**Cause**: Span registry not working or client interceptor not storing span.

**Solution**:

1. Verify `SentryUnaryClientInterceptor` is added to gRPC client connection
2. Check logs for span storage/retrieval
3. Ensure `grpc.NewClient` is used (not deprecated `grpc.Dial`)

---

### Issue: "Unauthorized errors after adding interceptors"

**Symptom**: Endpoints return 401/403 errors that worked before.

**Cause**: Context being overwritten, losing gRPC metadata including auth headers.

**Solution**:

- Verify server interceptor uses `sentry.SetHubOnContext(ctx, hub)`
- **Never** use `ctx = grpcSpan.Context()` - it wipes metadata
- See Gotcha #1 in Critical Gotchas section

---

### Issue: "Goroutine errors not linked to trace"

**Symptom**: Errors from goroutines appear as separate events.

**Cause**: Context not passed to goroutine, or goroutine not accepting context.

**Solution**:

1. Ensure goroutine function accepts `ctx context.Context` parameter
2. Pass context when launching: `go funcName(ctx, ...)`
3. Use `utils.LogError(ctx, ...)` inside goroutine
4. Verify trace_id tag appears in error event

---

### Issue: "Missing tenant tags in Sentry"

**Symptom**: Errors don't have `tenant.id` or `tenant.subdomain` tags.

**Cause**: Middleware order incorrect or tags not being set.

**Solution**:

1. Check middleware order: `commonServiceMiddleware` must be before `SentryTaggingMiddleware`
2. Verify `SentryTaggingMiddleware` is in the chain
3. Check that tenant_id is added to context in `commonServiceMiddleware`
4. Verify headers `x-tenant-id` and `x-sub-domain` are set

---

### Issue: "High memory usage"

**Symptom**: Memory increases over time.

**Cause**: Span registry not cleaning up (memory leak).

**Solution**:

- Verify `defer spanRegistry.Delete(traceID)` exists in client interceptor
- Check for panics in client interceptor preventing cleanup
- Add monitoring/logging for registry size

---

### Issue: "Compilation errors after LogError update"

**Symptom**: Hundreds of "not enough arguments" errors.

**Solution**: This is expected! The compiler is helping you find all callsites.

1. Go through each error systematically
2. For handlers: Pass `r.Context()` or `ctx`
3. For services: Add `ctx context.Context` parameter
4. For utilities: Use `context.Background()` temporarily
5. Use IDE's "Find & Replace" feature for bulk updates

---

## Implementation Timeline & Effort Estimation

Based on successful implementations across multiple services:

### Small Service (< 200 LogError calls)

**Examples**: user-service, simple APIs

**Timeline**: 1 day (3-4 hours)

- Hour 1: Create 3 new files (logging, tags, grpc interceptors)
- Hour 2: Update main.go (interceptors, middleware)
- Hour 3: Fix LogError callsites (~150-200 updates)
- Hour 4: Testing and verification

**Complexity**: ⭐⭐ Low

---

### Medium Service (200-400 LogError calls)

**Examples**: kds-management-service

**Timeline**: 2 days (6-8 hours)

- Day 1 (4 hours):
  - Infrastructure setup (1 hour)
  - High-traffic endpoints (3 hours)
- Day 2 (4 hours):
  - Background jobs and utilities (2 hours)
  - Testing across all flows (2 hours)

**Complexity**: ⭐⭐⭐ Medium

---

### Large Service (400+ LogError calls)

**Examples**: Complex monoliths, legacy services

**Timeline**: 1 week (20-25 hours)

- Day 1: Infrastructure + critical path
- Days 2-4: Module-by-module migration
- Day 5: Testing, cleanup, documentation

**Complexity**: ⭐⭐⭐⭐ High

**Recommendation**: Use feature branch, migrate incrementally

---

### Factors That Increase Time

- **Deeply nested function calls**: Functions calling functions calling LogError
- **No existing Sentry**: Starting from scratch adds 1-2 hours
- **Complex goroutine patterns**: Heavy background processing adds 2-3 hours
- **Poor test coverage**: Manual testing takes longer
- **Legacy code**: Unclear ownership, fear of breaking things

### Factors That Decrease Time

- **Good IDE refactoring tools**: VSCode/IntelliJ can bulk update
- **Existing Sentry setup**: Already halfway there
- **Good test coverage**: Confidence to move fast
- **Clear module boundaries**: Easy to migrate module-by-module
- **Second implementation**: Learn from the first one

---

## What You Have Now

After completing Part 1, you have:

✅ **Unified traces** - HTTP and gRPC calls in one trace  
✅ **Error tracking** - All errors linked to the correct trace  
✅ **Tenant context** - Every trace tagged with tenant information  
✅ **Goroutine support** - Errors in background tasks automatically linked  
✅ **Basic performance visibility** - 2-span view (HTTP + gRPC)

**Your traces look like this**:

```
http.server GET /v1/luna/auth (500ms)
└── grpc.server /LightspeedService/GetAuthStatus (450ms)
```

You can see:

- ✅ Total request time (500ms)
- ✅ gRPC handler time (450ms)
- ✅ All errors that occurred
- ✅ Which tenant the request was for
- ❌ What happened _inside_ those 450ms (not yet visible)

---

## What's Next: Part 2 (Optional)

**Part 2: Granular Performance Tracing** adds detailed visibility _inside_ your service methods.

**Before Part 2** (what you have now):

```
http.server GET /v1/luna/auth (500ms)
└── grpc.server /LightspeedService/GetAuthStatus (450ms)  ← Where did 450ms go?
```

**After Part 2**:

```
http.server GET /v1/luna/auth (500ms)
└── grpc.server /LightspeedService/GetAuthStatus (450ms)
    └── LightspeedService.GetAuthStatus (445ms)
        ├── LightspeedService.getClientCredentials (15ms)
        ├── LightspeedService.getAuthTokenWithTransaction (80ms)
        ├── LightspeedRepo.GetBusinesses (200ms)  ← Bottleneck found!
        └── LightspeedRepo.GetWebhook (150ms)
```

**When to implement Part 2**:

- 🔍 You need to **find performance bottlenecks**
- 📊 You want **detailed observability** for optimization
- 🐛 You're **debugging complex flows** and need more visibility
- ⚡ You're doing **performance tuning** and need data

**When to skip Part 2**:

- ✅ Part 1 gives you enough visibility for now
- ✅ Your service is fast enough and errors are rare
- ✅ You want to implement it later when you actually need it

See `sentry-p2.md` for implementation guide.

---

## References

- [Sentry Go SDK Documentation](https://docs.sentry.io/platforms/go/)
- [Sentry Distributed Tracing](https://docs.sentry.io/product/sentry-basics/tracing/distributed-tracing/)
- [gRPC Go Interceptors](https://github.com/grpc/grpc-go/blob/master/examples/features/interceptor/README.md)
- [grpc-gateway Documentation](https://grpc-ecosystem.github.io/grpc-gateway/)

---

**Last Updated**: November 21, 2025  
**Sentry SDK Version**: v0.38.0  
**Status**: Part 1 Complete ✅  
**Next**: Part 2 Optional (see `sentry-p2.md`)
