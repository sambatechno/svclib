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
