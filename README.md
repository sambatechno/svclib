# svclib - Service Infrastructure Library

[![Go Version](https://img.shields.io/badge/Go-1.24.3+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

A Go library providing observability infrastructure for microservices, with a focus on Sentry distributed tracing, error tracking, and context management.

## Features

✅ **Distributed Tracing** - Full HTTP→gRPC trace continuity with Sentry  
✅ **Context-Aware Error Logging** - Automatic error linking to traces  
✅ **Tenant Tagging** - Automatic tenant context in all traces and errors  
✅ **Performance Tracking** - Granular span tracking with `StartSpan`  
✅ **Zero-Config Goroutines** - Errors in goroutines automatically linked to parent traces  
✅ **Standard Utilities** - HeaderMatcher and ParseMockTenant helpers  

---

## Quick Start

### Installation

```bash
go get github.com/sambatechno/svclib@latest
```

### Basic Integration (5 minutes)

```go
package main

import (
    "context"
    "log"
    "time"
    
    "github.com/getsentry/sentry-go"
    "github.com/sambatechno/svclib"
    "google.golang.org/grpc"
)

func main() {
    // 1. Initialize Sentry
    sentryHandler, err := svclib.Init(svclib.Config{
        DSN:              "https://your-dsn@sentry.io/project",
        Environment:      "production",
        TracesSampleRate: 0.1,
        EnableTracing:    true,
    })
    if err != nil {
        log.Fatalf("Sentry init failed: %v", err)
    }
    defer sentry.Flush(2 * time.Second)

    // 2. Setup gRPC with interceptors
    grpcServer := grpc.NewServer(
        grpc.ChainUnaryInterceptor(
            svclib.UnaryServerInterceptor(),
        ),
    )

    grpcClient, _ := grpc.NewClient("localhost:8080",
        grpc.WithChainUnaryInterceptor(
            svclib.UnaryClientInterceptor(),
        ),
    )

    // 3. Setup HTTP middleware
    handler := sentryHandler.Handle(
        svclib.TenantTaggingMiddleware(yourHandler),
    )

    // 4. Use in your code
    ctx := svclib.WithTenantID(ctx, "tenant-123")
    svclib.LogError(ctx, "Something failed", err)
}
```

---

## Table of Contents

- [What This Library Does](#what-this-library-does)
- [Installation](#installation)
- [Core Components](#core-components)
  - [Initialization](#initialization)
  - [Context Management](#context-management)
  - [Error Logging](#error-logging)
  - [HTTP Middleware](#http-middleware)
  - [gRPC Interceptors](#grpc-interceptors)
  - [Performance Tracking](#performance-tracking)
- [Utilities](#utilities)
- [Examples](#examples)
- [Migration Guide](#migration-guide)
- [API Reference](#api-reference)
- [Troubleshooting](#troubleshooting)

---

## What This Library Does

`svclib` provides the infrastructure for **distributed tracing** across microservices using Sentry. It solves the problem of maintaining trace continuity across HTTP→gRPC boundaries in grpc-gateway architectures.

### Problem It Solves

In a typical microservice architecture with grpc-gateway:
1. HTTP request comes in (handled by Sentry's HTTP handler)
2. grpc-gateway converts it to internal gRPC call
3. **Trace context is lost** ❌

This library maintains the trace by:
1. Client interceptor stores span in registry
2. Server interceptor retrieves span and creates child span
3. All errors and logs are linked to the original trace ✅

### Key Benefits

- **Automatic trace linking** - No manual trace ID management
- **Context-aware errors** - Errors automatically linked to traces
- **Goroutine support** - Errors in goroutines linked to parent trace
- **Tenant tagging** - All traces tagged with tenant information
- **Performance insights** - Granular span tracking with `StartSpan`

---

## Core Components

### Initialization

Initialize Sentry with a simple configuration:

```go
import (
    "github.com/sambatechno/svclib"
    "github.com/getsentry/sentry-go"
)

handler, err := svclib.Init(svclib.Config{
    DSN:              "https://your-dsn@sentry.io/project",
    Environment:      "production",
    TracesSampleRate: 0.1,
    EnableTracing:    true,
    Repanic:          true,
})
if err != nil {
    log.Printf("Sentry init failed: %v\n", err)
}
defer sentry.Flush(2 * time.Second)
```

**Configuration Options:**
- `DSN` - Sentry Data Source Name
- `Environment` - "local", "development", or "production"
- `TracesSampleRate` - 0.0 to 1.0 (recommended: 1.0 for dev, 0.1 for prod)
- `EnableTracing` - Enable distributed tracing
- `Repanic` - Repanic after capturing errors (default: true)

---

### Context Management

Standard context key for tenant ID across all services:

```go
// Set tenant ID in context
ctx := svclib.WithTenantID(r.Context(), tenantID)

// Retrieve tenant ID from context
tenantID, ok := svclib.GetTenantID(ctx)
if ok {
    // Use tenant ID for DB queries, etc.
    db.Query(ctx, "SELECT * FROM users WHERE tenant_id = ?", tenantID)
}
```

**Key Points:**
- Use `svclib.TenantContextKey{}` consistently across all services
- Works for both Sentry tagging AND database queries
- Standardized across your entire microservice architecture

---

### Error Logging

Context-aware error logging with automatic trace linking:

```go
// In HTTP handlers
func (s *Server) CreateUser(ctx context.Context, req *pb.Request) (*pb.Response, error) {
    user, err := s.db.CreateUser(ctx, req.User)
    if err != nil {
        svclib.LogError(ctx, "Failed to create user", err)
        return nil, err
    }
    return &pb.Response{User: user}, nil
}

// In goroutines (automatically linked to parent trace!)
go func(ctx context.Context) {
    data, err := fetchData()
    if err != nil {
        svclib.LogError(ctx, "Failed to fetch data in goroutine", err)
    }
}(ctx)
```

**Features:**
- ✅ Logs to stdout and Sentry
- ✅ Automatically links to current span/trace
- ✅ Works in handlers, goroutines, and background tasks
- ✅ No special handling needed - just pass context

---

### HTTP Middleware

Tag HTTP requests with tenant and trace information:

```go
// Standard middleware (uses svclib.TenantContextKey)
handler := sentryHandler.Handle(
    svclib.TenantTaggingMiddleware(yourHandler),
)

// Custom extractor (for special cases)
handler := sentryHandler.Handle(
    svclib.TenantTaggingMiddlewareWithExtractor(
        func(r *http.Request) (tenantID, subdomain string) {
            return r.Header.Get("x-tenant-id"), r.Header.Get("x-sub-domain")
        },
    )(yourHandler),
)
```

**What it does:**
- Adds `http.method`, `http.route` tags
- Adds `tenant.id`, `tenant.subdomain` tags
- Propagates Sentry trace to gRPC metadata

---

### gRPC Interceptors

Maintain trace continuity across gRPC calls:

```go
// Server interceptor
grpcServer := grpc.NewServer(
    grpc.ChainUnaryInterceptor(
        recovery.UnaryServerInterceptor(recoveryOpt),
        svclib.UnaryServerInterceptor(),
    ),
)

// Client interceptor (for grpc-gateway)
conn, err := grpc.NewClient(
    "localhost:8080",
    grpc.WithChainUnaryInterceptor(
        svclib.UnaryClientInterceptor(),
    ),
)
```

**How it works:**
1. Client interceptor stores span in registry
2. Adds `sentry-trace` and `sentry-trace-id` to metadata
3. Server interceptor retrieves span from registry
4. Creates child span for proper parent-child relationship
5. Tags span with tenant info and gRPC method
6. Captures errors with full context

---

### Performance Tracking

Create granular spans for performance monitoring:

```go
func (s *Server) ProcessOrder(ctx context.Context, req *pb.OrderRequest) (resp *pb.OrderResponse, err error) {
    // Create span for this method
    ctx, finish := svclib.StartSpan(ctx, "OrderService.ProcessOrder")
    defer finish(&err)

    // Validate order (sub-span)
    ctx, finishValidate := svclib.StartSpan(ctx, "validate_order")
    validateErr := s.validateOrder(ctx, req.Order)
    finishValidate(&validateErr)
    if validateErr != nil {
        return nil, validateErr
    }

    // Save to database (sub-span)
    ctx, finishDB := svclib.StartSpan(ctx, "save_order_to_db")
    dbErr := s.db.SaveOrder(ctx, req.Order)
    finishDB(&dbErr)
    if dbErr != nil {
        return nil, dbErr
    }

    return &pb.OrderResponse{OrderId: "123"}, nil
}
```

**For goroutines:**

```go
go func() {
    ctx, finish := svclib.StartSpan(ctx, "background.email_notification")
    defer finish(nil)
    
    err := sendEmail(ctx, user.Email)
    if err != nil {
        svclib.LogError(ctx, "Failed to send email", err)
    }
}()
```

---

## Utilities

### DefaultHeaderMatcher

Standard header matcher for grpc-gateway (forwards all headers with "fwd-" prefix):

```go
grpcMux := runtime.NewServeMux(
    runtime.WithIncomingHeaderMatcher(svclib.DefaultHeaderMatcher()),
)
```

### ParseMockTenant

Extract mock tenant from command line for local testing:

```go
func main() {
    utils.ReadConfig()
    sentryHandler := initSentry()

    // Get mock tenant from CLI
    mockedTenant := svclib.ParseMockTenant()
    if mockedTenant != "" {
        log.Printf("Using mock tenant: %s\n", mockedTenant)
    }

    // ... rest of main ...
}
```

Run with: `go run main.go my-test-tenant`

---

## Examples

See the `examples/` directory for complete working examples:

- **`examples/basic/`** - Basic integration with error tracking
- **`examples/with_performance/`** - Full integration with performance tracking
- **`examples/custom_tenant/`** - Custom tenant extraction

---

## Migration Guide

See [MIGRATION.md](MIGRATION.md) for detailed step-by-step migration guides for:
- integration-service (reference implementation)
- Other services (adding full tracing)

---

## API Reference

### Types

```go
type Config struct {
    DSN              string
    Environment      string
    TracesSampleRate float64
    EnableTracing    bool
    Repanic          bool
}

type TenantContextKey struct{}
```

### Functions

```go
// Initialization
func Init(cfg Config) (*sentryhttp.Handler, error)

// Context Management
func WithTenantID(ctx context.Context, tenantID string) context.Context
func GetTenantID(ctx context.Context) (string, bool)

// Error Logging
func LogError(ctx context.Context, label string, err error)

// Middleware
func TenantTaggingMiddleware(next http.Handler) http.Handler
func TenantTaggingMiddlewareWithExtractor(
    extractTenant func(*http.Request) (tenantID, subdomain string),
) func(http.Handler) http.Handler

// Interceptors
func UnaryClientInterceptor() grpc.UnaryClientInterceptor
func UnaryServerInterceptor() grpc.UnaryServerInterceptor

// Performance Tracking
func StartSpan(ctx context.Context, spanName string) (context.Context, func(*error))

// Utilities
func DefaultHeaderMatcher() runtime.HeaderMatcherFunc
func ParseMockTenant() string
```

---

## Troubleshooting

### Traces not showing up in Sentry

**Check:**
1. ✅ Both client and server interceptors are installed
2. ✅ `EnableTracing: true` in config
3. ✅ `TracesSampleRate` is > 0
4. ✅ HTTP middleware is in the chain

### Errors not linked to traces

**Check:**
1. ✅ Using `svclib.LogError(ctx, ...)` (not `log.Println`)
2. ✅ Passing context through function calls
3. ✅ Interceptors are installed

### Tenant tags not appearing

**Check:**
1. ✅ Tenant ID is set in context with `svclib.WithTenantID`
2. ✅ `TenantTaggingMiddleware` is installed
3. ✅ Middleware runs AFTER tenant is set in context

### StartSpan not working

**Check:**
1. ✅ Using returned context (not original)
2. ✅ Calling `defer finish(&err)` or `defer finish(nil)`
3. ✅ Parent span exists in context

---

## Documentation

For detailed information about distributed tracing with Sentry:
- [sentry-p1.md](sentry-p1.md) - Part 1: Error tracking + distributed tracing
- [sentry-p2.md](sentry-p2.md) - Part 2: Granular performance tracing

---

## License

MIT License - see [LICENSE](LICENSE) for details

---

## Contributing

Issues and pull requests welcome! Please ensure:
- Code compiles with `go build ./...`
- Examples work
- Documentation is updated

---

## Changelog

### v0.1.0 (Initial Release)
- Sentry initialization
- Context management (TenantContextKey)
- Context-aware error logging
- HTTP tenant tagging middleware
- gRPC client and server interceptors
- Performance tracking with StartSpan
- Utility functions (DefaultHeaderMatcher, ParseMockTenant)

