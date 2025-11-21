# Migration Guide for svclib

This guide provides step-by-step instructions for migrating services to use `svclib`.

---

## Table of Contents

- [integration-service (Reference Implementation)](#integration-service-reference-implementation)
- [Other Services (Adding Full Tracing)](#other-services-adding-full-tracing)
- [Context Key Migration](#context-key-migration)
- [Common Migration Steps](#common-migration-steps)
- [Verification](#verification)

---

## integration-service (Reference Implementation)

The integration-service already has full Sentry distributed tracing. Migration is a drop-in replacement.

**Estimated time:** 20-30 minutes

### Step 1: Add Dependency

```bash
cd ~/Work/cata/integration-service

# Add to go.mod
go get github.com/sambatechno/svclib@latest

# For local testing (before publishing):
# Add this line to go.mod:
replace github.com/sambatechno/svclib => ../svclib

go mod tidy
```

### Step 2: Delete Old Files

```bash
# Backup first (optional)
mkdir .backup
cp utils/logging.go utils/sentry_tags.go utils/sentry_grpc.go .backup/

# Delete old files
rm utils/logging.go
rm utils/sentry_tags.go  
rm utils/sentry_grpc.go
```

### Step 3: Update main.go

#### Before:

```go
func HeaderMatcher(key string) (string, bool) {
    k, ok := runtime.DefaultHeaderMatcher(key)
    if ok {
        return k, ok
    }
    key = textproto.CanonicalMIMEHeaderKey(key)
    return "fwd-" + key, true
}

func mockArguments() {
    if len(os.Args) <= 1 {
        return
    }
    mockedTenant = os.Args[1]
    log.Println("Using Mock arguments", mockedTenant)
}

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
        Dsn:              "https://...",
        TracesSampleRate: tracing,
        EnableTracing:    true,
        Environment:      env,
    }); err != nil {
        log.Printf("Sentry initialization failed: %v\n", err)
    }
    return sentryhttp.New(sentryhttp.Options{Repanic: true})
}
```

#### After:

```go
import "github.com/sambatechno/svclib"

// Delete HeaderMatcher - use library's
// Delete mockArguments - use library's

func initSentry() *sentryhttp.Handler {
    env := "local"
    tracing := 1.0
    if strings.HasPrefix(utils.Config.Stage, "prod") {
        env = "production"
        tracing = 0.1
    } else if strings.HasPrefix(utils.Config.Stage, "dev") {
        env = "development"
    }
    
    handler, err := svclib.Init(svclib.Config{
        DSN:              "https://...",
        Environment:      env,
        TracesSampleRate: tracing,
        EnableTracing:    true,
        Repanic:          true,
    })
    if err != nil {
        log.Printf("Sentry initialization failed: %v\n", err)
    }
    return handler
}

func main() {
    utils.ReadConfig()
    sentryHandler := initSentry()
    
    // Use library helper
    mockedTenant := svclib.ParseMockTenant()
    
    // ... rest of main ...
    
    grpcMux := runtime.NewServeMux(
        runtime.WithIncomingHeaderMatcher(svclib.DefaultHeaderMatcher()),
    )
    
    gs := grpc.NewServer(
        grpc.ChainUnaryInterceptor(
            recovery.UnaryServerInterceptor(recoveryOpt),
            svclib.UnaryServerInterceptor(), // Changed from utils.SentryUnaryServerInterceptor()
        ),
    )
    
    conn, _ := grpc.NewClient("localhost:8080",
        grpc.WithChainUnaryInterceptor(svclib.UnaryClientInterceptor()), // Changed from utils.SentryUnaryClientInterceptor()
    )
    
    // ... rest of main ...
}
```

### Step 4: Global Find & Replace

Use your IDE's find & replace feature (Ctrl+Shift+H in VSCode/Cursor):

| Find | Replace |
|------|---------|
| `utils.LogError` | `svclib.LogError` |
| `utils.SentryTaggingMiddleware` | `svclib.TenantTaggingMiddleware` |
| `utils.SentryUnaryServerInterceptor` | `svclib.UnaryServerInterceptor` |
| `utils.SentryUnaryClientInterceptor` | `svclib.UnaryClientInterceptor` |
| `utils.StartSpan` | `svclib.StartSpan` |
| `utils.TenantContextKey` | `svclib.TenantContextKey` |
| `utils.GetTenantIdFromCtx` | `svclib.GetTenantID` |

**Note:** `GetTenantID` returns `(string, bool)` instead of just `string`.

### Step 5: Update GetTenantIdFromCtx Calls

#### Before:

```go
tenantId := utils.GetTenantIdFromCtx(ctx)
if tenantId == "" {
    return nil, errors.New("tenant not found")
}
```

#### After:

```go
tenantId, ok := svclib.GetTenantID(ctx)
if !ok || tenantId == "" {
    return nil, errors.New("tenant not found")
}
```

### Step 6: Update Context Setting

#### Before:

```go
ctx := context.WithValue(r.Context(), utils.TenantContextKey{}, tenantId)
```

#### After:

```go
ctx := svclib.WithTenantID(r.Context(), tenantId)
```

### Step 7: Add Import

Add to imports at the top of files that use svclib:

```go
import (
    // ... other imports ...
    "github.com/sambatechno/svclib"
)
```

### Step 8: Build and Test

```bash
# Build
go mod tidy
go build ./...

# Test
go test ./...

# Run service
go run main.go
```

### Step 9: Verify in Sentry

1. Make a test request
2. Check Sentry dashboard
3. Verify traces show up with:
   - ✅ HTTP → gRPC trace continuity
   - ✅ Tenant tags (tenant.id, tenant.subdomain)
   - ✅ Errors linked to traces

---

## Other Services (Adding Full Tracing)

For services that don't have Sentry distributed tracing yet (user-service, order-service, payment-service, etc.).

**Estimated time:** 30-45 minutes

### Step 1: Add Dependency

```bash
cd ~/Work/cata/<service-name>

go get github.com/sambatechno/svclib@latest

# For local testing:
# Add to go.mod: replace github.com/sambatechno/svclib => ../svclib

go mod tidy
```

### Step 2: Replace initSentry()

#### Before:

```go
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
        Dsn:              "https://...",
        TracesSampleRate: tracing,
        EnableTracing:    true,
        Environment:      env,
    }); err != nil {
        log.Printf("Sentry initialization failed: %v\n", err)
    }
    return sentryhttp.New(sentryhttp.Options{})
}
```

#### After:

```go
import "github.com/sambatechno/svclib"

func initSentry() *sentryhttp.Handler {
    env := "local"
    tracing := 1.0
    if strings.HasPrefix(utils.Config.Stage, "prod") {
        env = "production"
        tracing = 0.1
    } else if strings.HasPrefix(utils.Config.Stage, "dev") {
        env = "development"
    }
    
    handler, err := svclib.Init(svclib.Config{
        DSN:              "https://...",
        Environment:      env,
        TracesSampleRate: tracing,
        EnableTracing:    true,
        Repanic:          true,
    })
    if err != nil {
        log.Printf("Sentry initialization failed: %v\n", err)
    }
    return handler
}
```

### Step 3: Add gRPC Interceptors

#### Before:

```go
gs := grpc.NewServer(
    grpc.ChainUnaryInterceptor(recovery.UnaryServerInterceptor(recoveryOpt)),
)

conn, _ := grpc.NewClient("localhost:8080",
    grpc.WithTransportCredentials(insecure.NewCredentials()),
)
```

#### After:

```go
import "github.com/sambatechno/svclib"

gs := grpc.NewServer(
    grpc.ChainUnaryInterceptor(
        recovery.UnaryServerInterceptor(recoveryOpt),
        svclib.UnaryServerInterceptor(), // Add this
    ),
)

conn, _ := grpc.NewClient("localhost:8080",
    grpc.WithTransportCredentials(insecure.NewCredentials()),
    grpc.WithChainUnaryInterceptor(svclib.UnaryClientInterceptor()), // Add this
)
```

### Step 4: Add Tenant Tagging Middleware

Find where HTTP handlers are registered and add middleware:

#### Before:

```go
handler := sentryHandler.Handle(yourHandler)
```

#### After:

```go
handler := sentryHandler.Handle(
    svclib.TenantTaggingMiddleware(yourHandler),
)
```

### Step 5: Replace HeaderMatcher

#### Before:

```go
func HeaderMatcher(key string) (string, bool) {
    k, ok := runtime.DefaultHeaderMatcher(key)
    if ok {
        return k, ok
    }
    key = textproto.CanonicalMIMEHeaderKey(key)
    return "fwd-" + key, true
}

// Usage:
grpcMux := runtime.NewServeMux(
    runtime.WithIncomingHeaderMatcher(HeaderMatcher),
)
```

#### After:

```go
// Delete HeaderMatcher function

// Usage:
grpcMux := runtime.NewServeMux(
    runtime.WithIncomingHeaderMatcher(svclib.DefaultHeaderMatcher()),
)
```

### Step 6: Replace mockArguments

#### Before:

```go
func mockArguments() {
    if len(os.Args) <= 1 {
        return
    }
    mockedTenant = os.Args[1]
    log.Println("Using Mock arguments")
}

// In main():
mockArguments()
```

#### After:

```go
// Delete mockArguments function

// In main():
mockedTenant := svclib.ParseMockTenant()
```

### Step 7: Migrate Context Key (if needed)

See [Context Key Migration](#context-key-migration) section below.

### Step 8: Update LogError Calls

#### Before:

```go
utils.LogError("Failed to process", err)
```

#### After:

```go
svclib.LogError(ctx, "Failed to process", err)
```

**Note:** You need to add `ctx` parameter. This enables trace linking.

### Step 9: Build and Test

```bash
go mod tidy
go build ./...
go test ./...
go run main.go
```

### Step 10: Verify

1. Make test requests
2. Check Sentry dashboard
3. Verify distributed tracing works

---

## Context Key Migration

For services using `database.TenantContextKey{}` or other custom keys.

### Option 1: Migrate to Standard Key (Recommended)

This standardizes context key usage across all services.

#### Step 1: Find All Usages

```bash
cd ~/Work/cata/<service-name>

# Find all occurrences
grep -r "database.TenantContextKey" .
grep -r "context.WithValue.*TenantContextKey" .
grep -r "ctx.Value.*TenantContextKey" .
```

#### Step 2: Replace Context Key

**In database package:**

Delete or comment out:
```go
// database/database-helper.go
// type TenantContextKey struct{}  // DELETE THIS
```

**Update all usages:**

| Find | Replace |
|------|---------|
| `database.TenantContextKey{}` | `svclib.TenantContextKey{}` |
| `context.WithValue(ctx, database.TenantContextKey{}, id)` | `svclib.WithTenantID(ctx, id)` |
| `ctx.Value(database.TenantContextKey{}).(string)` | Use `svclib.GetTenantID(ctx)` |

#### Step 3: Update Database Helper

```go
// database/database-helper.go
import "github.com/sambatechno/svclib"

func GetTenantFromContext(ctx context.Context) string {
    tenantID, _ := svclib.GetTenantID(ctx)
    return tenantID
}
```

#### Step 4: Test Database Queries

Ensure all queries still work with the new context key:

```go
ctx := svclib.WithTenantID(context.Background(), "test-tenant")
// Should work with DB queries
result := db.Query(ctx, "SELECT * FROM users")
```

### Option 2: Custom Extractor (Not Recommended)

If you can't migrate context keys immediately, use custom extractor:

```go
import "github.com/sambatechno/svclib"

// Temporary: Extract from old context key
middleware := svclib.TenantTaggingMiddlewareWithExtractor(
    func(r *http.Request) (tenantID, subdomain string) {
        // Extract from old context key
        tenantID, _ := r.Context().Value(database.TenantContextKey{}).(string)
        subdomain := r.Header.Get("x-sub-domain")
        return tenantID, subdomain
    },
)

handler := sentryHandler.Handle(middleware(yourHandler))
```

**Note:** This is a temporary solution. Plan to migrate to standard key.

---

## Common Migration Steps

### Update Test Files

Tests often use context with tenant:

#### Before:

```go
ctx := context.WithValue(context.Background(), utils.TenantContextKey{}, "test-tenant")
```

#### After:

```go
ctx := svclib.WithTenantID(context.Background(), "test-tenant")
```

### Update Service Methods

Add context-aware error logging:

#### Before:

```go
func (s *Server) CreateOrder(ctx context.Context, req *pb.OrderRequest) (*pb.OrderResponse, error) {
    order, err := s.db.CreateOrder(ctx, req.Order)
    if err != nil {
        log.Println("Failed to create order:", err)
        return nil, err
    }
    return &pb.OrderResponse{OrderId: order.Id}, nil
}
```

#### After:

```go
import "github.com/sambatechno/svclib"

func (s *Server) CreateOrder(ctx context.Context, req *pb.OrderRequest) (*pb.OrderResponse, error) {
    order, err := s.db.CreateOrder(ctx, req.Order)
    if err != nil {
        svclib.LogError(ctx, "Failed to create order", err)
        return nil, err
    }
    return &pb.OrderResponse{OrderId: order.Id}, nil
}
```

### Optional: Add Performance Tracking

For detailed performance insights:

```go
func (s *Server) ProcessOrder(ctx context.Context, req *pb.OrderRequest) (resp *pb.OrderResponse, err error) {
    ctx, finish := svclib.StartSpan(ctx, "OrderService.ProcessOrder")
    defer finish(&err)
    
    // ... your code ...
    
    return &pb.OrderResponse{}, nil
}
```

---

## Verification

After migration, verify:

### 1. Compilation

```bash
go build ./...
# Should compile without errors
```

### 2. Tests

```bash
go test ./...
# Should pass all tests
```

### 3. Service Starts

```bash
go run main.go
# Should start without errors
```

### 4. Distributed Tracing Works

Make a test request and check Sentry:

- ✅ HTTP request creates a trace
- ✅ gRPC calls are child spans
- ✅ Tenant tags appear (tenant.id, tenant.subdomain)
- ✅ Errors are linked to traces

### 5. Performance Tracking (if using StartSpan)

Check Sentry Performance tab:

- ✅ See operation names (e.g., "OrderService.ProcessOrder")
- ✅ See timing for each span
- ✅ See parent-child relationships

---

## Rollback Plan

If something goes wrong:

### For integration-service:

```bash
# Restore backup
cp .backup/logging.go utils/
cp .backup/sentry_tags.go utils/
cp .backup/sentry_grpc.go utils/

# Remove library
go mod edit -dropreplace github.com/sambatechno/svclib
go mod tidy

# Restore old code (git)
git checkout main.go
```

### For other services:

```bash
# Remove interceptors from main.go
# Remove middleware
# Restore old initSentry() function
go mod tidy
```

---

## Getting Help

If you encounter issues:

1. Check [Troubleshooting](README.md#troubleshooting) section in README
2. Review [ANALYSIS.md](ANALYSIS.md) for design decisions
3. Check examples in `examples/` directory
4. Review [sentry-p1.md](sentry-p1.md) and [sentry-p2.md](sentry-p2.md)

---

## Summary Checklist

### integration-service:
- [ ] Add dependency
- [ ] Delete old utils files
- [ ] Update main.go (Init, HeaderMatcher, mockArguments)
- [ ] Global find & replace
- [ ] Update GetTenantIdFromCtx calls
- [ ] Update context setting
- [ ] Build and test
- [ ] Verify in Sentry

### Other services:
- [ ] Add dependency
- [ ] Replace initSentry()
- [ ] Add gRPC interceptors
- [ ] Add tenant tagging middleware
- [ ] Replace HeaderMatcher
- [ ] Replace mockArguments
- [ ] Migrate context key
- [ ] Update LogError calls
- [ ] Build and test
- [ ] Verify in Sentry

---

**Estimated total time per service:** 20-45 minutes  
**One-time effort with long-term benefits:** Centralized observability infrastructure ✅

