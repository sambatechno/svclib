# Migration Guide for svclib

This guide provides step-by-step instructions for migrating services to use `svclib`.

---

## Table of Contents

- [integration-service (Reference Implementation)](#integration-service-reference-implementation)
- [Other Services (Adding Full Tracing)](#other-services-adding-full-tracing)
- [Services Without Subdomain Routing](#services-without-subdomain-routing)
- [Context Key Migration](#context-key-migration)
- [Common Migration Steps](#common-migration-steps)
- [Common Pitfalls](#common-pitfalls)
- [Troubleshooting](#troubleshooting)
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

| Find                                 | Replace                          |
| ------------------------------------ | -------------------------------- |
| `utils.SentryTaggingMiddleware`      | `svclib.TenantTaggingMiddleware` |
| `utils.SentryUnaryServerInterceptor` | `svclib.UnaryServerInterceptor`  |
| `utils.SentryUnaryClientInterceptor` | `svclib.UnaryClientInterceptor`  |
| `utils.StartSpan`                    | `svclib.StartSpan`               |
| `utils.TenantContextKey`             | `svclib.TenantContextKey`        |
| `utils.GetTenantIdFromCtx`           | `svclib.GetTenantID`             |

**Note:** `GetTenantID` returns `(string, bool)` instead of just `string`.

**Do NOT globally replace `utils.LogError`** - it needs context parameter added (see Step 5).

### Step 5: Update ALL LogError Calls

**Critical:** Find all LogError calls and add context parameter.

```bash
grep -r "utils.LogError(" . --include="*.go"
```

#### Before:

```go
utils.LogError("Failed to process", err)
```

#### After:

```go
svclib.LogError(ctx, "Failed to process", err)
```

Check all files:

- Service implementations
- Database package
- Test files (`*_test.go`)
- Helper functions

For init code: `svclib.LogError(context.Background(), "label", err)`

### Step 6: Update GetTenantIdFromCtx Calls

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

### Step 7: Update Context Setting

#### Before:

```go
ctx := context.WithValue(r.Context(), utils.TenantContextKey{}, tenantId)
```

#### After:

```go
ctx := svclib.WithTenantID(r.Context(), tenantId)
```

### Step 8: Add Import

Add to imports at the top of files that use svclib:

```go
import (
    // ... other imports ...
    "github.com/sambatechno/svclib"
)
```

### Step 9: Build and Test

```bash
# Build
go mod tidy
go build ./...

# Test
go test ./...

# Run service
go run main.go
```

### Step 10: Verify in Sentry

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

**Critical:** Find ALL LogError calls - not just in service files!

```bash
grep -r "utils.LogError(" . --include="*.go"
```

#### Before:

```go
utils.LogError("Failed to process", err)
```

#### After:

```go
svclib.LogError(ctx, "Failed to process", err)
```

**Note:** You need to add `ctx` parameter. This enables trace linking.

**Don't forget to check:**

- Service method implementations
- Database package files (`database.go`, `redis.go`)
- Test files (`*_test.go`)
- Utility/helper functions

For initialization code without request context:

```go
utils.LogError(context.Background(), "Database connection failed", err)
```

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

## Services Without Subdomain Routing

For services that don't use subdomain-based tenant routing (like central-be, admin services, or services where tenant comes from request parameters).

**Estimated time:** 30-45 minutes

### Key Differences

Unlike services with subdomain routing (integration-service, user-service, etc.), these services:

- ✅ Distributed tracing works the same way
- ✅ Tenant tagging is **optional** - svclib works fine without it
- ✅ Add tenant to context only when you have it (e.g., in specific methods)
- ✅ No need for `TenantTaggingMiddleware` on HTTP layer

### Migration Pattern

Follow steps 1-3 from "Other Services" section above, then:

#### Step 4: Update utils/tenant.go (if exists)

Replace custom tenant context key with svclib's standard key:

```go
package utils

import (
    "context"
    "github.com/sambatechno/svclib"
    "google.golang.org/grpc/metadata"
)

func GetTenantIdFromCtx(ctx context.Context) string {
    // Try svclib context key (standard across all services)
    tenantID, ok := svclib.GetTenantID(ctx)
    if ok && tenantID != "" {
        return tenantID
    }

    // Fallback to gRPC metadata for backward compatibility
    metadata := metadata.ValueFromIncomingContext(ctx, "fwd-x-tenant-id")
    if len(metadata) >= 1 {
        return metadata[0]
    }

    return ""
}
```

Delete the `type TenantContextKey struct{}` - use svclib's instead.

#### Step 5: Update utils/logging.go

Add context parameter to LogError:

```go
package utils

import (
    "context"
    "log"
    "github.com/sambatechno/svclib"
)

var LogError = func(ctx context.Context, label string, err error) {
    errContext := errorContext(label, err)
    log.Println(errContext, err)
    svclib.LogError(ctx, label, err)
}
```

**Important:** This now requires `ctx` as the first parameter.

#### Step 6: Add Tenant to Context When Available

In service methods that have tenant information, add it to context:

```go
func (s *service) UpdateTenantSettings(ctx context.Context, req *pb.Request) (*pb.Response, error) {
    // Add tenant to context when you have it (preserves trace context!)
    ctx = svclib.WithTenantID(ctx, req.TenantId)

    // Now all subsequent LogError calls and database queries have tenant context
    err := s.DB.Tenant().UpdateSettings(ctx, params)
    if err != nil {
        utils.LogError(ctx, "UpdateSettings failed", err) // Auto-tagged with tenant.id
        return nil, err
    }
    // ...
}
```

**Critical:** Never use `context.Background()` - always extend the existing context!

#### Step 7: Update All LogError Calls

Search for all `utils.LogError` calls and add `ctx` parameter:

```bash
grep -r "utils.LogError(" . --include="*.go"
```

Update each occurrence:

```go
// Before
utils.LogError("GetTenants", err)

// After
utils.LogError(ctx, "GetTenants", err)
```

Don't forget to update:

- Service methods
- Database initialization code (use `context.Background()`)
- Test files (use `context.Background()`)

#### Step 8: Optional Session Tagging

For services with authentication, add user/session tagging:

```go
import "github.com/getsentry/sentry-go"

func grpcSessionTagging() grpc.UnaryServerInterceptor {
    return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
        md, ok := metadata.FromIncomingContext(ctx)
        if ok {
            // Tag with user email
            if emails := md.Get("session-email"); len(emails) > 0 {
                if hub := sentry.GetHubFromContext(ctx); hub != nil {
                    hub.ConfigureScope(func(scope *sentry.Scope) {
                        scope.SetUser(sentry.User{Email: emails[0]})
                        scope.SetTag("session.email", emails[0])
                    })
                }
            }
            // Tag with session ID
            if ids := md.Get("session-id"); len(ids) > 0 {
                if hub := sentry.GetHubFromContext(ctx); hub != nil {
                    hub.ConfigureScope(func(scope *sentry.Scope) {
                        scope.SetTag("session.id", ids[0])
                    })
                }
            }
        }
        return handler(ctx, req)
    }
}

// Add after auth interceptor:
gs := grpc.NewServer(
    grpc.ChainUnaryInterceptor(
        recovery.UnaryServerInterceptor(recoveryOpt),
        grpcCheckAuth(redisDb),
        grpcSessionTagging(),  // Add here, after auth
        svclib.UnaryServerInterceptor(),
    ),
)
```

### What Gets Tagged

With this pattern:

| Endpoint Type                 | Has Tenant? | Sentry Tags                            |
| ----------------------------- | ----------- | -------------------------------------- |
| Public (Login, ResetPassword) | ❌ No       | Traces work, no tenant tag             |
| Admin endpoints               | ❌ No       | Traces work, session tags only         |
| Tenant operations             | ✅ Yes      | Traces work with tenant + session tags |

**Important:** This is normal! Tenant tagging is optional - traces work perfectly without it.

### Example: central-be

See central-be for a complete example. Key points:

- Only 1-2 methods actually need tenant context (e.g., `SetTenantAutoInvoice`)
- Most endpoints work without tenant tags
- Session tagging provides user context for all authenticated requests

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

| Find                                                      | Replace                        |
| --------------------------------------------------------- | ------------------------------ |
| `database.TenantContextKey{}`                             | `svclib.TenantContextKey{}`    |
| `context.WithValue(ctx, database.TenantContextKey{}, id)` | `svclib.WithTenantID(ctx, id)` |
| `ctx.Value(database.TenantContextKey{}).(string)`         | Use `svclib.GetTenantID(ctx)`  |

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

**Important:** Don't forget test files! They often call LogError and use context.

#### Find all test files with LogError:

```bash
grep -r "LogError(" . --include="*_test.go"
```

#### Update LogError calls in tests:

**Before:**

```go
func TestSomething(t *testing.T) {
    LogError("test error", err)
}
```

**After:**

```go
import "context"

func TestSomething(t *testing.T) {
    ctx := context.Background()
    LogError(ctx, "test error", err)
}
```

#### Update context creation with tenant:

**Before:**

```go
ctx := context.WithValue(context.Background(), utils.TenantContextKey{}, "test-tenant")
```

**After:**

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

## Common Pitfalls

### ❌ Anti-Pattern: Using context.Background() in Handlers

**Never create new context with `context.Background()` in handlers** - this breaks trace continuity!

#### Bad:

```go
// ❌ Creates new context, breaks tracing!
ctxDB := context.WithValue(context.Background(), key, value)
db.Query(ctxDB, ...)
```

#### Good:

```go
// ✅ Extends existing context, preserves traces
ctx = svclib.WithTenantID(ctx, tenantId)
db.Query(ctx, ...)
```

**Why it matters:** `context.Background()` creates a brand new context without any trace information, so Sentry can't link database errors or operations back to the original HTTP/gRPC request.

### ❌ Forgetting LogError Calls in Initialization Code

Don't forget to update LogError calls in:

- Database connection initialization (`database.go`, `redis.go`)
- Test files (`*_test.go`)
- Helper functions without context access

For initialization code without context:

```go
// In database connection or other init code
utils.LogError(context.Background(), "Database connection failed", err)
```

### ❌ Not Checking All Test Files

Test files often call LogError. Search comprehensively:

```bash
grep -r "LogError(" . --include="*_test.go"
```

Update each test to pass context:

```go
func TestSomething(t *testing.T) {
    ctx := context.Background()
    utils.LogError(ctx, "test error", err)
}
```

### ❌ Missing Context Import

After adding `ctx` parameter to LogError, don't forget to import:

```go
import (
    "context"
    // ... other imports
)
```

---

## Troubleshooting

### Build fails: "not enough arguments in call to utils.LogError"

You missed updating some LogError calls. Search for all occurrences:

```bash
grep -r "utils.LogError(" . --include="*.go"
```

Common places to check:

- Service method implementations
- Database package (`database.go`, `redis.go`)
- Test files (`*_test.go`)
- Utility packages

Update each to include `ctx` as first parameter:

```go
utils.LogError(ctx, "label", err)
```

### Tests fail: "not enough arguments" or context parameter missing

Update test files to pass context:

```go
import "context"

func TestExample(t *testing.T) {
    ctx := context.Background()
    LogError(ctx, "label", err)
}
```

### Tenant tags not showing in Sentry

This is **expected** for endpoints without tenant context. Verify:

1. **For subdomain-routed services:** Is `TenantTaggingMiddleware` added?
2. **For parameter-based services:** Is tenant being added with `svclib.WithTenantID(ctx, tenantId)`?
3. **For public/admin endpoints:** No tenant tag is normal!

**Remember:** Tenant tagging is optional - distributed tracing works fine without it!

### Traces not connecting HTTP → gRPC

Check that:

1. `svclib.UnaryServerInterceptor()` is added to gRPC server
2. `svclib.UnaryClientInterceptor()` is added to gRPC client
3. Both interceptors are in the chain (verify with logs)
4. `svclib.DefaultHeaderMatcher()` is used in grpc-gateway

### Session tags not appearing

For services with authentication:

1. Verify `grpcSessionTagging()` interceptor is added **after** auth interceptor
2. Check that auth interceptor is setting session metadata (`session-email`, `session-id`)
3. Ensure Sentry hub is available (check `sentry.GetHubFromContext(ctx)` is not nil)

### "svclib is not in your go.mod file"

Run:

```bash
go get github.com/sambatechno/svclib@latest
go mod tidy
```

If it still fails, check that svclib is imported in at least one `.go` file before running `go mod tidy`.

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
- [ ] Verify ALL LogError calls updated (service, database, tests)
- [ ] Check for `context.Background()` anti-pattern in handlers
- [ ] Build and test
- [ ] Verify in Sentry

### Subdomain-routed services (user-service, order-service, etc.):

- [ ] Add dependency
- [ ] Replace initSentry()
- [ ] Add gRPC interceptors (server + client)
- [ ] Add TenantTaggingMiddleware to HTTP layer
- [ ] Replace HeaderMatcher
- [ ] Replace mockArguments (if exists)
- [ ] Migrate context key (if using custom key)
- [ ] Update ALL LogError calls (service, database, tests)
- [ ] Check for `context.Background()` anti-pattern in handlers
- [ ] Build and test
- [ ] Verify in Sentry

### Non-subdomain services (central-be, admin services):

- [ ] Add dependency
- [ ] Replace initSentry()
- [ ] Add gRPC interceptors (server + client)
- [ ] Replace HeaderMatcher
- [ ] Update utils/tenant.go to use svclib context key
- [ ] Update utils/logging.go to require context parameter
- [ ] Add tenant to context in relevant methods (`svclib.WithTenantID`)
- [ ] Update ALL LogError calls (service, database, tests)
- [ ] Check for `context.Background()` anti-pattern in handlers
- [ ] Optional: Add session tagging interceptor
- [ ] Build and test
- [ ] Verify in Sentry (tenant tags optional)

### Common verification for all services:

- [ ] Search for remaining LogError calls: `grep -r "utils.LogError(" . --include="*.go"`
- [ ] Search for context.Background() misuse: `grep -r "context.Background()" . --include="*.go"`
- [ ] All tests pass: `go test ./...`
- [ ] No linter errors
- [ ] HTTP → gRPC traces connected in Sentry
- [ ] Errors properly linked to traces

---

**Estimated total time per service:** 20-45 minutes  
**One-time effort with long-term benefits:** Centralized observability infrastructure ✅
