// Package logger provides structured JSON logging for Cloud Run / Cloud Logging
// with Sentry error reporting and automatic trace-ID correlation.
//
// The API is identical to the logger shipped inside kds-management-service, so
// call sites move over unchanged:
//
//	log := logger.New("SharedService").WithContext(ctx)
//	log.Info("sync started")
//	log.Error("sync failed", map[string]string{"provider": "revel"}, err)
//
// On top of that, every entry logged through a logger carrying a context is
// stamped with the svclib trace ID (plus the Cloud Logging join fields when the
// request's platform trace header was forwarded and a project ID is
// configured), and errors are captured on the hub/span of that context so they
// land inside the same Sentry trace as the request.
package logger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/sambatechno/svclib"
	"google.golang.org/grpc/metadata"
)

type Severity string

const (
	SeverityDebug   Severity = "DEBUG"
	SeverityInfo    Severity = "INFO"
	SeverityWarning Severity = "WARNING"
	SeverityError   Severity = "ERROR"
)

const (
	// EnvAppDebug gates Debug() output. Debug logs are written only when it is "true".
	EnvAppDebug = "APP_DEBUG"

	// EnvProjectID / EnvProjectIDAlt supply the GCP project used to build the
	// "logging.googleapis.com/trace" field from the platform trace parsed out
	// of the forwarded X-Cloud-Trace-Context / traceparent header. Without a
	// project (or without the header) entries still carry the Sentry trace_id;
	// they just are not joined to the platform request log.
	EnvProjectID    = "GOOGLE_CLOUD_PROJECT"
	EnvProjectIDAlt = "GCP_PROJECT"

	// gRPC metadata keys forwarded by the gateway, read by WithContext.
	metadataTenantID  = "fwd-x-tenant-id"
	metadataSubdomain = "fwd-x-sub-domain"
)

// Metadata keys that may carry the platform trace header. svclib's
// DefaultHeaderMatcher forwards unmatched HTTP headers with a "fwd-" prefix;
// grpc-gateway's own matcher uses "grpcgateway-"; a direct gRPC caller may
// set the bare key.
var (
	cloudTraceContextKeys = []string{"x-cloud-trace-context", "fwd-x-cloud-trace-context", "grpcgateway-x-cloud-trace-context"}
	traceparentKeys       = []string{"traceparent", "fwd-traceparent", "grpcgateway-traceparent"}
)

// LogEntry is the structured JSON format recognized by Cloud Run / Cloud Logging.
//
// Trace is the application stack trace ("error in A > B > ctx: err"), while
// TraceID / SpanID are the distributed-trace identifiers shared with Sentry.
type LogEntry struct {
	Severity  Severity       `json:"severity"`
	Message   string         `json:"message"`
	Time      string         `json:"timestamp"`
	Trace     string         `json:"trace,omitempty"`
	TraceID   string         `json:"trace_id,omitempty"`
	SpanID    string         `json:"span_id,omitempty"`
	GCPTrace  string         `json:"logging.googleapis.com/trace,omitempty"`
	GCPSpanID string         `json:"logging.googleapis.com/spanId,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

type ILogger interface {
	Info(msg string, data ...map[string]any)
	Warn(ctx string, err error)
	Error(ctx string, tags map[string]string, err error)
	Debug(ctx string, req any)
	WithFields(fields map[string]any) ILogger
	WithContext(ctx context.Context) ILogger
}

// Logger provides structured logging with severity levels for Cloud Run.
//
// A logger returned by WithContext is bound to that request: it resolves the
// trace identifiers, tenant fields and Sentry hub/span at bind time and keeps
// them for its lifetime. Bind per request — a bound logger cached on a
// long-lived struct keeps stamping the first request's trace on every later
// entry and reports errors into that request's (long since finished) Sentry
// transaction, and nothing at the call site makes that visible.
type Logger struct {
	prefix string
	fields map[string]any

	// resolved once, at WithContext time
	hub        *sentry.Hub
	span       *sentry.Span
	traceID    string
	spanID     string
	gcpTraceID string
	gcpSpanID  string
}

// clone returns a shallow copy; fields maps are never mutated in place.
func (l *Logger) clone() *Logger {
	c := *l
	return &c
}

// New creates a new Logger with a prefix.
func New(prefix string) ILogger {
	return &Logger{prefix: prefix}
}

var (
	debugOverride     atomic.Pointer[bool]
	projectIDOverride atomic.Pointer[string]
)

// SetDebugEnabled overrides the APP_DEBUG environment variable for Debug().
// Services that already parse their own config can wire it in at startup:
//
//	logger.SetDebugEnabled(cfg.AppDebug == "true")
func SetDebugEnabled(enabled bool) { debugOverride.Store(&enabled) }

// ResetDebugEnabled drops the SetDebugEnabled override, falling back to APP_DEBUG.
func ResetDebugEnabled() { debugOverride.Store(nil) }

func debugEnabled() bool {
	if v := debugOverride.Load(); v != nil {
		return *v
	}
	// Exact match, same as the kds logger this replaces: values like "TRUE"
	// that are inert there must stay inert after an import-only migration.
	return os.Getenv(EnvAppDebug) == "true"
}

// SetProjectID overrides the GCP project used for the Cloud Logging trace
// field. Normally GOOGLE_CLOUD_PROJECT (or GCP_PROJECT) is enough.
func SetProjectID(projectID string) { projectIDOverride.Store(&projectID) }

// ResetProjectID drops the SetProjectID override.
func ResetProjectID() { projectIDOverride.Store(nil) }

func projectID() string {
	if v := projectIDOverride.Load(); v != nil {
		return *v
	}
	if id := os.Getenv(EnvProjectID); id != "" {
		return id
	}
	return os.Getenv(EnvProjectIDAlt)
}

// mergeMaps merges base and overlay into a new map. Returns nil if empty.
func mergeMaps(base, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	merged := make(map[string]any, len(base)+len(overlay))
	maps.Copy(merged, base)
	maps.Copy(merged, overlay)
	return merged
}

// WithFields returns a new Logger that includes the given fields in every log entry.
func (l *Logger) WithFields(fields map[string]any) ILogger {
	c := l.clone()
	c.fields = mergeMaps(l.fields, fields)
	return c
}

// WithContext binds the logger to ctx: every entry then carries the trace ID of
// the current span, and Error() reports on that context's Sentry hub/span so the
// event lands in the same trace as the request.
//
// It also extracts tenant_id and subdomain — from the forwarded gRPC metadata
// (fwd-x-tenant-id, fwd-x-sub-domain) and, failing that, from the tenant stored
// on the context by svclib.WithTenantID.
func (l *Logger) WithContext(ctx context.Context) ILogger {
	if ctx == nil {
		return l
	}

	c := l.clone()
	fields := make(map[string]any)
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		// An empty forwarded value counts as absent: storing "" would both
		// clutter the entry and shadow the svclib.WithTenantID fallback below.
		if v := firstNonEmpty(md.Get(metadataTenantID)); v != "" {
			fields["tenant_id"] = v
		}
		if v := firstNonEmpty(md.Get(metadataSubdomain)); v != "" {
			fields["subdomain"] = v
		}
		c.gcpTraceID, c.gcpSpanID = cloudTraceFromMetadata(md)
	}
	if _, ok := fields["tenant_id"]; !ok {
		if tenantID, ok := svclib.GetTenantID(ctx); ok && tenantID != "" {
			fields["tenant_id"] = tenantID
		}
	}
	c.fields = mergeMaps(l.fields, fields)

	c.hub = sentry.GetHubFromContext(ctx)
	c.span = svclib.SpanFromContext(ctx)
	if c.span != nil {
		c.traceID = c.span.TraceID.String()
		c.spanID = c.span.SpanID.String()
	} else {
		c.traceID = svclib.TraceIDFromContext(ctx)
		c.spanID = ""
	}
	return c
}

// cloudTraceFromMetadata extracts the platform trace from a forwarded
// X-Cloud-Trace-Context ("TRACE_ID/SPAN_ID;o=1", decimal span) or W3C
// traceparent ("00-traceid-spanid-flags") header. This — not the Sentry trace
// ID — is what Cloud Logging joins on for the logging.googleapis.com fields.
func cloudTraceFromMetadata(md metadata.MD) (traceID, spanID string) {
	for _, key := range cloudTraceContextKeys {
		v := firstNonEmpty(md.Get(key))
		if v == "" {
			continue
		}
		if i := strings.IndexByte(v, ';'); i >= 0 {
			v = v[:i]
		}
		tid, sid, _ := strings.Cut(v, "/")
		if !isHexTraceID(tid) {
			continue
		}
		if n, err := strconv.ParseUint(sid, 10, 64); err == nil && n > 0 {
			return strings.ToLower(tid), fmt.Sprintf("%016x", n)
		}
		return strings.ToLower(tid), ""
	}
	for _, key := range traceparentKeys {
		v := firstNonEmpty(md.Get(key))
		if v == "" {
			continue
		}
		parts := strings.Split(v, "-")
		if len(parts) >= 4 && isHexTraceID(parts[1]) {
			return strings.ToLower(parts[1]), strings.ToLower(parts[2])
		}
	}
	return "", ""
}

// isHexTraceID reports whether s is a 32-char lowercase-insensitive hex trace
// ID that is not all zeros (the W3C invalid value).
func isHexTraceID(s string) bool {
	if len(s) != 32 {
		return false
	}
	nonZero := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			nonZero = nonZero || r != '0'
		case r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
			nonZero = true
		default:
			return false
		}
	}
	return nonZero
}

// firstNonEmpty returns the first non-empty value, or "" when there is none.
func firstNonEmpty(values []string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (l *Logger) write(severity Severity, msg string, trace string, data map[string]any) {
	merged := mergeMaps(l.fields, data)
	entry := LogEntry{
		Severity: severity,
		Message:  msg,
		Time:     time.Now().UTC().Format(time.RFC3339Nano),
		Trace:    trace,
		TraceID:  l.traceID,
		SpanID:   l.spanID,
		Data:     merged,
	}
	// The Cloud Logging join fields carry the platform trace parsed from the
	// forwarded request header — never the Sentry trace ID, which Cloud Logging
	// could not match against the platform request log.
	if l.gcpTraceID != "" {
		if project := projectID(); project != "" {
			entry.GCPTrace = fmt.Sprintf("projects/%s/traces/%s", project, l.gcpTraceID)
			entry.GCPSpanID = l.gcpSpanID
		}
	}
	b, err := json.Marshal(entry)
	if err != nil {
		// Marshal the fallback too: interpolating the error text into a JSON
		// template by hand breaks the JSON when that text contains a quote or
		// newline (json.MarshalerError embeds the offending value verbatim).
		fallback := LogEntry{
			Severity: SeverityError,
			Message:  "failed to marshal log entry: " + err.Error(),
			Time:     entry.Time,
		}
		if fb, ferr := json.Marshal(fallback); ferr == nil {
			fmt.Fprintln(os.Stderr, string(fb))
		}
		return
	}
	fmt.Fprintln(os.Stdout, string(b))
}

// Info logs a message at INFO severity.
func (l *Logger) Info(msg string, data ...map[string]any) {
	l.write(SeverityInfo, msg, "", mergeData(data))
}

func errorMsg(ctx string, err error) string {
	if err != nil {
		return fmt.Sprintf("%s: %s", ctx, err.Error())
	}
	return ctx
}

func errorData(err error) map[string]any {
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{}
}

// Warn logs a message at WARNING severity with stack trace.
func (l *Logger) Warn(ctx string, err error) {
	l.write(SeverityWarning, errorMsg(ctx, err), buildStackTrace(ctx, err), errorData(err))
}

// Error logs a message at ERROR severity with stack trace and reports to Sentry with tags.
//
// Sentry receives the error as given, so its type and unwrap chain survive; the
// call-stack string goes along as the "stack_trace" extra. When err is nil the
// stack trace itself becomes the reported error, so the event is still raised.
func (l *Logger) Error(ctx string, tags map[string]string, err error) {
	trace := buildStackTrace(ctx, err)
	l.write(SeverityError, errorMsg(ctx, err), trace, errorData(err))

	reported := err
	if reported == nil {
		reported = errors.New(trace)
	}
	l.capture(ctx, tags, reported, trace)
}

// captureHub returns a private clone of the hub this logger reports on: the
// bound request's hub when it has one, the current hub otherwise.
//
// The clone matters. A hub owns a single scope stack, so two goroutines sharing
// one hub push and pop on the same stack: hub.CaptureException then reads
// whichever scope happens to be on top, and per-call tags land on another
// goroutine's event. A logger built once per request is routinely used from
// several goroutines, so each capture gets its own hub — cloning copies the
// client and scope, so the event still reaches the same Sentry project.
func (l *Logger) captureHub() *sentry.Hub {
	if l.hub != nil {
		return l.hub.Clone()
	}
	return sentry.CurrentHub().Clone()
}

// capture reports captured to Sentry, linked to the logger's trace when it has one.
func (l *Logger) capture(ctx string, tags map[string]string, captured error, trace string) {
	span := l.span
	traceID := l.traceID

	configure := func(scope *sentry.Scope) {
		scope.SetTag("transaction", fmt.Sprintf("%s > %s", l.prefix, ctx))
		scope.SetFingerprint([]string{l.prefix, ctx})
		for key, value := range l.fields {
			scope.SetTag(key, fmt.Sprintf("%v", value))
		}
		for key, value := range tags {
			scope.SetTag(key, value)
		}
		if span != nil {
			scope.SetSpan(span)
		}
		if traceID != "" {
			scope.SetTag("trace_id", traceID)
			scope.SetContext("trace", map[string]any{"trace_id": traceID})
		}
		if trace != "" {
			scope.SetExtra("stack_trace", trace)
		}
		scope.SetLevel(sentry.LevelError)
	}

	hub := l.captureHub()
	hub.WithScope(func(scope *sentry.Scope) {
		configure(scope)
		hub.CaptureException(captured)
	})
}

// Debug logs a message at DEBUG severity with JSON-stringified data.
// Only outputs when APP_DEBUG is "true" (or SetDebugEnabled(true) was called).
func (l *Logger) Debug(ctx string, req any) {
	if !debugEnabled() {
		return
	}

	reqJSON, err := json.Marshal(req)
	if err != nil {
		l.write(SeverityDebug, ctx, "", map[string]any{"marshal_error": err.Error()})
		return
	}

	l.write(SeverityDebug, ctx, "", map[string]any{"payload": json.RawMessage(reqJSON)})
}

// traceBoundaryPrefixes mark where the application's call chain ends and the
// framework's begins. Matching on the fully-qualified prefix — not a substring
// — keeps application closures (pkg.Fn.func1) and packages that merely contain
// "proto" or "http" in their name inside the trace.
var traceBoundaryPrefixes = []string{
	"net/http.",
	"google.golang.org/grpc",
	"github.com/grpc-ecosystem/grpc-gateway",
	"google.golang.org/protobuf",
	"runtime.",
	"testing.",
}

func isTraceBoundary(function string) bool {
	for _, prefix := range traceBoundaryPrefixes {
		if strings.HasPrefix(function, prefix) {
			return true
		}
	}
	return false
}

// buildStackTrace walks the call stack and produces a trace string like:
// "error in H2HService > AutoFinalizeOrder > SharedService > AcceptOrder > ctx: err"
//
// skip=3: runtime.Callers(0) -> buildStackTrace(1) -> Error/Warn(2) -> caller(3, first captured)
func buildStackTrace(ctx string, err error) string {
	pc := make([]uintptr, 64)
	n := runtime.Callers(3, pc)
	if n == 0 {
		return formatTraceResult("error", ctx, err)
	}

	// Collect frames bottom-up (caller first), then write in reverse.
	// Parse package + func inline to avoid a second pass.
	type frame struct{ pkg, fn string }
	var frames []frame

	iter := runtime.CallersFrames(pc[:n])
	for {
		f, more := iter.Next()
		if !more {
			break
		}
		if isTraceBoundary(f.Function) {
			break
		}
		// f.Function: "project/service/SharedService.(*server).AcceptOrder"
		short := f.Function[strings.LastIndex(f.Function, "/")+1:] // SharedService.(*server).AcceptOrder
		dotIdx := strings.Index(short, ".")
		if dotIdx < 0 {
			continue
		}
		fn := short[dotIdx+1:]
		// Drop a method receiver ("(*server).AcceptOrder" -> "AcceptOrder") but
		// keep closure suffixes ("AcceptOrder.func1") — they name real frames.
		if strings.HasPrefix(fn, "(") {
			if end := strings.Index(fn, ")."); end >= 0 {
				fn = fn[end+2:]
			}
		}
		frames = append(frames, frame{pkg: short[:dotIdx], fn: fn})
	}

	if len(frames) == 0 {
		return formatTraceResult("error", ctx, err)
	}

	var b strings.Builder
	prevPkg := ""
	// Write in reverse (outermost caller first)
	for i := len(frames) - 1; i >= 0; i-- {
		f := frames[i]
		if b.Len() == 0 {
			b.WriteString("error in ")
			b.WriteString(f.pkg)
		} else if f.pkg != prevPkg {
			b.WriteString(" > ")
			b.WriteString(f.pkg)
		}
		b.WriteString(" > ")
		b.WriteString(f.fn)
		prevPkg = f.pkg
	}

	return formatTraceResult(b.String(), ctx, err)
}

func formatTraceResult(prefix, ctx string, err error) string {
	if err != nil {
		return prefix + " > " + ctx + ": " + err.Error()
	}
	return prefix + " > " + ctx
}

func mergeData(data []map[string]any) map[string]any {
	if len(data) == 0 {
		return nil
	}
	merged := make(map[string]any)
	for _, d := range data {
		maps.Copy(merged, d)
	}
	return merged
}
