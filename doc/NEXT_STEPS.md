# Next Steps: Creating svclib

## 🎯 Quick Overview

You're about to create `svclib` - a shared library that extracts common Sentry integration and service infrastructure code from your microservices. This will reduce 500+ lines of copy-paste code to ~10-15 lines of imports per service.

---

## 📋 Step-by-Step Checklist

### ✅ Step 1: Create the Repository (5 minutes)

```bash
cd ~/Work/cata
mkdir svclib
cd svclib

# Initialize Git and Go module
git init
go mod init github.com/sambatechno/svclib

# Copy documentation files
cp ../integration-service/sentry-p1.md .
cp ../integration-service/sentry-p2.md .
cp ../integration-service/SENTRYLIB_EXTRACTION_PROMPT.md ./PROMPT.md
cp ../integration-service/SENTRYLIB_TODO.md ./TODO.md
cp ../integration-service/SENTRYLIB_QUICK_REFERENCE.md ./QUICK_REFERENCE.md

# Verify files are copied
ls -la
```

**Expected output**:
```
svclib/
├── .git/
├── go.mod
├── sentry-p1.md
├── sentry-p2.md
├── PROMPT.md
├── TODO.md
└── QUICK_REFERENCE.md
```

---

### ✅ Step 2: Open Cursor in svclib Directory

```bash
cd ~/Work/cata/svclib
cursor .
```

Wait for Cursor to open the workspace.

---

### ✅ Step 3: Give the Agent the Extraction Prompt

1. **Open Agent Mode** in Cursor (press the agent button or use shortcut)

2. **Paste this prompt**:

```
Please read PROMPT.md and implement the svclib (service library) extraction project.

Key points:
1. Start by analyzing services in ~/Work/cata/*-service/ to understand variations
2. Extract code from ~/Work/cata/integration-service/utils/ (the reference implementation)
3. Create a flexible API that works across service variations
4. Focus on making integration simple (< 30 minutes per service)
5. Document everything thoroughly
6. IMPORTANT: Don't force services to migrate context keys - make TenantContextKey optional

The reference implementation is in ~/Work/cata/integration-service/ - that's the working code.

Follow the checklist in TODO.md and deliverables in PROMPT.md.

Start with analysis of 5+ services, then extraction, then documentation. Ask me if you find significant variations that need design decisions.

Bonus (if time permits): Include DefaultHeaderMatcher and ParseMockTenant - they're 100% identical across services.
```

3. **Press Enter** and let the agent work

---

### ✅ Step 4: Review Agent's Analysis (After ~1 hour)

The agent will create `ANALYSIS.md`. Review it and check:

- [ ] Does it identify the common patterns correctly?
- [ ] Does it note variations in tenant context handling?
- [ ] Does it propose a reasonable abstraction?

**If you see issues**, provide feedback to the agent.

**If it looks good**, let the agent continue to extraction.

---

### ✅ Step 5: Review Proposed API Design (After ~2 hours)

The agent should show you the proposed library structure. Check:

- [ ] Are the function signatures simple?
- [ ] Is `TenantContextKey` optional (not forced)?
- [ ] Does it support custom tenant extractors?
- [ ] Is it backward compatible with integration-service?

**Provide feedback** if needed, then let agent continue.

---

### ✅ Step 6: Wait for Completion (After ~6-8 hours)

The agent will:
- Extract all code
- Create documentation
- Create examples
- Run tests

**You'll know it's done when**:
- `go build ./...` succeeds
- README.md is complete
- MIGRATION.md has step-by-step guides
- Examples compile

---

### ✅ Step 7: Test the Library with integration-service (30 minutes)

Once the library is ready, test it:

#### A. Add Library Dependency

```bash
cd ~/Work/cata/integration-service

# Add to go.mod
echo 'require github.com/sambatechno/svclib v0.1.0' >> go.mod
echo 'replace github.com/sambatechno/svclib => ../svclib' >> go.mod

go mod tidy
```

#### B. Delete Old Files

```bash
# Backup first (optional)
mkdir .backup
cp utils/logging.go utils/sentry_tags.go utils/sentry_grpc.go .backup/

# Delete
rm utils/logging.go
rm utils/sentry_tags.go  
rm utils/sentry_grpc.go
```

#### C. Update Imports Globally

Use Find & Replace in Cursor:

| Find | Replace |
|------|---------|
| `utils.LogError` | `svclib.LogError` |
| `utils.SentryTaggingMiddleware` | `svclib.TenantTaggingMiddleware` |
| `utils.SentryUnaryServerInterceptor` | `svclib.UnaryServerInterceptor` |
| `utils.SentryUnaryClientInterceptor` | `svclib.UnaryClientInterceptor` |
| `utils.StartSpan` | `svclib.StartSpan` |
| `utils.TenantContextKey` | `svclib.TenantContextKey` |

#### D. Update main.go

**Before**:
```go
func initSentry() *sentryhttp.Handler {
    // ... environment logic ...
    if err := sentry.Init(sentry.ClientOptions{...}); err != nil {
        log.Printf("Sentry initialization failed: %v\n", err)
    }
    return sentryhttp.New(sentryhttp.Options{Repanic: true})
}

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
}
```

**After**:
```go
import "github.com/sambatechno/svclib"

func initSentry() *sentryhttp.Handler {
    // ... environment logic ...
    handler, err := svclib.Init(svclib.Config{
        DSN:              "https://...",
        Environment:      env,
        TracesSampleRate: tracing,
        EnableTracing:    true,
    })
    if err != nil {
        log.Printf("Sentry initialization failed: %v\n", err)
    }
    return handler
}

// Delete HeaderMatcher - use library's
// Delete mockArguments - use library's

func main() {
    utils.ReadConfig()
    sentryHandler := initSentry()
    mockedTenant := svclib.ParseMockTenant() // Use library helper

    // ... rest of main ...
    
    grpcMux := runtime.NewServeMux(
        runtime.WithIncomingHeaderMatcher(svclib.DefaultHeaderMatcher()), // Use library
    )
    
    // ... rest unchanged ...
}
```

#### E. Build and Test

```bash
cd ~/Work/cata/integration-service

# Build
go mod tidy
go build ./...

# If builds successfully, test actual endpoint
go run main.go

# In another terminal, test endpoint
curl http://localhost:8081/v1/luna/auth

# Check Sentry for trace - should work exactly as before!
```

---

### ✅ Step 8: Commit and Tag the Library

If testing is successful:

```bash
cd ~/Work/cata/svclib

# Commit
git add .
git commit -m "Initial release: Observability library for microservices

Features:
- Sentry distributed tracing (HTTP + gRPC)
- Context-aware error logging
- Tenant tagging middleware
- gRPC interceptors with span registry
- Performance span tracking (StartSpan)
- Common utilities (HeaderMatcher, ParseMockTenant)

Tested with integration-service ✅"

# Tag
git tag v0.1.0

# Push (if you have a remote repo)
# git remote add origin <url>
# git push origin main
# git push origin v0.1.0
```

---

### ✅ Step 9: Update integration-service to Use Remote Version

```bash
cd ~/Work/cata/integration-service

# Remove local replace directive from go.mod
# Change:
#   replace github.com/sambatechno/svclib => ../svclib
# To: (remove the replace line)

# If library is published to GitHub:
go get github.com/sambatechno/svclib@v0.1.0

# If still using local:
# Keep the replace directive

# Commit changes
git add .
git commit -m "Migrate to svclib for Sentry integration"
```

---

### ✅ Step 10: Rollout to Other Services (Repeat for each)

For each service (user-service, kds-management-service, etc.):

**Time per service**: 30-60 minutes

```bash
cd ~/Work/cata/<service-name>

# 1. Add dependency
echo 'require github.com/sambatechno/svclib v0.1.0' >> go.mod
# If local: echo 'replace github.com/sambatechno/svclib => ../svclib' >> go.mod
go mod tidy

# 2. Add import to main.go
# Add: import "github.com/sambatechno/svclib"

# 3. Update initSentry() - use svclib.Init()
# 4. Update middleware - use svclib.TenantTaggingMiddleware()
# 5. Update interceptors - use svclib.UnaryServerInterceptor(), svclib.UnaryClientInterceptor()
# 6. Replace HeaderMatcher with svclib.DefaultHeaderMatcher()
# 7. Replace mockArguments with svclib.ParseMockTenant()
# 8. Add svclib.LogError where needed

# 9. Build and test
go build ./...
go test ./...

# 10. Test actual service
go run main.go
# Test endpoints, check Sentry

# 11. Commit
git add .
git commit -m "Integrate svclib for observability"
```

---

## 📊 Timeline

| Phase | Time | Status |
|-------|------|--------|
| **Step 1-3**: Setup & Start Agent | 10 min | ⏳ Todo |
| **Step 4**: Agent Analysis | 1-2 hours | ⏳ Todo |
| **Step 5**: Agent Extraction | 2-3 hours | ⏳ Todo |
| **Step 6**: Agent Documentation | 1-2 hours | ⏳ Todo |
| **Step 7**: Test with integration-service | 30 min | ⏳ Todo |
| **Step 8-9**: Commit & Tag | 15 min | ⏳ Todo |
| **Step 10**: Rollout to other services | 30-60 min each | ⏳ Todo |
| **TOTAL** | ~6-10 hours | ⏳ Todo |

---

## ✅ What Success Looks Like

### Before (Current State)

Each service has:
- `utils/logging.go` (70 lines)
- `utils/sentry_tags.go` (50 lines)
- `utils/sentry_grpc.go` (383 lines)
- `initSentry()` function (20 lines)
- `HeaderMatcher()` function (8 lines)
- `mockArguments()` function (5 lines)

**Total**: ~536 lines of duplicated code per service

### After (With svclib)

Each service has:
```go
import "github.com/sambatechno/svclib"

func main() {
    handler, _ := svclib.Init(svclib.Config{...})  // 5 lines
    mockedTenant := svclib.ParseMockTenant()        // 1 line
    
    grpcMux := runtime.NewServeMux(
        runtime.WithIncomingHeaderMatcher(svclib.DefaultHeaderMatcher()),  // 1 line
    )
    
    gs := grpc.NewServer(
        grpc.UnaryInterceptor(svclib.UnaryServerInterceptor()),  // 1 line
    )
    
    conn, _ := grpc.NewClient("...",
        grpc.WithUnaryInterceptor(svclib.UnaryClientInterceptor()),  // 1 line
    )
    
    mux := sentryHandler.Handle(svclib.TenantTaggingMiddleware(handler))  // 1 line
}

// In code:
svclib.LogError(ctx, "error", err)  // 1 line
ctx, finish := svclib.StartSpan(ctx, "op")  // 1 line
```

**Total**: ~12-15 lines of integration code

**Savings**: 520+ lines per service!

---

## 🚨 Potential Issues & Solutions

### Issue 1: "Agent finds too many variations"

**Solution**: Ask agent to use custom extractor pattern for variations:
```go
svclib.TenantTaggingMiddlewareWithExtractor(func(r *http.Request) (tenantID, subdomain string) {
    // Custom extraction logic
})
```

### Issue 2: "Context keys conflict"

**Solution**: Library's `TenantContextKey` is optional. Services can keep their own keys and provide custom extractors.

### Issue 3: "Tests fail after migration"

**Solution**: Check that all `utils.*` calls are replaced with `svclib.*`. Use grep:
```bash
grep -r "utils\.LogError\|utils\.StartSpan\|utils\.Sentry" .
```

### Issue 4: "Sentry traces don't show up"

**Solution**: Verify interceptors are added:
- gRPC server: `svclib.UnaryServerInterceptor()`
- gRPC client: `svclib.UnaryClientInterceptor()`
- HTTP middleware: `svclib.TenantTaggingMiddleware()`

---

## 📞 When to Ask for Help

Ask the agent for clarification if:
1. ANALYSIS.md shows unexpected variations
2. Proposed API seems too complex
3. Tests fail after extraction
4. Context key handling is unclear

The agent has all the information in PROMPT.md - just reference specific sections!

---

## 🎉 Done!

When all steps are complete:
- ✅ Library exists and compiles
- ✅ integration-service uses it successfully
- ✅ Ready to roll out to other services
- ✅ Documentation is complete

**Next**: Roll out to user-service, kds-management-service, and others incrementally!

---

**Estimated total time**: 8-12 hours (including rollout to 3-5 services)

**Maintenance effort**: Near zero - bug fixes in one place benefit all services!

Good luck! 🚀

