# Service Library (svclib) - Quick Reference for Setup

## Library Name: `svclib`

**Why `svclib` instead of `sentrylib`?**
- Generic name for service infrastructure (not just Sentry)
- Context keys can be used for DB queries, not just observability
- Future-proof: can include other service infrastructure features
- Current focus: Observability (error tracking, distributed tracing)

## What You Need to Do

### 1. Create the Repo

```bash
cd ~/Work/cata
mkdir svclib
cd svclib

# Initialize
git init
go mod init github.com/sambatechno/svclib

# Copy these files from integration-service
cp ../integration-service/sentry-p1.md .
cp ../integration-service/sentry-p2.md .
cp ../integration-service/SENTRYLIB_EXTRACTION_PROMPT.md ./PROMPT.md
cp ../integration-service/SENTRYLIB_TODO.md ./TODO.md
cp ../integration-service/SENTRYLIB_QUICK_REFERENCE.md ./QUICK_REFERENCE.md
```

### 2. Open Cursor in svclib

```bash
cd ~/Work/cata/svclib
cursor .
```

### 3. Give the Agent This Prompt

Open the **Agent Mode** in Cursor and paste:

```
Please read PROMPT.md and implement the svclib (service library) extraction project.

Key points:
1. Start by analyzing services in ~/Work/cata/*-service/ to understand variations
2. Extract code from ~/Work/cata/integration-service/utils/
3. Create a flexible API that works across service variations
4. Focus on making integration simple (< 30 minutes per service)
5. Document everything thoroughly

The reference implementation is in ~/Work/cata/integration-service/ - that's the working code.

Follow the checklist in TODO.md and deliverables in PROMPT.md.

Start with analysis, then extraction, then documentation. Ask me if you find significant variations that need design decisions.
```

---

## Expected Deliverables

After the agent completes, you should have:

```
svclib/
├── go.mod
├── go.sum
├── README.md                    ← Main documentation
├── ANALYSIS.md                  ← Service comparison findings
├── MIGRATION.md                 ← How to integrate in services
├── PROMPT.md                    ← The extraction prompt
├── TODO.md                      ← Checklist
├── sentry-p1.md                 ← Part 1 docs
├── sentry-p2.md                 ← Part 2 docs
├── sentry.go                    ← Init() function
├── logging.go                   ← LogError()
├── middleware.go                ← TenantTaggingMiddleware()
├── interceptor.go               ← gRPC interceptors
├── span.go                      ← StartSpan()
├── context.go                   ← Context helpers
├── examples/
│   ├── basic/main.go
│   ├── with_performance/main.go
│   └── custom_tenant/main.go
└── internal/
    └── registry.go
```

---

## What Gets Extracted

### From `integration-service/utils/logging.go` → `svclib/logging.go`

```go
// Exported function
func LogError(ctx context.Context, label string, err error)
```

### From `integration-service/utils/sentry_tags.go` → `svclib/middleware.go`

```go
// Exported function
func TenantTaggingMiddleware(next http.Handler) http.Handler

// New variant for flexibility
func TenantTaggingMiddlewareWithExtractor(
    extractTenant func(*http.Request) (tenantID, subdomain string),
) func(http.Handler) http.Handler
```

### From `integration-service/utils/sentry_grpc.go` → Multiple files

**`svclib/interceptor.go`**:
```go
func UnaryClientInterceptor() grpc.UnaryClientInterceptor
func UnaryServerInterceptor() grpc.UnaryServerInterceptor
```

**`svclib/span.go`**:
```go
func StartSpan(ctx context.Context, spanName string) (context.Context, func(*error))
```

**`svclib/internal/registry.go`**:
```go
// Not exported - internal implementation detail
var spanRegistry = &sync.Map{}
```

### New: `svclib/context.go`

```go
// Exported context key
type TenantContextKey struct{}

// Helpers
func WithTenantID(ctx context.Context, tenantID string) context.Context
func GetTenantID(ctx context.Context) (string, bool)
```

### New: `svclib/sentry.go`

```go
type Config struct {
    DSN              string
    Environment      string
    TracesSampleRate float64
    EnableTracing    bool
}

func Init(cfg Config) (*sentryhttp.Handler, error)
```

---

## How Services Will Use It

### Before (integration-service current state)

```go
// utils/logging.go - 70 lines
// utils/sentry_tags.go - 50 lines  
// utils/sentry_grpc.go - 383 lines
// Total: 503 lines per service!
```

### After (with svclib)

```go
import "github.com/sambatechno/svclib"

func main() {
    // 1. Init (1 line)
    sentryHandler, _ := svclib.Init(svclib.Config{...})
    
    // 2. gRPC server (1 line)
    gs := grpc.NewServer(
        grpc.UnaryInterceptor(svclib.UnaryServerInterceptor()),
    )
    
    // 3. gRPC client (1 line)
    conn, _ := grpc.NewClient("...", 
        grpc.WithUnaryInterceptor(svclib.UnaryClientInterceptor()),
    )
    
    // 4. HTTP middleware (1 line)
    handler := sentryHandler.Handle(svclib.TenantTaggingMiddleware(mux))
    
    // 5. Context helper (1 line)
    ctx := svclib.WithTenantID(r.Context(), tenantID)
}

// In service code:
svclib.LogError(ctx, "Failed", err)
ctx, finish := svclib.StartSpan(ctx, "MyService.Method")
defer finish(&err)
```

**Total: ~10-15 lines of integration code!**

---

## Testing the Library

Once created, test it by updating integration-service:

### 1. Add dependency

```go
// integration-service/go.mod
require github.com/sambatechno/svclib v0.1.0

// Use local version for testing
replace github.com/sambatechno/svclib => ../svclib
```

### 2. Delete old files

```bash
cd ~/Work/cata/integration-service
rm utils/logging.go
rm utils/sentry_tags.go
rm utils/sentry_grpc.go
```

### 3. Update imports globally

Search & replace in integration-service:
- `utils.LogError` → `svclib.LogError`
- `utils.SentryTaggingMiddleware` → `svclib.TenantTaggingMiddleware`
- `utils.SentryUnaryServerInterceptor` → `svclib.UnaryServerInterceptor`
- `utils.SentryUnaryClientInterceptor` → `svclib.UnaryClientInterceptor`
- `utils.StartSpan` → `svclib.StartSpan`
- `utils.TenantContextKey` → `svclib.TenantContextKey`

### 4. Update context usage

```go
// Before
ctx := context.WithValue(r.Context(), utils.TenantContextKey{}, tenantId)
tenantId := utils.GetTenantIdFromCtx(ctx)

// After  
ctx := svclib.WithTenantID(r.Context(), tenantId)
tenantId, _ := svclib.GetTenantID(ctx)
```

### 5. Build and test

```bash
cd ~/Work/cata/integration-service
go mod tidy
go build ./...
go test ./...

# Test actual endpoint
curl http://localhost:8081/v1/luna/auth
# Check Sentry for trace
```

If it works → **Library is ready!** 🎉

---

## Key Success Metrics

✅ **integration-service** can drop-in replace with library  
✅ Other services can integrate in **< 30 minutes**  
✅ **No behavior changes** vs current implementation  
✅ **Flexible enough** for service variations  
✅ **Well documented** with working examples  

---

## Timeline

- **Analysis**: 1-2 hours
- **Extraction**: 2-3 hours
- **Documentation**: 1-2 hours
- **Testing**: 1 hour
- **Total**: ~6-8 hours for production-ready library

---

## When to Check Back

The agent should:
1. Ask you about significant variations found during analysis
2. Show you the proposed API design before implementing
3. Notify you when ready for testing

You should check:
- After analysis (review ANALYSIS.md)
- After API design (review proposed structure)
- After implementation (test with integration-service)

---

Good luck! The agent has all the information needed in `PROMPT.md`. 🚀

