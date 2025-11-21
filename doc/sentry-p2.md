# Sentry Integration Documentation

## Part 2: Granular Performance Tracing with StartSpan

### Prerequisites

✅ **Part 1 must be completed first**

This part builds on top of Part 1 (Distributed Tracing for HTTP + gRPC Gateway). You should have:
- `utils/logging.go` with context-aware `LogError`
- `utils/sentry_tags.go` with `SentryTaggingMiddleware`
- `utils/sentry_grpc.go` with interceptors
- Working HTTP → gRPC trace propagation
- Errors properly linked to traces

Part 1 gives you **one trace per request** showing HTTP and gRPC spans. Part 2 adds **detailed performance breakdown** within your service methods.

---

## Overview

Part 2 adds a unified `StartSpan` helper that instruments service methods for detailed performance observability. This helps you:

- **Identify slow operations** - See which database queries, API calls, or business logic take the longest
- **Visualize call hierarchy** - Understand the flow through your service layers
- **Track operation success/failure** - Automatic error capture linked to specific operations
- **Monitor goroutine performance** - Optional detailed tracing for background tasks

**What you get**: From a basic 2-span trace to a detailed 10+ span breakdown showing exactly where time is spent.

---

## Problem Statement

### What Part 1 Gives You

After Part 1, your Sentry traces look like this:

```
http.server GET /v1/luna/auth (500ms)
└── grpc.server /LightspeedService/GetAuthStatus (450ms)
```

This shows the request took 500ms total, with 450ms in the gRPC handler. But **what happened in those 450ms?**

### What Part 2 Adds

With Part 2 instrumentation, you see the detailed breakdown:

```
http.server GET /v1/luna/auth (500ms)
└── grpc.server /LightspeedService/GetAuthStatus (450ms)
    └── LightspeedService.GetAuthStatus (445ms)
        ├── LightspeedService.getClientCredentials (15ms)
        │   └── database.query (12ms)
        ├── LightspeedService.getAuthTokenWithTransaction (80ms)
        │   ├── database.transaction (60ms)
        │   └── LightspeedRepo.RefreshToken (20ms)
        ├── LightspeedRepo.GetBusinesses (200ms) ← Slow external API!
        └── LightspeedRepo.GetWebhook (150ms)
```

Now you can see: **the external API call to get businesses is the bottleneck!**

---

## Implementation Guide

### 1. Add `StartSpan` Function to `utils/sentry_grpc.go`

Add this function to your existing `utils/sentry_grpc.go` file (after the existing interceptor functions):

```go
// StartSpan creates a child span for the given operation.
// This is a unified helper that works for both synchronous code (service methods)
// and asynchronous code (goroutines).
//
// For service methods (synchronous):
//
//	func (s *Server) MyMethod(ctx context.Context, req *pb.Request) (resp *pb.Response, err error) {
//	    ctx, finish := utils.StartSpan(ctx, "ServiceName.MyMethod")
//	    defer finish(&err)
//	    // ... your code using the returned ctx ...
//	    return &pb.Response{}, nil
//	}
//
// For goroutines (asynchronous):
//
//	go func() {
//	    ctx, finish := utils.StartSpan(ctx, "background.processing")
//	    defer finish(nil) // or defer finish(&err) if you want to track errors
//	    // ... your async work ...
//	}()
//
// The function automatically:
// - Creates a child span linked to the parent
// - Handles hub cloning for goroutines (detects context.Background())
// - Sets span status based on error (OK or InternalError)
// - Captures errors to Sentry if error pointer is provided
//
// Returns the new context and a finish function that accepts an optional error pointer.
func StartSpan(ctx context.Context, spanName string) (context.Context, func(*error)) {
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		// No Sentry context, return noop
		return ctx, func(*error) {}
	}

	// Detect if we're in a goroutine context (context.Background or no existing span chain)
	// For goroutines, we need to clone the hub to avoid race conditions
	isGoroutine := false
	if ctx == context.Background() {
		isGoroutine = true
	}

	if isGoroutine {
		hub = hub.Clone()
	}

	// Get parent span from context
	var parentSpan *sentry.Span
	if span, ok := ctx.Value(grpcSpanContextKey{}).(*sentry.Span); ok {
		parentSpan = span
	} else {
		parentSpan = sentry.SpanFromContext(ctx)
	}

	// Create child span
	var childSpan *sentry.Span
	if parentSpan != nil {
		childSpan = parentSpan.StartChild(spanName)
	} else {
		childSpan = sentry.StartSpan(ctx, spanName)
	}
	childSpan.Description = spanName

	// Create new context with hub and span
	newCtx := ctx
	if isGoroutine {
		newCtx = context.Background()
		// Copy tenant ID if present
		if tenantID, ok := ctx.Value(TenantContextKey{}).(string); ok {
			newCtx = context.WithValue(newCtx, TenantContextKey{}, tenantID)
		}
	}

	newCtx = sentry.SetHubOnContext(newCtx, hub)
	newCtx = context.WithValue(newCtx, grpcSpanContextKey{}, childSpan)
	newCtx = context.WithValue(newCtx, sentryTraceContextKey{}, childSpan.TraceID.String())

	// Configure hub scope to use this span
	hub.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetSpan(childSpan)
	})

	// Return finish function that accepts optional error pointer
	finish := func(errPtr *error) {
		if errPtr != nil && *errPtr != nil {
			childSpan.Status = sentry.SpanStatusInternalError
			// Capture the error linked to this span
			hub.WithScope(func(scope *sentry.Scope) {
				scope.SetSpan(childSpan)
				scope.SetContext("operation", map[string]interface{}{
					"name": spanName,
				})
				hub.CaptureException(*errPtr)
			})
		} else {
			childSpan.Status = sentry.SpanStatusOK
		}
		childSpan.Finish()
	}

	return newCtx, finish
}
```

**Key points**:
- Works for both synchronous (service methods) and asynchronous (goroutines) code
- Auto-detects goroutine context and clones hub to avoid race conditions
- Accepts `*error` to automatically set span status and capture errors
- Returns new context that must be used for subsequent operations

---

### 2. Instrument Service Methods

Add `StartSpan` to your public service methods. Use this pattern:

**Before**:
```go
func (s *server) GetAuthStatus(ctx context.Context, req *pb.NoRequest) (*pb.LsGetAuthStatusResponse, error) {
    // ... implementation ...
    return &pb.LsGetAuthStatusResponse{}, nil
}
```

**After**:
```go
func (s *server) GetAuthStatus(ctx context.Context, req *pb.NoRequest) (resp *pb.LsGetAuthStatusResponse, err error) {
    ctx, finish := utils.StartSpan(ctx, "LightspeedService.GetAuthStatus")
    defer finish(&err)
    
    // ... implementation using ctx ...
    return &pb.LsGetAuthStatusResponse{}, nil
}
```

**Important**:
1. **Use named return values** (`resp`, `err`) - required for `defer finish(&err)` to work
2. **Use the returned `ctx`** - pass it to all subsequent calls to maintain the span hierarchy
3. **Naming convention**: `ServiceName.MethodName` for clarity in Sentry

---

### 3. Instrumentation Strategy

#### Start Small: Instrument One Endpoint End-to-End

Pick one important endpoint and instrument the entire call chain. Example for `GetAuthStatus`:

1. **Handler method** (entry point):
   ```go
   func (s *server) GetAuthStatus(ctx context.Context, req *pb.NoRequest) (resp *pb.LsGetAuthStatusResponse, err error) {
       ctx, finish := utils.StartSpan(ctx, "LightspeedService.GetAuthStatus")
       defer finish(&err)
       // ...
   }
   ```

2. **Helper methods** (database, validation):
   ```go
   func (s *server) getClientCredentials(ctx context.Context) (creds *credentials, err error) {
       ctx, finish := utils.StartSpan(ctx, "LightspeedService.getClientCredentials")
       defer finish(&err)
       // ...
   }
   ```

3. **External API calls** (repository layer):
   ```go
   func (r *LightspeedApiRepository) GetBusinesses(ctx context.Context, accessToken string) (businesses []Business, err error) {
       ctx, finish := utils.StartSpan(ctx, "LightspeedRepo.GetBusinesses")
       defer finish(&err)
       // ...
   }
   ```

**Test** the endpoint and verify the Sentry trace shows the full hierarchy.

#### Then Expand

Once one endpoint works well:
- Instrument other endpoints in the same service
- Instrument commonly called helper methods
- Instrument repository/database layer
- Add to other services following the same pattern

**Priority order**:
1. High-traffic endpoints (80% of your requests)
2. Slow or problematic endpoints (where you need visibility)
3. Critical business operations (payments, orders, auth)
4. Background workers and cron jobs

---

### 4. Optional: Instrument Goroutines for Performance Tracking

By default (from Part 1), errors in goroutines are automatically linked to the parent trace. But if you want **performance tracking** of the goroutine itself, use `StartSpan`:

```go
// Without StartSpan: Errors are linked, but no performance span
go func() {
    if err := sendEmail(ctx, email); err != nil {
        utils.LogError(ctx, "Failed to send email", err) // Linked to trace ✓
    }
}()

// With StartSpan: Errors are linked AND you get a performance span
go func() {
    ctx, finish := utils.StartSpan(ctx, "background.send_email")
    defer finish(nil)
    
    if err := sendEmail(ctx, email); err != nil {
        utils.LogError(ctx, "Failed to send email", err) // Linked to span ✓
    }
}()
```

**When to use StartSpan in goroutines**:
- ✅ Long-running background processing you want to measure
- ✅ Complex async workflows with multiple steps
- ✅ Performance-critical goroutines
- ❌ Simple fire-and-forget notifications (errors auto-link anyway)

---

## Usage Patterns

### Pattern 1: Simple Service Method

```go
func (s *server) EnableOfflineOrder(ctx context.Context, req *pb.EnableOfflineOrderRequest) (resp *pb.EnableOfflineOrderResponse, err error) {
    ctx, finish := utils.StartSpan(ctx, "LightspeedService.EnableOfflineOrder")
    defer finish(&err)
    
    // Validate
    if req.BusinessLocationId == "" {
        return nil, fmt.Errorf("business_location_id is required")
    }
    
    // Save to database
    err = s.DB.Tenant().UpdateSettings(ctx, "business_location_id", req.BusinessLocationId)
    if err != nil {
        return nil, err // Captured by finish(&err)
    }
    
    return &pb.EnableOfflineOrderResponse{Success: true}, nil
}
```

### Pattern 2: Method Calling Other Methods

```go
func (s *server) SyncMenus(ctx context.Context, req *pb.SyncRequest) (resp *pb.SyncResponse, err error) {
    ctx, finish := utils.StartSpan(ctx, "LightspeedService.SyncMenus")
    defer finish(&err)
    
    // Each of these creates a child span
    token, err := s.getAuthToken(ctx)
    if err != nil {
        return nil, err
    }
    
    menus, err := s.Repo.GetMenus(ctx, token)
    if err != nil {
        return nil, err
    }
    
    err = s.saveMenusToDatabase(ctx, menus)
    if err != nil {
        return nil, err
    }
    
    return &pb.SyncResponse{Count: len(menus)}, nil
}

func (s *server) getAuthToken(ctx context.Context) (token string, err error) {
    ctx, finish := utils.StartSpan(ctx, "LightspeedService.getAuthToken")
    defer finish(&err)
    // ...
}

func (s *server) saveMenusToDatabase(ctx context.Context, menus []Menu) (err error) {
    ctx, finish := utils.StartSpan(ctx, "LightspeedService.saveMenusToDatabase")
    defer finish(&err)
    // ...
}
```

**Result in Sentry**:
```
SyncMenus (200ms)
├── getAuthToken (50ms)
├── GetMenus (100ms)
└── saveMenusToDatabase (45ms)
```

### Pattern 3: Repository/API Layer

```go
func (r *LightspeedApiRepository) CreateWebhook(ctx context.Context, accessToken string, request *WebhookCreateRequest) (webhook *WebhookResponse, err error) {
    ctx, finish := utils.StartSpan(ctx, "LightspeedRepo.CreateWebhook")
    defer finish(&err)
    
    host, err := r.getLightspeedHost(ctx)
    if err != nil {
        return nil, err
    }
    
    var result WebhookResponse
    err = r.apiClient.Post(ctx, host+webhookEndpoint).
        BearerAuth(accessToken).
        JSONBody(request).
        DoJSON(&result)
    
    if err != nil {
        return nil, fmt.Errorf("failed to create webhook: %w", err)
    }
    
    return &result, nil
}
```

### Pattern 4: Background Goroutine with Performance Tracking

```go
func (s *server) ProcessOrder(ctx context.Context, req *pb.OrderRequest) (*pb.OrderResponse, error) {
    // Process order synchronously
    order := s.createOrder(req)
    
    // Send notifications in background with performance tracking
    go func() {
        ctx, finish := utils.StartSpan(ctx, "background.order_notifications")
        defer finish(nil)
        
        s.sendCustomerNotification(ctx, order)
        s.sendMerchantNotification(ctx, order)
        s.updateAnalytics(ctx, order)
    }()
    
    return &pb.OrderResponse{OrderId: order.ID}, nil
}
```

---

## Expected Results

### Before Part 2 (Part 1 Only)

```
http.server GET /v1/luna/auth (500ms)
└── grpc.server /LightspeedService/GetAuthStatus (450ms)
```

**Limited visibility**: You know the handler took 450ms, but not why.

### After Part 2 (Full Instrumentation)

```
http.server GET /v1/luna/auth (500ms)
└── grpc.server /LightspeedService/GetAuthStatus (450ms)
    └── LightspeedService.GetAuthStatus (445ms)
        ├── LightspeedService.getClientCredentials (15ms)
        │   └── database.GetIntegrationPartyCreds (12ms)
        ├── database.GetSettings (5ms)
        ├── LightspeedService.getAuthTokenWithTransaction (80ms)
        │   ├── database.transaction.begin (5ms)
        │   ├── LightspeedService.shouldRefreshToken (2ms)
        │   ├── LightspeedRepo.RefreshAccessToken (60ms)
        │   └── database.transaction.commit (3ms)
        ├── LightspeedRepo.GetBusinesses (200ms)
        ├── database.GetSettings (5ms)
        └── LightspeedRepo.GetWebhook (140ms)
```

**Full visibility**: You can see:
- `GetBusinesses` API call (200ms) is the slowest operation
- Token refresh (60ms) happens inside the transaction
- Database queries are fast (5-15ms each)
- Total time matches: 15 + 5 + 80 + 200 + 5 + 140 = 445ms ✓

---

## Verification

### 1. Build and Test

```bash
cd /path/to/integration-service
go build ./...
```

### 2. Make a Request

Call an instrumented endpoint:

```bash
curl -H "Authorization: Bearer YOUR_TOKEN" \
     http://localhost:8081/v1/luna/auth
```

### 3. Check Sentry

Go to Sentry → Performance → Transactions → Find your transaction

**Verify**:
- ✅ You see more than just `http.server` and `grpc.server`
- ✅ Service method spans appear (e.g., `LightspeedService.GetAuthStatus`)
- ✅ Repository method spans appear (e.g., `LightspeedRepo.GetBusinesses`)
- ✅ Spans are nested correctly (parent-child relationships)
- ✅ Timings add up (child spans ≤ parent span duration)
- ✅ Errors show on the specific span where they occurred

---

## Migration Checklist

- [ ] Add `StartSpan` function to `utils/sentry_grpc.go`
- [ ] Pick one endpoint to instrument fully
- [ ] Instrument the handler method
- [ ] Instrument all methods it calls
- [ ] Instrument repository/API layer methods
- [ ] Test and verify in Sentry
- [ ] Expand to other high-traffic endpoints
- [ ] Instrument background workers if needed
- [ ] Document which endpoints are instrumented

---

## Common Patterns by Service Size

### Small Service (< 10 endpoints)

Instrument everything:
- All public gRPC handlers
- All repository methods
- Major helper functions

**Time**: 2-3 hours

### Medium Service (10-30 endpoints)

Start with top endpoints:
- Top 5 high-traffic endpoints (by request volume)
- Top 3 problematic endpoints (by error rate or latency)
- Shared utilities and repository layer

**Time**: 1-2 days

### Large Service (30+ endpoints)

Phased approach:
- **Week 1**: Critical path (auth, payments, orders)
- **Week 2**: High-traffic reads (menus, products, settings)
- **Week 3**: Background jobs and sync operations
- **Week 4**: Admin/internal tools

**Time**: 3-4 weeks

---

## Best Practices

### 1. Span Naming Convention

Use consistent, hierarchical names:

```
ServiceName.MethodName
ServiceName.helperMethod
RepoName.MethodName
component.operation
```

**Good**:
- `LightspeedService.GetAuthStatus`
- `LightspeedRepo.GetBusinesses`
- `database.transaction`
- `background.send_notifications`

**Bad**:
- `getAuthStatus` (no service name)
- `Lightspeed API Call` (too generic)
- `method_1` (not descriptive)

### 2. Always Use Named Return Values

**Required**:
```go
func MyMethod(ctx context.Context) (result *Data, err error) {
    ctx, finish := utils.StartSpan(ctx, "Service.MyMethod")
    defer finish(&err) // ✓ Works because err is named
    // ...
}
```

**Won't work**:
```go
func MyMethod(ctx context.Context) (*Data, error) {
    ctx, finish := utils.StartSpan(ctx, "Service.MyMethod")
    defer finish(&err) // ✗ err is undefined
    // ...
}
```

### 3. Pass the Returned Context

**Correct**:
```go
func Parent(ctx context.Context) error {
    ctx, finish := utils.StartSpan(ctx, "Parent")
    defer finish(nil)
    
    return Child(ctx) // ✓ Pass the NEW context
}
```

**Incorrect**:
```go
func Parent(ctx context.Context) error {
    _, finish := utils.StartSpan(ctx, "Parent")
    defer finish(nil)
    
    return Child(ctx) // ✗ Uses OLD context, breaks span hierarchy
}
```

### 4. Don't Over-Instrument

**Instrument**:
- ✅ Service layer methods (business logic)
- ✅ Repository layer (database, external APIs)
- ✅ Expensive operations (file I/O, crypto, parsing)

**Don't instrument**:
- ❌ Simple getters/setters
- ❌ Pure functions with no I/O
- ❌ Trivial helpers (< 1ms execution time)

**Example**:
```go
// DON'T instrument this
func (s *server) buildResponse(data *Data) *pb.Response {
    return &pb.Response{Data: data}
}

// DO instrument this
func (s *server) getDataFromDatabase(ctx context.Context, id string) (*Data, error) {
    ctx, finish := utils.StartSpan(ctx, "Service.getDataFromDatabase")
    defer finish(&err)
    // ...
}
```

---

## Troubleshooting

### Issue: Spans not appearing in Sentry

**Possible causes**:
1. `StartSpan` function not added to `utils/sentry_grpc.go`
2. Not using returned context (using original `ctx` instead)
3. Part 1 not completed (no hub in context)

**Solution**: Verify Part 1 is working first (you should see `http.server` and `grpc.server` spans).

### Issue: Flat spans instead of nested hierarchy

**Symptom**: All spans appear at the same level instead of nested.

**Cause**: Not passing the returned context to child operations.

**Fix**:
```go
// Wrong
ctx, finish := utils.StartSpan(ctx, "Parent")
defer finish(nil)
s.childMethod(ctx) // ✗ Original ctx

// Correct
ctx, finish := utils.StartSpan(ctx, "Parent")
defer finish(nil)
s.childMethod(ctx) // ✓ Returned ctx (note: same variable name)
```

### Issue: Errors not captured

**Cause**: Not passing error pointer to `finish()`.

**Fix**:
```go
// Wrong
func Method(ctx context.Context) (result *Data, err error) {
    ctx, finish := utils.StartSpan(ctx, "Method")
    defer finish(nil) // ✗ Errors not captured
    // ...
}

// Correct
func Method(ctx context.Context) (result *Data, err error) {
    ctx, finish := utils.StartSpan(ctx, "Method")
    defer finish(&err) // ✓ Errors captured
    // ...
}
```

---

## Performance Considerations

### Overhead

Each `StartSpan` call adds ~0.1-0.5ms overhead:
- Creating span object: ~0.1ms
- Context manipulation: ~0.1ms
- Finishing span: ~0.3ms

**Total**: ~0.5ms per span

**Example**: 20 spans = 10ms overhead (acceptable for most applications)

### Sampling

Control how many traces are sent to Sentry:

```go
// In initSentry()
sentry.Init(sentry.ClientOptions{
    TracesSampleRate: 0.1, // 10% of requests
})
```

**Recommendations**:
- **Local/Dev**: 1.0 (100%) - full visibility for debugging
- **Staging**: 0.5 (50%) - balance between visibility and quota
- **Production**: 0.1 (10%) - reduce costs while maintaining insight

Even with 10% sampling, you'll see patterns and identify slow operations.

---

## Summary

### What Part 2 Adds

- `StartSpan` function for creating performance spans
- Detailed breakdown of where time is spent in service methods
- Automatic error capture linked to specific operations
- Optional performance tracking for goroutines

### Key Benefits

1. **Find bottlenecks**: See which operations are slow
2. **Understand flow**: Visualize the call hierarchy
3. **Track errors precisely**: Know exactly where errors occur
4. **Optimize effectively**: Data-driven performance improvements

### Remember

- Part 2 is **optional** - Part 1 already gives you error tracking and basic tracing
- Part 2 is **incremental** - start with one endpoint, expand gradually
- Part 2 is **stackable** - each instrumented method adds more visibility

---

**Last Updated**: November 21, 2025  
**Sentry SDK Version**: v0.38.0  
**Prerequisites**: Part 1 Complete ✅  
**Status**: Part 2 Ready for Implementation

