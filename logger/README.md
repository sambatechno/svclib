# logger

Structured JSON logger for Cloud Run with automatic stack tracing, Sentry error
tracking, and trace-ID correlation.

This is the logger from `kds-management-service` (`project/pkg/logger`), moved
into svclib. **The API is identical** — call sites move over by changing the
import only:

```diff
-import "project/pkg/logger"
+import "github.com/sambatechno/svclib/logger"
```

What svclib adds on top: every entry logged through a logger bound to a context
carries the distributed `trace_id` / `span_id`, and `Error()` reports on that
context's Sentry hub and span, so the event lands inside the same trace as the
request instead of as a free-floating error.

## Output Format

All logs are written as JSON to stdout, recognized by Cloud Run / Cloud Logging:

```json
{
  "severity": "ERROR",
  "message": "SharedService: capturePayment",
  "timestamp": "2026-03-25T10:00:00Z",
  "trace": "error in SharedService > CompleteOrder > capturePayment > ...: unexpected http status: 520",
  "trace_id": "d4cda95b652f4a1592b449d5929fda1b",
  "span_id": "1e5e29a5b4b9c1f2",
  "logging.googleapis.com/trace": "projects/cata-prod/traces/d4cda95b652f4a1592b449d5929fda1b",
  "logging.googleapis.com/spanId": "1e5e29a5b4b9c1f2",
  "data": {
    "tenant_id": "b9565b91-...",
    "subdomain": "gyg",
    "order_uuid": "316a0ffa-...",
    "error": "unexpected http status: 520"
  }
}
```

| Field | Meaning |
|---|---|
| `trace` | Application **stack trace** — `error in A > B > ctx: err`. |
| `trace_id` / `span_id` | **Distributed trace** identifiers, shared with Sentry. |
| `logging.googleapis.com/trace` / `spanId` | Cloud Logging correlation; emitted only when a project ID is configured. |

The `severity` field maps directly to Cloud Logging levels:

| Method  | Severity  | Cloud Logging Level | Stack Trace | Sentry |
|---------|-----------|---------------------|-------------|--------|
| `Info`  | `INFO`    | Info (blue)         | No          | No     |
| `Warn`  | `WARNING` | Warning (yellow)    | Yes         | No     |
| `Error` | `ERROR`   | Error (red)         | Yes         | Yes    |
| `Debug` | `DEBUG`   | Debug               | No          | No     |

## Usage

### Initialize

Logger is initialized once per service:

```go
import "github.com/sambatechno/svclib/logger"

// In Controller.go
type server struct {
    Logger logger.ILogger
}

func NewService(...) *server {
    return &server{
        Logger: logger.New("SharedService"),
    }
}
```

### WithContext (gRPC + tracing)

Binds the logger to the request context. From then on every entry carries the
trace ID of the current span, and `Error()` reports on that context's hub.

It also extracts `tenant_id` and `subdomain` — from the forwarded gRPC metadata
(`fwd-x-tenant-id`, `fwd-x-sub-domain`), falling back to the tenant stored by
`svclib.WithTenantID`.

```go
func (s *server) SyncMenu(ctx context.Context, req *pb.SyncMenuRequest) {
    log := s.Logger.WithContext(ctx)
    log.Info("sync started")
    log.Error("sync failed", nil, err)
}
```

The trace ID comes from whatever svclib already established:

- `UnaryServerInterceptor()` — the gRPC handler span (continues the HTTP trace).
- `StartSpan(ctx, "name")` — the child span, including inside goroutines.
- Any context carrying a Sentry span.

```go
go func() {
    ctx, finish := svclib.StartSpan(ctx, "background.processing")
    defer finish(nil)

    log := s.Logger.WithContext(ctx) // same trace_id as the request that spawned it
    log.Info("processing in background")
}()
```

`WithFields` preserves the bound context, so ordering does not matter:

```go
log := s.Logger.WithContext(ctx).WithFields(map[string]any{"order_uuid": id})
```

### Goroutines and background contexts

A logger bound with `WithContext` keeps the context it was given, so passing it
into a goroutine or a helper keeps `trace_id`, `tenant_id` and every field —
even after the handler returned. Concurrent use is safe: each `Error()` reports
on its own clone of the hub, so tags never leak between goroutines.

What loses the trace is starting a fresh context in the callee
(`WithContext(context.Background())`): there is nothing on it to read. Pass the
request context down, or re-scope the goroutine with `StartSpan` to give the
background work its own span:

```go
go func() {
    ctx, finish := svclib.StartSpan(ctx, "background.processing")
    defer finish(nil)

    log := s.Logger.WithContext(ctx) // own span, same trace_id as the request
    log.Info("working")
}()
```

See [Tested behaviour](#tested-behaviour) for each case with its real output.

### WithFields (manual)

For functions without a context (e.g. SharedService order functions):

```go
func (s *server) AcceptOrder(request structs.UpdateStatusOrderRequest) {
    log := s.Logger.WithFields(map[string]any{
        "tenant_id":  request.TenantId,
        "subdomain":  request.Subdomain,
        "order_uuid": request.OrderUuid,
    })
    log.Error("AcceptOrder", nil, err)
}
```

### Info

```go
log.Info("order created")
log.Info("order created", map[string]any{"order_id": "123"})
```

### Warn

For non-critical issues that don't need Sentry (e.g. `canTransition=false`). Includes stack trace.

```go
log.Warn("cannot change order status", fmt.Errorf("from %s to %s", current, desired))
```

### Error

For errors that need Sentry reporting. Includes stack trace.

```go
// Without tags
log.Error("AcceptOrder", nil, err)

// With Sentry tags
log.Error("capturePayment", map[string]string{
    "provider": "xendit",
}, err)
```

**Rules:**
- Use `Error` at top-level callers (AcceptOrder, CancelOrder, etc.) to report to Sentry
- Use `Error` in fire-and-forget functions where errors don't bubble up (capturePayment, callPostOrder, etc.)
- Use `Warn` for non-critical warnings (canTransition=false) — no Sentry, only Cloud Logging
- Internal functions that return errors to caller: no logger needed, error bubbles up to top-level

### Debug

Only outputs when `APP_DEBUG=true`. Payload is JSON-stringified automatically.

```go
log.Debug("request payload", requestBody)
```

## Tested behaviour

Every case below was run against this package; the output shown is the real
output, trimmed only of the trailing `logging.googleapis.com/*` fields where
they repeat `trace_id` / `span_id`. All of them use the same call site:

```go
log := s.Logger.WithContext(ctx)
log.Info("sync started")
log.Error("sync failed", nil, err)
```

| Case | `trace_id` in logs | `tenant_id` in logs | Sentry event | Linked to the request trace |
|---|---|---|---|---|
| 1. Bound to the request context (base case) | ✅ | ✅ | ✅ | ✅ |
| 2. Goroutine, logger never bound to a context | ❌ | ❌ | ✅ | ❌ unrelated trace |
| 3. Goroutine binding a fresh `context.Background()` | ❌ | ❌ | ✅ | ❌ unrelated trace |
| 4. Goroutine reusing the bound logger (request already finished) | ✅ | ✅ | ✅ | ✅ same span |
| 5. Goroutine re-scoped with `StartSpan` | ✅ | ✅ | ✅ | ✅ child span |
| 6. Several goroutines sharing one bound logger | ✅ | ✅ | ✅ | ✅ tags stay per call |
| 7. Service that never initialized Sentry | ❌ | ✅ | ⚪ dropped | — |

---

### Case 1 — base case (bound to the request context)

```go
log := s.Logger.WithContext(ctx) // ctx from the gRPC handler / interceptor
log.Info("sync started")
log.Error("sync failed", nil, fmt.Errorf("connection refused"))
```

Request span: `trace_id=e136710525abbba5e773799b29eb0341 span_id=24e153d8c50612c0`

```json
{"severity":"INFO","message":"sync started","timestamp":"2026-08-22T12:34:51Z","trace_id":"e136710525abbba5e773799b29eb0341","span_id":"24e153d8c50612c0","logging.googleapis.com/trace":"projects/cata-prod/traces/e136710525abbba5e773799b29eb0341","logging.googleapis.com/spanId":"24e153d8c50612c0","data":{"tenant_id":"b9565b91"}}
{"severity":"ERROR","message":"sync failed: connection refused","timestamp":"2026-08-22T12:34:51Z","trace":"error > sync failed: connection refused","trace_id":"e136710525abbba5e773799b29eb0341","span_id":"24e153d8c50612c0","data":{"error":"connection refused","tenant_id":"b9565b91"}}
```

Sentry — 1 event, on the request trace:

```
tags  = {tenant_id: b9565b91, trace_id: e136710525abbba5e773799b29eb0341, transaction: "SharedService > sync failed"}
trace = {trace_id: e136710525abbba5e773799b29eb0341}
```

### Case 2 — inside a goroutine, without a context

```go
go func() {
    log := s.Logger // never bound with WithContext
    log.Info("sync started")
    log.Error("sync failed", nil, fmt.Errorf("connection refused"))
}()
```

```json
{"severity":"INFO","message":"sync started","timestamp":"2026-08-22T12:34:51Z"}
{"severity":"ERROR","message":"sync failed: connection refused","timestamp":"2026-08-22T12:34:51Z","trace":"error in main > 1 > sync failed: connection refused","data":{"error":"connection refused"}}
```

Sentry — 1 event, but **not** on the request trace:

```
tags  = {transaction: "SharedService > sync failed"}          <- no trace_id, no tenant_id
trace = {trace_id: 0b3f4949a0efd8112146e7c65823c5df, ...}     <- auto-generated, unrelated
```

The log line still reaches Cloud Logging with its severity, message and stack
trace, and the error still reaches Sentry — but nothing ties either of them back
to the request. Searching Sentry or Cloud Logging by the request's trace ID will
not find them.

### Case 3 — inside a goroutine, with a new `context.Background()`

```go
go func() {
    log := s.Logger.WithContext(context.Background()) // fresh context
    log.Info("sync started")
    log.Error("sync failed", nil, fmt.Errorf("connection refused"))
}()
```

```json
{"severity":"INFO","message":"sync started","timestamp":"2026-08-22T12:34:51Z"}
{"severity":"ERROR","message":"sync failed: connection refused","timestamp":"2026-08-22T12:34:51Z","trace":"error in main > 1 > sync failed: connection refused","data":{"error":"connection refused"}}
```

Identical to case 2. `context.Background()` carries no hub, no span and no
tenant, so `WithContext` has nothing to read — binding it buys nothing. This is
the one to avoid: it looks correct at the call site while silently dropping the
trace.

### Case 4 — goroutine reusing the bound logger

The logger keeps the context it was given, so handing it to a goroutine keeps
everything — even after the handler returned and the span finished:

```go
log := s.Logger.WithContext(ctx)

go func() {
    log.Info("sync started")   // handler already returned, ctx already cancelled
    log.Error("sync failed", nil, fmt.Errorf("connection refused"))
}()
```

Request span: `trace_id=e7c6e005e37dfeddef4f66eaec581000 span_id=b7ac5bcd9dc450ca`

```json
{"severity":"INFO","message":"sync started","timestamp":"2026-08-22T12:34:51Z","trace_id":"e7c6e005e37dfeddef4f66eaec581000","span_id":"b7ac5bcd9dc450ca","data":{"tenant_id":"b9565b91"}}
{"severity":"ERROR","message":"sync failed: connection refused","timestamp":"2026-08-22T12:34:51Z","trace":"error in main > 1 > sync failed: connection refused","trace_id":"e7c6e005e37dfeddef4f66eaec581000","span_id":"b7ac5bcd9dc450ca","data":{"error":"connection refused","tenant_id":"b9565b91"}}
```

Sentry: `trace_id=e7c6e005e37dfeddef4f66eaec581000`, `tenant_id=b9565b91`.

A cancelled context does not silence logging — the logger only reads values from
it, never `ctx.Done()`. The goroutine reports under the request's own span; use
case 5 when you want the background work to show up as its own span.

### Case 5 — goroutine re-scoped with `StartSpan`

```go
go func() {
    ctx, finish := svclib.StartSpan(ctx, "background.processing")
    defer finish(nil)

    log := s.Logger.WithContext(ctx)
    log.Info("sync started")
    log.Error("sync failed", nil, fmt.Errorf("connection refused"))
}()
```

Request span: `trace_id=d7176516c130e4b8a780158d29c190a1 span_id=2737fd408e691a3d`

```json
{"severity":"INFO","message":"sync started","timestamp":"2026-08-22T12:34:51Z","trace_id":"d7176516c130e4b8a780158d29c190a1","span_id":"c9a675f6c8bd1cc1","data":{"tenant_id":"b9565b91"}}
{"severity":"ERROR","message":"sync failed: connection refused","timestamp":"2026-08-22T12:34:51Z","trace":"error in main > 1 > sync failed: connection refused","trace_id":"d7176516c130e4b8a780158d29c190a1","span_id":"c9a675f6c8bd1cc1","data":{"error":"connection refused","tenant_id":"b9565b91"}}
```

Same `trace_id` as the request, **different `span_id`** (`c9a675f6c8bd1cc1` vs
`2737fd408e691a3d`) — the background work is a child span of the request, which
is what you want for anything that outlives the handler.

### Case 6 — several goroutines sharing one bound logger

```go
log := s.Logger.WithContext(ctx) // built once per request

for i := range items {
    go func(i int) {
        log.Error(fmt.Sprintf("op-%d", i), map[string]string{"i": fmt.Sprint(i)}, err)
    }(i)
}
```

Each `Error()` reports on its own clone of the hub, so per-call tags stay with
their own event. Regression test: `TestErrorTagsAreNotMixedAcrossGoroutines`
(200 goroutines, asserts every event's `i` tag matches its own error). Without
the clone, 258 of 300 concurrent events carried another goroutine's tag —
sentry's hub owns one scope stack, and a shared hub lets goroutines read each
other's scope. Note the race detector stays quiet either way: the interleaving
is logical, not a data race.

### Case 7 — service that never initialized Sentry

No `svclib.Init` / `sentry.Init` anywhere:

```json
{"severity":"INFO","message":"plain info","timestamp":"2026-08-22T12:23:31Z","data":{"order_id":"123"}}
{"severity":"WARNING","message":"a warning: timeout","timestamp":"2026-08-22T12:23:31Z","trace":"error in runtime > main > main > main > a warning: timeout","data":{"error":"timeout"}}
{"severity":"ERROR","message":"Error with ctx: boom","timestamp":"2026-08-22T12:23:31Z","trace":"error in runtime > main > main > main > Error with ctx: boom","data":{"error":"boom","tenant_id":"tenant-1"}}
```

Nothing panics: all four levels keep writing structured JSON, `WithFields` /
`WithContext` fields still land in `data`, `APP_DEBUG` still gates `Debug`. Only
the Sentry half is inert — `hub.CaptureException` returns early when the hub has
no client, so `Error()` events are dropped silently — and there are no spans, so
`trace_id` is absent. Cloud Logging is fully usable; error tracking is not.

### Rule of thumb

Pass the request context (or the bound logger) down. Bind a new context only
through `StartSpan`. `WithContext(context.Background())` is never useful.

---

## Stack Trace

`Warn` and `Error` automatically walk the Go call stack using `runtime.Callers`:

```
error in <package> > <function> > ... > <ctx>: <error>
```

The trace stops at HTTP/gRPC handler boundaries, showing only application-level call chains.

## Configuration

| Variable | Values | Description |
|---|---|---|
| `APP_DEBUG` | `true` / anything else | Enable/disable `Debug` output |
| `GOOGLE_CLOUD_PROJECT` (or `GCP_PROJECT`) | project ID | Enables the `logging.googleapis.com/trace` field |

Both can be set from code instead of the environment, for services that parse
their own config:

```go
logger.SetDebugEnabled(cfg.AppDebug == "true") // logger.ResetDebugEnabled() restores env lookup
logger.SetProjectID(cfg.GcpProject)            // logger.ResetProjectID() restores env lookup
```

## Interface

```go
type ILogger interface {
    Info(msg string, data ...map[string]any)
    Warn(ctx string, err error)
    Error(ctx string, tags map[string]string, err error)
    Debug(ctx string, req any)
    WithFields(fields map[string]any) ILogger
    WithContext(ctx context.Context) ILogger
}
```

Mockable in tests via the `ILogger` interface:

```go
service := &server{
    Logger: logger.New("test"),
}
```

## Sentry Integration

`Error()` reports to Sentry with:

- **Stack trace** as the error message — `error in X > Y > Z` format
- **Transaction tag**: `{prefix} > {ctx}`
- **Fingerprint**: `[prefix, ctx]` — prevents merging unrelated errors
- **Field tags**: everything added via `WithFields` / `WithContext` (tenant_id, subdomain, …)
- **Custom tags**: the `tags` argument
- **Trace linking**: when the logger is bound to a context, the event is captured
  on that context's hub with the span attached, plus a `trace_id` tag and trace context
- **Level**: `error`

Without a context, it falls back to the current hub — same behaviour as before.
Make sure Sentry is initialized (`svclib.Init(...)`) before calling `Error()`.

## Relation to `svclib.LogError`

`svclib.LogError(ctx, label, err)` stays as-is: a one-liner for plain-text
logging plus a trace-linked Sentry capture. Use `logger` when you want
structured Cloud Logging entries, severity levels, per-request fields and
Sentry tags — the two write to the same trace.
