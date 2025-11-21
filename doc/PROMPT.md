# Service Library (svclib) Extraction Prompt

## Context

You are working in the `~/Work/cata/svclib` repository. Your goal is to **extract Sentry integration code from `integration-service` into a reusable Go library** that can be shared across multiple microservices.

**Library Name**: `svclib` (service library)
- **Why not `sentrylib`**: The library provides service infrastructure features (logging, tracing, context handling) that are useful beyond just Sentry. The name should be generic since context keys might be used for DB queries and other service infrastructure needs.
- **Module path**: `github.com/sambatechno/svclib`
- **Current focus**: Observability (error tracking, distributed tracing)
- **Scope**: This extraction focuses on Sentry integration. Future expansions may include other service infrastructure.

### Reference Implementation

The working implementation is in: `~/Work/cata/integration-service/`

**Key files to extract**:
- `utils/logging.go` (70 lines) - Context-aware error logging
- `utils/sentry_tags.go` (50 lines) - HTTP middleware for tenant tagging
- `utils/sentry_grpc.go` (383 lines) - gRPC interceptors and StartSpan helper

**Documentation**:
- `sentry-p1.md` - Part 1: Error tracking + distributed tracing (should be copied to sentrylib)
- `sentry-p2.md` - Part 2: Granular performance tracing (should be copied to sentrylib)

### Architecture Overview

All services in `~/Work/cata/*-service/` use:
- HTTP endpoints via `grpc-gateway`
- gRPC service handlers
- Similar structure: `module project`
- Similar setup (but may vary in tenant context handling)

---

## Your Mission

### 1. Analyze the Reference Implementation

Before creating anything, **analyze** these services to understand variations:

```bash
# Check these services for structure comparison:
~/Work/cata/integration-service/    # Reference implementation ✅
~/Work/cata/user-service/            # Has Sentry, check compatibility
~/Work/cata/kds-management-service/  # Large service, check patterns
~/Work/cata/order-service/           # Check context handling
~/Work/cata/payment-service/         # Check middleware patterns
```

**Questions to answer**:
1. Do they all use similar middleware chains?
2. How do they handle tenant ID in context?
3. Do they all use `grpc-gateway`?
4. Are there variations in context key naming?
5. Do they have `utils.GetTenantIdFromCtx()` or similar?

**Document your findings** in `ANALYSIS.md` before proceeding.

---

### 2. Design the Library API

Based on your analysis, design a library that:

✅ **Works with the reference implementation** (integration-service)  
✅ **Adapts to variations** in other services  
✅ **Minimizes changes** needed in target services  
✅ **Provides sensible defaults** but allows customization  

**Core Principles**:
- **Simple by default**: 3-5 lines to integrate
- **Flexible where needed**: Allow custom tenant extractors
- **Type-safe**: Export context keys properly
- **Well-documented**: Clear examples for each use case

---

### 3. Create the Library Structure

**Proposed structure**:

```
svclib/
├── go.mod                      # module github.com/sambatechno/svclib
├── go.sum
├── README.md                   # Main documentation
├── ANALYSIS.md                 # Your findings from step 1
├── sentry-p1.md                # Copy from integration-service
├── sentry-p2.md                # Copy from integration-service
├── sentry.go                   # Init() and Config
├── logging.go                  # LogError function
├── middleware.go               # HTTP middleware (TenantTaggingMiddleware)
├── interceptor.go              # gRPC interceptors (client + server)
├── span.go                     # StartSpan (Part 2)
├── context.go                  # Context helpers and keys
├── grpc.go                     # DefaultHeaderMatcher (100% identical across services)
├── testing.go                  # ParseMockTenant (for dev/testing)
├── examples/
│   ├── basic/
│   │   └── main.go            # Basic integration example
│   ├── custom_tenant/
│   │   └── main.go            # Custom tenant extraction
│   └── full_instrumentation/
│       └── main.go            # Part 2 with StartSpan
└── internal/
    └── registry.go            # Span registry (not exported)
```

---

### 4. Key Design Decisions

#### A. Tenant Context Handling

**Challenge**: Services might extract tenant ID differently.

**Solution**: Provide both built-in and custom extractors:

```go
// Default: Extract from context using TenantContextKey
func TenantTaggingMiddleware(next http.Handler) http.Handler

// Custom: User provides extractor function
func TenantTaggingMiddlewareWithExtractor(
    extractTenant func(*http.Request) (tenantID, subdomain string),
) func(http.Handler) http.Handler
```

**Example**:
```go
// For services that use the library's context key:
handler := sentrylib.TenantTaggingMiddleware(mux)

// For services with custom extraction:
handler := sentrylib.TenantTaggingMiddlewareWithExtractor(
    func(r *http.Request) (string, string) {
        return r.Header.Get("x-tenant-id"), r.Header.Get("x-sub-domain")
    },
)(mux)
```

#### B. Context Keys

**Challenge**: Services might have existing context keys for tenant ID (e.g., for DB queries).

**IMPORTANT**: Do NOT force services to migrate their context keys!

**Solution**: Make TenantContextKey OPTIONAL and provide custom extractors:

```go
// OPTIONAL: Exported context key that services CAN use
// But services can keep their own keys - not required!
type TenantContextKey struct{}

// Optional helpers (only if service wants to use library's key)
func WithTenantID(ctx context.Context, tenantID string) context.Context
func GetTenantID(ctx context.Context) (string, bool)

// Internal keys (not exported - prevents conflicts)
type grpcSpanContextKey struct{}
type sentryTraceContextKey struct{}
```

**Key point**: Services should be able to:
1. Use their existing context keys for DB queries
2. Provide custom tenant extraction for Sentry middleware
3. Optionally migrate to library's key if they want

**Example**:
```go
// Service keeps existing context key for DB
type MyServiceTenantKey struct{}

// DB query uses service's own key
db.Query(ctx, "SELECT * FROM users WHERE tenant_id = ?", 
    ctx.Value(MyServiceTenantKey{}).(string))

// But Sentry middleware extracts tenant via custom function
middleware := svclib.TenantTaggingMiddlewareWithExtractor(
    func(r *http.Request) (tenantID, subdomain string) {
        // Read from service's context or headers
        if id, ok := r.Context().Value(MyServiceTenantKey{}).(string); ok {
            return id, r.Header.Get("x-sub-domain")
        }
        return r.Header.Get("x-tenant-id"), r.Header.Get("x-sub-domain")
    },
)
```

#### C. Initialization

**Keep it simple**:

```go
// Minimal initialization
sentryHandler, err := sentrylib.Init(sentrylib.Config{
    DSN:              "https://...",
    Environment:      "production",
    TracesSampleRate: 0.1,
})

// Advanced initialization with options
sentryHandler, err := sentrylib.InitWithOptions(sentrylib.Config{
    DSN:         "https://...",
    Environment: "production",
}, sentrylib.Options{
    CustomTenantExtractor: myExtractorFunc,
    EnableDebugLogging:    true,
})
```

---

### 5. Implementation Checklist

- [ ] **Analyze** 5+ services for patterns and variations
- [ ] **Document findings** in `ANALYSIS.md`
- [ ] **Extract code** from `integration-service/utils/`
- [ ] **Refactor** to remove internal dependencies
- [ ] **Add flexibility** for tenant extraction variations
- [ ] **Export proper types** and helpers
- [ ] **Write comprehensive README** with:
  - [ ] Quick start guide
  - [ ] API reference
  - [ ] Migration guide from integration-service
  - [ ] Examples for common patterns
  - [ ] Troubleshooting section
- [ ] **Create examples/** directory with working code
- [ ] **Test compilation** against integration-service imports
- [ ] **Verify** no breaking changes to existing code

---

### 6. Testing Strategy

**Compatibility Test**:

Create a test file that simulates how `integration-service` would use the library:

```go
// test_integration.go
package sentrylib_test

import (
    "context"
    "testing"
    "github.com/sambatechno/sentrylib"
)

// Test that mimics integration-service usage
func TestIntegrationServiceCompatibility(t *testing.T) {
    // Initialize
    _, err := sentrylib.Init(sentrylib.Config{
        DSN:              "test",
        Environment:      "test",
        TracesSampleRate: 1.0,
    })
    if err != nil {
        t.Fatal(err)
    }
    
    // Test context helpers
    ctx := context.Background()
    ctx = sentrylib.WithTenantID(ctx, "test-tenant")
    
    tenantID, ok := sentrylib.GetTenantID(ctx)
    if !ok || tenantID != "test-tenant" {
        t.Errorf("Expected tenant ID 'test-tenant', got '%s'", tenantID)
    }
    
    // Test LogError doesn't panic
    sentrylib.LogError(ctx, "test error", nil)
    
    // Test StartSpan
    ctx, finish := sentrylib.StartSpan(ctx, "test.operation")
    defer finish(nil)
}
```

---

### 7. Migration Guide Template

Create `MIGRATION.md` with step-by-step instructions:

```markdown
# Migrating from integration-service utils to sentrylib

## For integration-service (Reference Implementation)

### Step 1: Add Dependency
\`\`\`bash
go get github.com/sambatechno/sentrylib@latest
\`\`\`

### Step 2: Remove Old Files
Delete:
- `utils/logging.go`
- `utils/sentry_tags.go`
- `utils/sentry_grpc.go`

### Step 3: Update Imports
Replace all occurrences:
- `utils.LogError` → `sentrylib.LogError`
- `utils.SentryTaggingMiddleware` → `sentrylib.TenantTaggingMiddleware`
- `utils.SentryUnaryServerInterceptor` → `sentrylib.UnaryServerInterceptor`
- `utils.SentryUnaryClientInterceptor` → `sentrylib.UnaryClientInterceptor`
- `utils.StartSpan` → `sentrylib.StartSpan`
- `utils.TenantContextKey` → `sentrylib.TenantContextKey`

### Step 4: Update Context Helpers
Replace:
- `utils.GetTenantIdFromCtx(ctx)` → `sentrylib.GetTenantID(ctx)`
- `context.WithValue(ctx, utils.TenantContextKey{}, id)` → `sentrylib.WithTenantID(ctx, id)`

### Step 5: Test
\`\`\`bash
go build ./...
go test ./...
\`\`\`

## For Other Services

[To be filled based on analysis]
```

---

### 8. README Template

Your README should cover:

1. **Quick Start** (5 lines of code)
2. **What This Library Does** (from sentry-p1.md overview)
3. **Installation**
4. **Basic Usage** (Part 1)
5. **Advanced Usage** (Part 2)
6. **API Reference**
7. **Examples**
8. **Migration Guides**
9. **Troubleshooting**
10. **References to sentry-p1.md and sentry-p2.md**

---

### 9. Critical Requirements

❗ **Must-haves**:
1. **Zero breaking changes** for integration-service
2. **Simple imports** - no complex setup
3. **Exported context key** for compatibility
4. **Flexible tenant extraction** for service variations
5. **Complete documentation** with examples
6. **Works with `module project`** naming

❗ **Nice-to-haves**:
1. Debug logging option
2. Custom tag extractors
3. Performance metrics
4. Test helpers

---

### 10. Low-Hanging Fruit (Bonus - If Time Permits)

These are **100% identical** across all services and safe to extract:

#### A. DefaultHeaderMatcher (grpc.go)

Found in: integration-service, user-service, kds-management-service (IDENTICAL)

```go
// DefaultHeaderMatcher is the standard header matcher for grpc-gateway
// It forwards all headers with "fwd-" prefix
func DefaultHeaderMatcher() runtime.HeaderMatcherFunc {
    return func(key string) (string, bool) {
        k, ok := runtime.DefaultHeaderMatcher(key)
        if ok {
            return k, ok
        }
        key = textproto.CanonicalMIMEHeaderKey(key)
        return "fwd-" + key, true
    }
}
```

**Usage in services**:
```go
// Before (every service has this function)
func HeaderMatcher(key string) (string, bool) { ... }
grpcMux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(HeaderMatcher))

// After (use library)
grpcMux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(svclib.DefaultHeaderMatcher()))
```

#### B. ParseMockTenant (testing.go)

Found in: integration-service, user-service, kds-management-service (IDENTICAL pattern)

```go
// ParseMockTenant extracts mock tenant from command line args for testing
// Usage: go run main.go mock-tenant-name
func ParseMockTenant() string {
    if len(os.Args) <= 1 {
        return ""
    }
    return os.Args[1]
}
```

**Usage in services**:
```go
// Before (every service has mockArguments function)
func mockArguments() {
    if len(os.Args) <= 1 { return }
    mockedTenant = os.Args[1]
}

// After (use library)
mockedTenant := svclib.ParseMockTenant()
```

**Benefits**: Saves 10-15 lines per service, 100% safe (no variations).

**Note**: These are optional but highly recommended - they're trivial to implement and provide immediate value.

---

### 11. Deliverables

When you're done, the repository should have:

✅ **Working library code** that compiles  
✅ **ANALYSIS.md** with findings from service comparison  
✅ **README.md** with comprehensive documentation  
✅ **MIGRATION.md** with step-by-step guides  
✅ **examples/** directory with 3+ working examples  
✅ **sentry-p1.md** and **sentry-p2.md** (copied and updated if needed)  
✅ **Test file** demonstrating integration-service compatibility  
✅ **grpc.go** with DefaultHeaderMatcher (bonus, if time permits)  
✅ **testing.go** with ParseMockTenant (bonus, if time permits)  

---

## Success Criteria

The library is ready when:

1. ✅ You can describe variations between services in `ANALYSIS.md`
2. ✅ `integration-service` can replace `utils/sentry_*.go` with this library with minimal changes
3. ✅ Other services can integrate with 10-20 lines of code
4. ✅ Documentation is clear enough for a new developer to integrate in 30 minutes
5. ✅ Examples cover common use cases
6. ✅ Tests pass
7. ✅ No external dependencies beyond `sentry-go` and standard gRPC

---

## Tips

- **Start with analysis** - don't code until you understand the variations
- **Keep it simple** - resist over-engineering
- **Think about the user** - what's the minimal integration effort?
- **Document assumptions** - note what might need customization
- **Test early** - verify imports work before finishing everything
- **Be pragmatic** - it's okay to have "basic" and "advanced" APIs

---

## Questions to Consider

Before finalizing, answer these:

1. Can a service integrate this in < 30 minutes?
2. Does it work for services with different tenant extraction?
3. Is the API intuitive?
4. Are the examples clear?
5. Does it maintain the same behavior as integration-service?
6. Can services gradually adopt Part 2 (StartSpan) later?

---

## Reference Commands

```bash
# Analyze service structure
find ~/Work/cata -name "main.go" -path "*-service/*" -exec grep -l "grpc.NewServer" {} \;

# Check for tenant context patterns
grep -r "TenantContextKey\|tenant.*context\|GetTenantId" ~/Work/cata/user-service/
grep -r "TenantContextKey\|tenant.*context\|GetTenantId" ~/Work/cata/kds-management-service/

# Check middleware patterns
grep -r "commonServiceMiddleware\|middleware" ~/Work/cata/user-service/main.go
grep -r "sentryhttp\|sentry\.Init" ~/Work/cata/user-service/

# Check existing Sentry integration
grep -r "sentry" ~/Work/cata/user-service/ | grep -v "node_modules\|vendor"
```

---

Good luck! Focus on **analysis first**, then create a **simple, flexible API** that works across service variations. The goal is to make Sentry integration **trivial** for all services while maintaining the power and flexibility of the current implementation.

**Reference the working code in `integration-service`** - that's your source of truth! ✅

