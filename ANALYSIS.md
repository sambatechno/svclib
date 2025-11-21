# Service Analysis for svclib Extraction

## Executive Summary

Analyzed 5 services to understand Sentry integration patterns and variations. Found that **integration-service** has the most complete implementation with full distributed tracing, while other services have varying levels of Sentry integration.

## Services Analyzed

1. **integration-service** (reference implementation) ✅
2. **user-service**
3. **order-service**
4. **payment-service**
5. **kds-management-service**

---

## Key Findings

### 1. Sentry Integration Levels

| Service                    | Sentry Init   | HTTP Handler | LogError           | gRPC Interceptors  | Distributed Tracing | StartSpan |
| -------------------------- | ------------- | ------------ | ------------------ | ------------------ | ------------------- | --------- |
| **integration-service**    | ✅            | ✅           | ✅ (context-aware) | ✅ Client + Server | ✅ Full             | ✅        |
| **user-service**           | ✅            | ✅           | ✅ (basic)         | ❌                 | ❌                  | ❌        |
| **order-service**          | ✅            | ✅           | ✅ (basic)         | ❌                 | ❌                  | ❌        |
| **payment-service**        | ✅            | ✅           | Basic (assumed)    | ❌                 | ❌                  | ❌        |
| **kds-management-service** | ✅ (in utils) | ✅           | ✅ (basic)         | ❌                 | ❌                  | ❌        |

**Conclusion**: Only integration-service has full distributed tracing. Other services would benefit from the library.

---

### 2. Context Key Variations

**integration-service**:

```go
// utils/tenant.go
type TenantContextKey struct{}
func GetTenantIdFromCtx(ctx context.Context) string
```

**user-service**:

```go
// database/database-helper.go
type TenantContextKey struct{}
// Uses: context.WithValue(ctx, database.TenantContextKey{}, tenantId)
```

**Observation**:

- Different packages define TenantContextKey (utils vs database)
- Services use context keys for DB queries, not just Sentry
- **Decision**: Standardize on library's TenantContextKey across all services for consistency

---

### 3. LogError Variations

**integration-service** (context-aware):

```go
func LogError(ctx context.Context, label string, err error)
// - Links errors to current span
// - Supports goroutines with trace linking
// - Automatically detects span from context
```

**user-service, order-service** (basic):

```go
func LogError(label string, err error)
// - Simple sentry.CaptureException(err)
// - No trace linking
// - No context awareness
```

**Impact**: Library will provide context-aware version. Services will migrate call sites as part of adoption.

---

### 4. 100% Identical Code (Low-Hanging Fruit)

#### A. HeaderMatcher

**Found in**: integration-service, user-service, order-service, payment-service, kds-management-service

```go
func HeaderMatcher(key string) (string, bool) {
    k, ok := runtime.DefaultHeaderMatcher(key)
    if ok {
        return k, ok
    }
    key = textproto.CanonicalMIMEHeaderKey(key)
    return "fwd-" + key, true
}
```

**Usage**: `runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(HeaderMatcher))`

**Benefit**: Saves 8 lines per service, 100% safe to extract.

---

#### B. mockArguments

**Found in**: integration-service, user-service, order-service, payment-service, kds-management-service

```go
func mockArguments() {
    if len(os.Args) <= 1 {
        return
    }
    mockedTenant = os.Args[1]
    log.Println("Using Mock arguments", mockedTenant)
}
```

**Usage**: Called in main() before starting services

**Benefit**: Saves 5-6 lines per service, 100% safe to extract.

---

### 5. Sentry Initialization Pattern

All services with Sentry use similar initialization:

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
    return sentryhttp.New(sentryhttp.Options{
        Repanic: true, // integration-service
        // OR no options (other services)
    })
}
```

**Pattern**: Environment detection logic is consistent across services.

---

### 6. Middleware Chain Analysis

**integration-service**:

```go
// main.go
mux.Handle("/", sentryHandler.Handle(
    utils.SentryTaggingMiddleware(
        commonServiceMiddleware(handler),
    ),
))

// In SentryTaggingMiddleware:
// - Reads tenant from context (set by commonServiceMiddleware)
// - Adds Sentry tags (tenant.id, tenant.subdomain)
// - Propagates trace context to gRPC
```

**Other services**: No tenant tagging middleware, just sentryhttp.Handler

**Impact**: Library middleware will:

1. Use standard svclib.TenantContextKey{} for consistency
2. Optionally support custom extractors for edge cases
3. Services migrate to standard key for simplified integration

---

### 7. gRPC Interceptor Setup

**integration-service**:

```go
// Client interceptor (for grpc-gateway internal calls)
conn, _ := grpc.NewClient("localhost:8080",
    grpc.WithChainUnaryInterceptor(utils.SentryUnaryClientInterceptor()),
)

// Server interceptor (for gRPC handlers)
gs := grpc.NewServer(
    grpc.ChainUnaryInterceptor(
        recovery.UnaryServerInterceptor(recoveryOpt),
        utils.SentryUnaryServerInterceptor(),
    ),
)
```

**Other services**: No Sentry interceptors, only recovery middleware

**Pattern**: Interceptors work together to maintain trace continuity across HTTP→gRPC boundaries.

---

## Recommended Abstraction Strategy

### Phase 1: Core Functionality (Priority P0)

1. **sentry.go**: Simple Init() function
2. **logging.go**: Context-aware LogError (with trace linking)
3. **middleware.go**: TenantTaggingMiddleware with flexible extractors
4. **interceptor.go**: gRPC interceptors (client + server)
5. **span.go**: StartSpan for performance tracking
6. **context.go**: Standard TenantContextKey + helpers (for consistency across services)

### Phase 2: Utilities (Priority P1 - Bonus)

7. **grpc.go**: DefaultHeaderMatcher (100% identical, safe to extract)
8. **testing.go**: ParseMockTenant (100% identical, safe to extract)

---

## Design Decisions

### A. Standardized TenantContextKey

**Problem**: Services have existing context keys in different packages.

**Solution**: Standardize on library's TenantContextKey across all services.

```go
// Exported - standard context key for all services
type TenantContextKey struct{}
func WithTenantID(ctx context.Context, tenantID string) context.Context
func GetTenantID(ctx context.Context) (string, bool)

// Migration strategy:
// 1. Services update from database.TenantContextKey{} → svclib.TenantContextKey{}
// 2. Consistent usage across all services (DB queries + Sentry)
// 3. Simplifies middleware (no custom extractors needed for most cases)
```

---

### B. Tenant Tagging Middleware

**Solution**: Standard middleware using library's TenantContextKey.

```go
// Standard middleware - reads from svclib.TenantContextKey{}
func TenantTaggingMiddleware(next http.Handler) http.Handler

// Optional custom extractor for special cases
func TenantTaggingMiddlewareWithExtractor(
    extractTenant func(*http.Request) (tenantID, subdomain string),
) func(http.Handler) http.Handler

// Standard usage (after migration):
middleware := svclib.TenantTaggingMiddleware(handler)

// Custom extractor (only if needed for special cases):
middleware := svclib.TenantTaggingMiddlewareWithExtractor(
    func(r *http.Request) (string, string) {
        // Custom extraction logic for edge cases
        return customExtractTenant(r)
    },
)
```

---

### C. Context-Aware LogError

**Problem**: Existing services use `LogError(label, err)`, new pattern is `LogError(ctx, label, err)`.

**Solution**: Only export context-aware version for best practices.

```go
// Library exports:
func LogError(ctx context.Context, label string, err error)

// Migration is straightforward:
// Before: utils.LogError("Failed", err)
// After:  svclib.LogError(ctx, "Failed", err)
```

**Benefits**:

- Forces best practices (context-aware error tracking)
- Automatic trace linking in distributed systems
- Consistent API across all services

---

### D. Simple Init API

**Problem**: All services have similar but slightly different initialization.

**Solution**: Unified Config struct with sensible defaults.

```go
type Config struct {
    DSN              string
    Environment      string  // "local", "development", "production"
    TracesSampleRate float64
    EnableTracing    bool
    Repanic          bool   // Default: true (integration-service pattern)
}

func Init(cfg Config) (*sentryhttp.Handler, error)
```

**Usage**:

```go
handler, err := svclib.Init(svclib.Config{
    DSN:              "https://...",
    Environment:      "production",
    TracesSampleRate: 0.1,
    EnableTracing:    true,
})
```

---

## Implementation Risks & Mitigations

### Risk 1: Breaking Changes for integration-service

**Mitigation**:

- Keep exact same API signatures
- Export same types (TenantContextKey, context keys)
- Maintain same behavior

**Validation**: Create compatibility test that simulates integration-service usage.

---

### Risk 2: Context Key Migration Effort

**Mitigation**:

- Provide clear migration guide for updating context keys
- Simple find/replace: `database.TenantContextKey{}` → `svclib.TenantContextKey{}`
- Standardization benefits outweigh one-time migration effort
- Document migration strategy with examples

---

### Risk 3: Incomplete Distributed Tracing

**Mitigation**:

- Test trace continuity with real HTTP→gRPC calls
- Document interceptor setup clearly
- Provide working examples

---

## Success Criteria

✅ **integration-service** can drop-in replace with < 50 lines of changes  
✅ **user-service** can add full tracing with < 30 lines  
✅ **order-service** can add full tracing with < 30 lines  
✅ All services can replace HeaderMatcher with 1-line import  
✅ All services can replace mockArguments with 1-line import  
✅ No behavior changes vs current implementation  
✅ Documentation clear enough for 30-minute integration

---

## Next Steps

1. ✅ **Complete analysis** (DONE)
2. **Create library structure** (go.mod, directory layout)
3. **Extract code** from integration-service/utils/
4. **Refactor** to remove service-specific dependencies
5. **Add flexibility** for variations (custom extractors)
6. **Document** thoroughly with examples
7. **Test** with integration-service
8. **Iterate** based on feedback

---

## Files to Extract

| Source File                 | Library File                 | Lines          | Complexity |
| --------------------------- | ---------------------------- | -------------- | ---------- |
| `utils/logging.go`          | `logging.go`                 | 70             | Low        |
| `utils/sentry_tags.go`      | `middleware.go`              | 42             | Low        |
| `utils/sentry_grpc.go`      | `interceptor.go` + `span.go` | 383            | Medium     |
| `utils/tenant.go` (partial) | `context.go`                 | 15             | Low        |
| `main.go` (HeaderMatcher)   | `grpc.go`                    | 8              | Low        |
| `main.go` (mockArguments)   | `testing.go`                 | 6              | Low        |
| N/A (new)                   | `sentry.go`                  | 50             | Low        |
| **TOTAL**                   |                              | **~574 lines** |            |

**Estimated Effort**: 4-6 hours for extraction + 2-3 hours for documentation + 1 hour for testing = **7-10 hours total**.

---

## Appendix: Service-Specific Notes

### integration-service

- Most complete implementation
- Source of truth for library
- Uses `utils.TenantContextKey{}`
- Has `GetTenantIdFromCtx()` helper
- Full distributed tracing setup

### user-service

- Basic Sentry (no tracing)
- Uses `database.TenantContextKey{}`
- Simple `LogError(label, err)` without context
- Good candidate for library adoption

### order-service

- Basic Sentry (no tracing)
- Simple `LogError(label, err)` without context
- Good candidate for library adoption

### payment-service

- Basic Sentry (no tracing)
- Similar to order-service

### kds-management-service

- Has Sentry but only in utils (SetupLogUtil)
- Would benefit from library
- Uses HeaderMatcher (identical pattern)

---

**Analysis Date**: 2025-11-21  
**Analyst**: AI Assistant  
**Status**: ✅ Complete - Ready for Implementation
