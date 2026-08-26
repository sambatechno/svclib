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

Deliberate differences from kds:

- Sentry receives the original error rather than a synthetic one built from the
  stack trace, so the error type and unwrap chain survive. The stack trace
  travels as the `stack_trace` extra, and the fingerprint is unchanged, so
  existing issue grouping still holds.
- The `logging.googleapis.com/trace` fields carry the **platform trace** parsed
  from the forwarded `X-Cloud-Trace-Context` / `traceparent` request header —
  never the Sentry trace ID, which Cloud Logging could not join against the
  request log. Without that header the fields are omitted; `trace_id` (Sentry)
  is always there.
- `APP_DEBUG` is an exact match on `"true"`, same as kds — values like `TRUE`
  that are inert there stay inert here.

## Output Format

All logs are written as JSON to stdout, recognized by Cloud Run / Cloud Logging:

```json
{
  "severity": "ERROR",
  "message": "SharedService: capturePayment",
  "timestamp": "2026-03-25T10:00:00.599334168Z",
  "trace": "error in SharedService > CompleteOrder > capturePayment > ...: unexpected http status: 520",
  "trace_id": "d4cda95b652f4a1592b449d5929fda1b",
  "span_id": "1e5e29a5b4b9c1f2",
  "logging.googleapis.com/trace": "projects/cata-prod/traces/105445aa7843bc8bf206b12000100000",
  "logging.googleapis.com/spanId": "000000000000004a",
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
| `logging.googleapis.com/trace` / `spanId` | Cloud Logging request-log correlation — the **platform trace** parsed from the forwarded `X-Cloud-Trace-Context` / `traceparent` header; emitted only when that header is present *and* a project ID is configured. |

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

func NewService() *server {
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

Cases 2 and 3 are recoverable — see
[How to keep `tenant_id` in cases 2 and 3](#how-to-keep-tenant_id-in-cases-2-and-3).

---

### Case 1 — base case (bound to the request context)

```go
log := s.Logger.WithContext(ctx) // ctx from the gRPC handler / interceptor
log.Info("sync started")
log.Error("sync failed", nil, fmt.Errorf("connection refused"))
```

Request span: `trace_id=639e412338420a0e0212a6dc9dde649d span_id=4660e7af6efed661`, request
carrying `X-Cloud-Trace-Context: 105445aa7843bc8bf206b12000100000/74;o=1`:

```json
{"severity":"INFO","message":"sync started","timestamp":"2026-08-24T07:46:20.599334168Z","trace_id":"639e412338420a0e0212a6dc9dde649d","span_id":"4660e7af6efed661","logging.googleapis.com/trace":"projects/cata-prod/traces/105445aa7843bc8bf206b12000100000","logging.googleapis.com/spanId":"000000000000004a","data":{"tenant_id":"b9565b91"}}
{"severity":"ERROR","message":"sync failed: connection refused","timestamp":"2026-08-24T07:46:20.599425865Z","trace":"error in main > main > run > main.func1 > sync failed: connection refused","trace_id":"639e412338420a0e0212a6dc9dde649d","span_id":"4660e7af6efed661","logging.googleapis.com/trace":"projects/cata-prod/traces/105445aa7843bc8bf206b12000100000","logging.googleapis.com/spanId":"000000000000004a","data":{"error":"connection refused","tenant_id":"b9565b91"}}
```

Note the two trace identities: `trace_id` is the Sentry trace; the
`logging.googleapis.com/*` fields carry the platform trace from the forwarded
header (decimal span `74` re-encoded as hex), which is what Cloud Logging joins
against the request log.

Sentry — 1 event, on the request trace:

```text
tags  = {tenant_id: b9565b91, trace_id: 639e412338420a0e0212a6dc9dde649d, transaction: "SharedService > sync failed"}
trace = {trace_id: 639e412338420a0e0212a6dc9dde649d}
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
{"severity":"INFO","message":"sync started","timestamp":"2026-08-24T07:46:20.601790223Z"}
{"severity":"ERROR","message":"sync failed: connection refused","timestamp":"2026-08-24T07:46:20.601822718Z","trace":"error in main > main.func2.1 > sync failed: connection refused","data":{"error":"connection refused"}}
```

Sentry — 1 event, but **not** on the request trace:

```text
tags  = {transaction: "SharedService > sync failed"}          <- no trace_id, no tenant_id
trace = {trace_id: a1db116052978b0ae20c2af9094fec1d, ...}     <- auto-generated, unrelated
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
{"severity":"INFO","message":"sync started","timestamp":"2026-08-24T07:46:20.604029574Z"}
{"severity":"ERROR","message":"sync failed: connection refused","timestamp":"2026-08-24T07:46:20.604044151Z","trace":"error in main > main.func3.1 > sync failed: connection refused","data":{"error":"connection refused"}}
```

Identical to case 2. `context.Background()` carries no hub, no span and no
tenant, so `WithContext` has nothing to read — binding it buys nothing. This is
the one to avoid: it looks correct at the call site while silently dropping the
trace.

### How to keep `tenant_id` in cases 2 and 3

Pick by what the goroutine actually has in hand:

| You have | Use | `tenant_id` | `subdomain` | `trace_id` |
|---|---|---|---|---|
| No context can reach the goroutine | pass the values, `WithFields` | ✅ | ✅ | ❌ |
| A fresh context you control | `svclib.WithTenantID` | ✅ | ❌ (add via `WithFields`) | ❌ |
| The request context, but it gets cancelled | `context.WithoutCancel` | ✅ | ✅ | ✅ |
| The request context, and the work deserves its own span | `context.WithoutCancel` + `StartSpan` | ✅ | ✅ | ✅ child span |

#### Option A — pass the tenant explicitly, build a new logger there

Use this when the goroutine genuinely cannot be reached by a context: it is
started from a place that has no handler context, or it runs work whose inputs
are plain values. Then the tenant travels as an ordinary argument, and the
goroutine builds its own logger:

```go
// caller: hands the identifying values over explicitly
go s.processSyncMenu(request.TenantId, request.Subdomain, request.StoreUuid)

// goroutine: builds its own logger from those values
func (s *server) processSyncMenu(tenantId, subdomain, storeUuid string) {
    log := s.Logger.WithFields(map[string]any{
        "tenant_id":  tenantId,
        "subdomain":  subdomain,
        "store_uuid": storeUuid,
    })

    log.Info("sync started")
    if err := s.sync(tenantId, storeUuid); err != nil {
        log.Error("processSyncMenu", nil, err)
    }
}
```

```json
{"severity":"INFO","message":"sync started","timestamp":"2026-08-24T07:46:43.379820307Z","data":{"store_uuid":"9f21c3","subdomain":"gyg","tenant_id":"b9565b91"}}
{"severity":"ERROR","message":"processSyncMenu: connection refused","timestamp":"2026-08-24T07:46:43.379970505Z","trace":"error in main > runFixes.func1.1 > processSyncMenu: connection refused","data":{"error":"connection refused","store_uuid":"9f21c3","subdomain":"gyg","tenant_id":"b9565b91"}}
```

Sentry: `tags = {store_uuid: 9f21c3, subdomain: gyg, tenant_id: b9565b91, transaction: "SharedService > processSyncMenu"}`.

Fields become Sentry tags, so the event stays filterable per tenant. There is
still no `trace_id` — nothing in this call chain knows about a trace — so treat
the tenant fields as the correlation key here.

When several functions need the same fields, give the service a small factory
rather than repeating the map (the kds `orderLog` pattern):

```go
func (s *server) orderLog(request structs.UpdateStatusOrderRequest) logger.ILogger {
    return s.Logger.WithFields(map[string]any{
        "tenant_id":  request.TenantId,
        "subdomain":  request.Subdomain,
        "order_uuid": request.OrderUuid,
    })
}

go func() {
    log := s.orderLog(request)
    log.Error("AcceptOrder", nil, err)
}()
```

Do not pass a `logger.ILogger` built for another request into the goroutine
instead of the values — a logger bound with `WithContext` carries that request's
trace and tenant, and reusing it elsewhere labels your entries with them.

#### Option B — `svclib.WithTenantID`, when you build the context yourself

```go
go func() {
    ctx := svclib.WithTenantID(context.Background(), request.TenantId)
    log := s.Logger.WithContext(ctx)
    log.Info("sync started")
    log.Error("sync failed", nil, err)
}()
```

```json
{"severity":"INFO","message":"sync started","timestamp":"2026-08-24T07:46:43.382252249Z","data":{"tenant_id":"b9565b91"}}
{"severity":"ERROR","message":"sync failed: connection refused","timestamp":"2026-08-24T07:46:43.382280066Z","trace":"error in main > runFixes.func2.1 > sync failed: connection refused","data":{"error":"connection refused","tenant_id":"b9565b91"}}
```

Worth it when the same context is already being passed to the database layer,
which reads the tenant from it too. `WithTenantID` carries **only** the tenant —
there is no subdomain equivalent, so add that one with `WithFields`. Still no
trace: a bare `context.Background()` has no hub and no span.

#### Option C — `context.WithoutCancel`, the usual real answer

Most `context.Background()` in a goroutine is there for one reason: the request
context is cancelled when the handler returns. `context.WithoutCancel` (Go 1.21+)
solves exactly that — it keeps every value (hub, span, tenant, gRPC metadata)
while dropping the cancellation, and with it the deadline, the `Done` channel
and `Err`. Give the goroutine its own bound with `context.WithTimeout` on the
detached context when the work should not run forever:

```go
detached := context.WithoutCancel(ctx) // take it BEFORE starting the goroutine

go func() {
    log := s.Logger.WithContext(detached)
    log.Info("sync started")
    log.Error("sync failed", nil, err)
}()
```

Request span: `trace_id=18a3a07d074a999441f61dc96322725d span_id=0e1003346cc6de16`

```json
{"severity":"INFO","message":"sync started","timestamp":"2026-08-24T07:46:43.384772857Z","trace_id":"18a3a07d074a999441f61dc96322725d","span_id":"0e1003346cc6de16","logging.googleapis.com/trace":"projects/cata-prod/traces/105445aa7843bc8bf206b12000100000","logging.googleapis.com/spanId":"000000000000004a","data":{"subdomain":"gyg","tenant_id":"b9565b91"}}
{"severity":"ERROR","message":"sync failed: connection refused","timestamp":"2026-08-24T07:46:43.384816121Z","trace":"error in main > runFixes.func3.1 > sync failed: connection refused","trace_id":"18a3a07d074a999441f61dc96322725d","span_id":"0e1003346cc6de16","logging.googleapis.com/trace":"projects/cata-prod/traces/105445aa7843bc8bf206b12000100000","logging.googleapis.com/spanId":"000000000000004a","data":{"error":"connection refused","subdomain":"gyg","tenant_id":"b9565b91"}}
```

```text
request ctx err = context canceled   <- handler already returned
detached ctx err = <nil>             <- goroutine keeps working
```

Everything comes back: `tenant_id`, `subdomain` (from the forwarded gRPC
metadata), `trace_id`, `span_id` — plus the `logging.googleapis.com/*` fields,
which appear here because the request carried `X-Cloud-Trace-Context` and a
project ID is configured (see [Configuration](#configuration)); without either
the entry still carries `trace_id`. And the
Sentry event is tagged `trace_id=2fb4533d…`, i.e. on the request's own trace.
Because the values survive, the detached context is also the one to hand to
outbound calls in that goroutine.

#### Option D — `WithoutCancel` + `StartSpan`, for its own span

```go
detached := context.WithoutCancel(ctx)

go func() {
    ctx, finish := svclib.StartSpan(detached, "background.processing")
    defer finish(nil)

    log := s.Logger.WithContext(ctx)
    log.Info("sync started")
    log.Error("sync failed", nil, err)
}()
```

Request span: `trace_id=37213ede77101a4811640dd245d8391d span_id=e992597a62b11cf4`

```json
{"severity":"INFO","message":"sync started","timestamp":"2026-08-24T07:46:43.387252655Z","trace_id":"37213ede77101a4811640dd245d8391d","span_id":"19111afc1887b4bd","logging.googleapis.com/trace":"projects/cata-prod/traces/105445aa7843bc8bf206b12000100000","logging.googleapis.com/spanId":"000000000000004a","data":{"subdomain":"gyg","tenant_id":"b9565b91"}}
```

Same trace, own `span_id` (`19111afc1887b4bd` vs the request's
`e992597a62b11cf4`), tenant intact. This is the default choice for background
work that outlives the handler.

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

Request span: `trace_id=5da0938d25a990e60b46cf90dc425da3 span_id=01f5716797191066`

```json
{"severity":"INFO","message":"sync started","timestamp":"2026-08-24T07:46:20.606351662Z","trace_id":"5da0938d25a990e60b46cf90dc425da3","span_id":"01f5716797191066","logging.googleapis.com/trace":"projects/cata-prod/traces/105445aa7843bc8bf206b12000100000","logging.googleapis.com/spanId":"000000000000004a","data":{"tenant_id":"b9565b91"}}
{"severity":"ERROR","message":"sync failed: connection refused","timestamp":"2026-08-24T07:46:20.606366009Z","trace":"error in main > main.func4.1 > sync failed: connection refused","trace_id":"5da0938d25a990e60b46cf90dc425da3","span_id":"01f5716797191066","logging.googleapis.com/trace":"projects/cata-prod/traces/105445aa7843bc8bf206b12000100000","logging.googleapis.com/spanId":"000000000000004a","data":{"error":"connection refused","tenant_id":"b9565b91"}}
```

Sentry: `trace_id=5da0938d25a990e60b46cf90dc425da3`, `tenant_id=b9565b91`.

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

Request span: `trace_id=2b70cb37e2c386cf146123d2560e94ca span_id=4566c10715b7ab76`

```json
{"severity":"INFO","message":"sync started","timestamp":"2026-08-24T07:46:20.609384998Z","trace_id":"2b70cb37e2c386cf146123d2560e94ca","span_id":"f98ff114127653c5","logging.googleapis.com/trace":"projects/cata-prod/traces/105445aa7843bc8bf206b12000100000","logging.googleapis.com/spanId":"000000000000004a","data":{"tenant_id":"b9565b91"}}
{"severity":"ERROR","message":"sync failed: connection refused","timestamp":"2026-08-24T07:46:20.609400093Z","trace":"error in main > main.func5.1 > sync failed: connection refused","trace_id":"2b70cb37e2c386cf146123d2560e94ca","span_id":"f98ff114127653c5","logging.googleapis.com/trace":"projects/cata-prod/traces/105445aa7843bc8bf206b12000100000","logging.googleapis.com/spanId":"000000000000004a","data":{"error":"connection refused","tenant_id":"b9565b91"}}
```

Same `trace_id` as the request, **different `span_id`** (`f98ff114127653c5` vs
`4566c10715b7ab76`) — the background work is a child span of the request, which
is what you want for anything that outlives the handler. `StartSpan` runs the
operation on a private clone of the hub, so it never repoints the request hub's
scope at the background span.

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
(200 goroutines, asserts every event's `i` tag matches its own error). A
separate 300-goroutine experiment on the pre-fix code measured the damage:
258 of those 300 events carried another goroutine's tag —
sentry's hub owns one scope stack, and a shared hub lets goroutines read each
other's scope. Note the race detector stays quiet either way: the interleaving
is logical, not a data race.

### Case 7 — service that never initialized Sentry

No `svclib.Init` / `sentry.Init` anywhere:

```json
{"severity":"INFO","message":"plain info","timestamp":"2026-08-24T07:46:59.483962226Z","data":{"order_id":"123"}}
{"severity":"WARNING","message":"a warning: timeout","timestamp":"2026-08-24T07:46:59.484038035Z","trace":"error in main > main > a warning: timeout","data":{"error":"timeout"}}
{"severity":"ERROR","message":"Error with ctx: boom","timestamp":"2026-08-24T07:46:59.484098417Z","trace":"error in main > main > Error with ctx: boom","data":{"error":"boom","tenant_id":"tenant-1"}}
```

Nothing panics: `Info`, `Warn` and `Error` keep writing structured JSON (and so
does `Debug`, still only when `APP_DEBUG=true` — the run above left it off, which
is why no DEBUG line appears), `WithFields` / `WithContext` fields still land in
`data`. Only
the Sentry half is inert — `hub.CaptureException` returns early when the hub has
no client, so `Error()` events are dropped silently — and there are no spans, so
`trace_id` is absent. Cloud Logging is fully usable; error tracking is not.

### Rule of thumb

Pass the request context (or the bound logger) down. When you reach for
`context.Background()` because the request context is cancelled, use
`context.WithoutCancel(ctx)` instead — same values, no cancellation — and wrap
the goroutine in `StartSpan` when it deserves its own span. Where no context
exists at all, carry the tenant with `WithFields`. `WithContext(context.Background())`
is never useful.

---

## Stack Trace

`Warn` and `Error` automatically walk the Go call stack using `runtime.Callers`:

```text
error in <package> > <function> > ... > <ctx>: <error>
```

The walk stops at framework boundaries, identified by fully-qualified package
prefix (`net/http.`, `google.golang.org/grpc`, grpc-gateway, protobuf,
`runtime.`, `testing.`) — so only application-level frames appear. Application
closures keep their frames (`syncAll.func1`), and packages that merely contain
"proto" or "http" in their own name are not cut off.

## Configuration

| Variable | Values | Description |
|---|---|---|
| `APP_DEBUG` | exactly `true` / anything else | Enable/disable `Debug` output (exact match, kds parity) |
| `GOOGLE_CLOUD_PROJECT` (or `GCP_PROJECT`) | project ID | Enables the `logging.googleapis.com/trace` field — emitted only for requests whose forwarded `X-Cloud-Trace-Context` / `traceparent` header was parsed |

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

- **The error itself** — reported as given, so its type and unwrap chain survive
  in the event (when `err` is nil, the stack trace becomes the reported error, so
  the event is still raised). Grouping does not follow from that: the explicit
  fingerprint below overrides Sentry's default exception-based grouping
- **Stack trace** as the `stack_trace` extra — `error in X > Y > Z` format
- **Transaction tag**: `{prefix} > {ctx}`
- **Fingerprint**: `[prefix, ctx]` — overrides default grouping, so every event
  from the same call site forms one issue and unrelated errors never merge
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
