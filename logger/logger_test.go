package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/sambatechno/svclib"
	"google.golang.org/grpc/metadata"
)

// captureStream drains the pipe in the background while fn runs: the
// concurrent tests write more than a pipe buffer holds, and a full pipe would
// block the logger write instead of returning.
func captureStream(stream **os.File, fn func()) string {
	old := *stream
	r, w, _ := os.Pipe()
	*stream = w

	var buf bytes.Buffer
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(&buf, r)
	}()

	fn()

	_ = w.Close()
	<-copied
	*stream = old
	return buf.String()
}

func captureOutput(fn func()) string { return captureStream(&os.Stdout, fn) }
func captureStderr(fn func()) string { return captureStream(&os.Stderr, fn) }

func parseLogEntry(t *testing.T, output string) LogEntry {
	t.Helper()
	var entry LogEntry
	if err := json.Unmarshal([]byte(output), &entry); err != nil {
		t.Fatalf("parse log entry: %v; output=%q", err, output)
	}
	return entry
}

// tracedContext returns a context carrying a real Sentry span, plus its trace ID.
func tracedContext(t *testing.T) (context.Context, string) {
	t.Helper()

	client, err := sentry.NewClient(sentry.ClientOptions{
		EnableTracing:    true,
		TracesSampleRate: 1.0,
	})
	if err != nil {
		t.Fatalf("sentry.NewClient: %v", err)
	}
	hub := sentry.NewHub(client, sentry.NewScope())

	ctx := sentry.SetHubOnContext(context.Background(), hub)
	span := sentry.StartSpan(ctx, "test.operation")
	return span.Context(), span.TraceID.String()
}

func TestNew(t *testing.T) {
	log := New("TestService")
	if log == nil {
		t.Fatal("New() returned nil")
	}
}

func TestInfo(t *testing.T) {
	log := New("TestService")

	t.Run("without data", func(t *testing.T) {
		output := captureOutput(func() {
			log.Info("hello")
		})
		entry := parseLogEntry(t, output)
		if entry.Severity != SeverityInfo {
			t.Errorf("expected severity INFO, got %s", entry.Severity)
		}
		if entry.Message != "hello" {
			t.Errorf("expected message 'hello', got %s", entry.Message)
		}
		if entry.Time == "" {
			t.Error("expected timestamp to be non-empty")
		}
	})

	t.Run("with data", func(t *testing.T) {
		output := captureOutput(func() {
			log.Info("order created", map[string]any{"order_id": "123"})
		})
		entry := parseLogEntry(t, output)
		if entry.Data["order_id"] != "123" {
			t.Errorf("expected order_id=123, got %v", entry.Data["order_id"])
		}
	})

	t.Run("with multiple data maps", func(t *testing.T) {
		output := captureOutput(func() {
			log.Info("test", map[string]any{"a": "1"}, map[string]any{"b": "2"})
		})
		entry := parseLogEntry(t, output)
		if entry.Data["a"] != "1" || entry.Data["b"] != "2" {
			t.Errorf("expected merged data, got %v", entry.Data)
		}
	})

	t.Run("no trace for info", func(t *testing.T) {
		output := captureOutput(func() {
			log.Info("hello")
		})
		entry := parseLogEntry(t, output)
		if entry.Trace != "" {
			t.Errorf("expected empty trace for Info, got %s", entry.Trace)
		}
	})
}

func TestWarn(t *testing.T) {
	log := New("TestService")

	t.Run("with error", func(t *testing.T) {
		output := captureOutput(func() {
			log.Warn("slow query", fmt.Errorf("timeout"))
		})
		entry := parseLogEntry(t, output)
		if entry.Severity != SeverityWarning {
			t.Errorf("expected severity WARNING, got %s", entry.Severity)
		}
		if entry.Data["error"] != "timeout" {
			t.Errorf("expected error=timeout, got %v", entry.Data["error"])
		}
		if entry.Trace == "" {
			t.Error("expected trace to be non-empty")
		}
	})

	t.Run("with nil error", func(t *testing.T) {
		output := captureOutput(func() {
			log.Warn("just a warning", nil)
		})
		entry := parseLogEntry(t, output)
		if entry.Severity != SeverityWarning {
			t.Errorf("expected severity WARNING, got %s", entry.Severity)
		}
		if entry.Trace == "" {
			t.Error("expected trace even with nil error")
		}
	})
}

func TestError(t *testing.T) {
	log := New("TestService")

	t.Run("with error and tags", func(t *testing.T) {
		output := captureOutput(func() {
			log.Error("failed", map[string]string{"tenant_id": "abc"}, fmt.Errorf("db error"))
		})
		entry := parseLogEntry(t, output)
		if entry.Severity != SeverityError {
			t.Errorf("expected severity ERROR, got %s", entry.Severity)
		}
		if entry.Data["error"] != "db error" {
			t.Errorf("expected error='db error', got %v", entry.Data["error"])
		}
		if entry.Trace == "" {
			t.Error("expected trace to be non-empty")
		}
	})

	t.Run("with nil tags", func(t *testing.T) {
		output := captureOutput(func() {
			log.Error("failed", nil, fmt.Errorf("some error"))
		})
		entry := parseLogEntry(t, output)
		if entry.Severity != SeverityError {
			t.Errorf("expected severity ERROR, got %s", entry.Severity)
		}
	})

	t.Run("with nil error", func(t *testing.T) {
		output := captureOutput(func() {
			log.Error("something", nil, nil)
		})
		entry := parseLogEntry(t, output)
		if entry.Severity != SeverityError {
			t.Errorf("expected severity ERROR, got %s", entry.Severity)
		}
		if entry.Trace == "" {
			t.Error("expected trace even with nil error")
		}
	})

	t.Run("with fields sends fields to sentry scope", func(t *testing.T) {
		scoped := log.WithFields(map[string]any{
			"tenant_id": "abc-123",
			"subdomain": "gyg",
		})
		output := captureOutput(func() {
			scoped.Error("test", map[string]string{"extra": "tag"}, fmt.Errorf("err"))
		})
		entry := parseLogEntry(t, output)
		if entry.Data["tenant_id"] != "abc-123" {
			t.Errorf("expected tenant_id in data, got %v", entry.Data)
		}
	})

	t.Run("trace includes caller function", func(t *testing.T) {
		output := captureOutput(func() {
			log.Error("my context", nil, fmt.Errorf("fail"))
		})
		entry := parseLogEntry(t, output)
		if !strings.Contains(entry.Trace, "my context") {
			t.Errorf("expected trace to contain 'my context', got %s", entry.Trace)
		}
	})
}

func TestDebug(t *testing.T) {
	log := New("TestService")

	t.Run("disabled when APP_DEBUG is not true", func(t *testing.T) {
		t.Setenv(EnvAppDebug, "false")
		output := captureOutput(func() {
			log.Debug("payload", map[string]any{"key": "val"})
		})
		if output != "" {
			t.Errorf("expected no output, got %s", output)
		}
	})

	t.Run("outputs debug log when APP_DEBUG is true", func(t *testing.T) {
		t.Setenv(EnvAppDebug, "true")
		output := captureOutput(func() {
			log.Debug("payload", map[string]any{"key": "val"})
		})
		entry := parseLogEntry(t, output)
		if entry.Severity != SeverityDebug {
			t.Errorf("expected severity DEBUG, got %s", entry.Severity)
		}
		if entry.Data["payload"] == nil {
			t.Error("expected payload in data")
		}
	})

	t.Run("only the exact value true enables it", func(t *testing.T) {
		for _, inert := range []string{"TRUE", "True", "true ", "1"} {
			t.Setenv(EnvAppDebug, inert)
			output := captureOutput(func() {
				log.Debug("payload", map[string]any{"key": "val"})
			})
			if output != "" {
				t.Errorf("expected APP_DEBUG=%q to stay inert (kds parity), got %s", inert, output)
			}
		}
	})

	t.Run("SetDebugEnabled overrides the environment", func(t *testing.T) {
		t.Setenv(EnvAppDebug, "false")
		SetDebugEnabled(true)
		defer ResetDebugEnabled()

		output := captureOutput(func() {
			log.Debug("payload", map[string]any{"key": "val"})
		})
		if output == "" {
			t.Error("expected debug output with SetDebugEnabled(true)")
		}

		SetDebugEnabled(false)
		output = captureOutput(func() {
			log.Debug("payload", map[string]any{"key": "val"})
		})
		if output != "" {
			t.Errorf("expected no output with SetDebugEnabled(false), got %s", output)
		}
	})

	t.Run("handles unmarshalable input", func(t *testing.T) {
		t.Setenv(EnvAppDebug, "true")
		output := captureOutput(func() {
			log.Debug("bad", make(chan int))
		})
		entry := parseLogEntry(t, output)
		if entry.Data["marshal_error"] == nil {
			t.Error("expected marshal_error in data")
		}
	})
}

func TestWithFields(t *testing.T) {
	log := New("TestService")
	scoped := log.WithFields(map[string]any{
		"tenant_id": "abc",
		"subdomain": "gyg",
	})

	output := captureOutput(func() {
		scoped.Info("hello")
	})
	entry := parseLogEntry(t, output)
	if entry.Data["tenant_id"] != "abc" {
		t.Errorf("expected tenant_id=abc, got %v", entry.Data["tenant_id"])
	}
	if entry.Data["subdomain"] != "gyg" {
		t.Errorf("expected subdomain=gyg, got %v", entry.Data["subdomain"])
	}
}

func TestWithFieldsMerge(t *testing.T) {
	log := New("TestService")
	scoped := log.WithFields(map[string]any{"a": "1"})
	scoped2 := scoped.WithFields(map[string]any{"b": "2"})

	output := captureOutput(func() {
		scoped2.Info("hello")
	})
	entry := parseLogEntry(t, output)
	if entry.Data["a"] != "1" {
		t.Errorf("expected a=1, got %v", entry.Data["a"])
	}
	if entry.Data["b"] != "2" {
		t.Errorf("expected b=2, got %v", entry.Data["b"])
	}
}

func TestWithFieldsOverride(t *testing.T) {
	log := New("TestService")
	scoped := log.WithFields(map[string]any{"key": "old"})
	scoped2 := scoped.WithFields(map[string]any{"key": "new"})

	output := captureOutput(func() {
		scoped2.Info("hello")
	})
	entry := parseLogEntry(t, output)
	if entry.Data["key"] != "new" {
		t.Errorf("expected key=new (overridden), got %v", entry.Data["key"])
	}
}

func TestWithFieldsEmpty(t *testing.T) {
	log := New("TestService")
	scoped := log.WithFields(nil)

	output := captureOutput(func() {
		scoped.Info("hello")
	})
	entry := parseLogEntry(t, output)
	if entry.Data != nil {
		t.Errorf("expected nil data with empty fields, got %v", entry.Data)
	}
}

func TestWithFieldsKeepsContext(t *testing.T) {
	ctx, traceID := tracedContext(t)

	log := New("TestService").WithContext(ctx).WithFields(map[string]any{"order_uuid": "abc"})
	output := captureOutput(func() {
		log.Info("hello")
	})
	entry := parseLogEntry(t, output)
	if entry.TraceID != traceID {
		t.Errorf("expected trace_id %s to survive WithFields, got %s", traceID, entry.TraceID)
	}
	if entry.Data["order_uuid"] != "abc" {
		t.Errorf("expected order_uuid in data, got %v", entry.Data)
	}
}

func TestWithContext(t *testing.T) {
	log := New("TestService")

	t.Run("extracts tenant and subdomain from gRPC context", func(t *testing.T) {
		md := metadata.New(map[string]string{
			"fwd-x-tenant-id":  "tenant-123",
			"fwd-x-sub-domain": "gyg",
		})
		ctx := metadata.NewIncomingContext(context.Background(), md)
		scoped := log.WithContext(ctx)

		output := captureOutput(func() {
			scoped.Info("test")
		})
		entry := parseLogEntry(t, output)
		if entry.Data["tenant_id"] != "tenant-123" {
			t.Errorf("expected tenant_id=tenant-123, got %v", entry.Data["tenant_id"])
		}
		if entry.Data["subdomain"] != "gyg" {
			t.Errorf("expected subdomain=gyg, got %v", entry.Data["subdomain"])
		}
	})

	t.Run("empty context has no fields", func(t *testing.T) {
		ctx := context.Background()
		scoped := log.WithContext(ctx)

		output := captureOutput(func() {
			scoped.Info("test")
		})
		entry := parseLogEntry(t, output)
		if entry.Data != nil {
			t.Errorf("expected nil data, got %v", entry.Data)
		}
	})

	t.Run("partial metadata only tenant_id", func(t *testing.T) {
		md := metadata.New(map[string]string{
			"fwd-x-tenant-id": "tenant-only",
		})
		ctx := metadata.NewIncomingContext(context.Background(), md)
		scoped := log.WithContext(ctx)

		output := captureOutput(func() {
			scoped.Info("test")
		})
		entry := parseLogEntry(t, output)
		if entry.Data["tenant_id"] != "tenant-only" {
			t.Errorf("expected tenant_id=tenant-only, got %v", entry.Data["tenant_id"])
		}
		if entry.Data["subdomain"] != nil {
			t.Errorf("expected no subdomain, got %v", entry.Data["subdomain"])
		}
	})

	t.Run("falls back to svclib tenant context", func(t *testing.T) {
		ctx := svclib.WithTenantID(context.Background(), "tenant-ctx")
		scoped := log.WithContext(ctx)

		output := captureOutput(func() {
			scoped.Info("test")
		})
		entry := parseLogEntry(t, output)
		if entry.Data["tenant_id"] != "tenant-ctx" {
			t.Errorf("expected tenant_id=tenant-ctx, got %v", entry.Data["tenant_id"])
		}
	})

	t.Run("empty forwarded tenant falls back to the svclib tenant context", func(t *testing.T) {
		md := metadata.New(map[string]string{"fwd-x-tenant-id": "", "fwd-x-sub-domain": ""})
		ctx := metadata.NewIncomingContext(svclib.WithTenantID(context.Background(), "tenant-ctx"), md)
		scoped := log.WithContext(ctx)

		output := captureOutput(func() {
			scoped.Info("test")
		})
		entry := parseLogEntry(t, output)
		if entry.Data["tenant_id"] != "tenant-ctx" {
			t.Errorf("expected tenant_id=tenant-ctx, got %v", entry.Data["tenant_id"])
		}
		if _, ok := entry.Data["subdomain"]; ok {
			t.Errorf("expected empty subdomain to be dropped, got %v", entry.Data["subdomain"])
		}
	})

	t.Run("gRPC metadata wins over svclib tenant context", func(t *testing.T) {
		md := metadata.New(map[string]string{"fwd-x-tenant-id": "tenant-md"})
		ctx := metadata.NewIncomingContext(svclib.WithTenantID(context.Background(), "tenant-ctx"), md)
		scoped := log.WithContext(ctx)

		output := captureOutput(func() {
			scoped.Info("test")
		})
		entry := parseLogEntry(t, output)
		if entry.Data["tenant_id"] != "tenant-md" {
			t.Errorf("expected tenant_id=tenant-md, got %v", entry.Data["tenant_id"])
		}
	})
}

func TestWithContextNilDoesNotPanic(t *testing.T) {
	log := New("TestService").WithContext(nil)
	output := captureOutput(func() {
		log.Info("still works")
	})
	entry := parseLogEntry(t, output)
	if entry.Message != "still works" {
		t.Errorf("expected an unbound logger, got %+v", entry)
	}
}

func TestStackTraceSurvivesClosures(t *testing.T) {
	var trace string
	done := make(chan struct{})
	go func() {
		defer close(done)
		trace = buildStackTraceForTest()
	}()
	<-done

	if !strings.Contains(trace, "func1") {
		t.Errorf("expected the goroutine closure frame in the trace, got %q", trace)
	}
	if !strings.Contains(trace, "TestStackTraceSurvivesClosures") {
		t.Errorf("expected the caller function in the trace, got %q", trace)
	}
}

// buildStackTraceForTest stands in for Warn/Error so buildStackTrace's skip
// count lines up the same way it does in production.
func buildStackTraceForTest() string {
	return buildStackTrace("boom", nil)
}

func TestWithFieldsDoesNotMutateParent(t *testing.T) {
	log := New("TestService")
	_ = log.WithFields(map[string]any{"tenant_id": "abc"})

	output := captureOutput(func() {
		log.Info("hello")
	})
	entry := parseLogEntry(t, output)
	if entry.Data != nil {
		t.Errorf("expected nil data on parent, got %v", entry.Data)
	}
}

func TestFieldsMergedWithCallData(t *testing.T) {
	log := New("TestService")
	scoped := log.WithFields(map[string]any{"tenant_id": "abc"})

	output := captureOutput(func() {
		scoped.Info("test", map[string]any{"order_id": "123"})
	})
	entry := parseLogEntry(t, output)
	if entry.Data["tenant_id"] != "abc" {
		t.Errorf("expected tenant_id from fields, got %v", entry.Data["tenant_id"])
	}
	if entry.Data["order_id"] != "123" {
		t.Errorf("expected order_id from call data, got %v", entry.Data["order_id"])
	}
}

func TestWriteMarshalError(t *testing.T) {
	log := New("TestService").(*Logger)

	output := captureStderr(func() {
		captureOutput(func() {
			log.write(SeverityInfo, "test", "", map[string]any{
				"bad": make(chan int),
			})
		})
	})
	if !strings.Contains(output, "failed to marshal log entry") {
		t.Errorf("expected marshal error on stderr, got %s", output)
	}
}

func TestMergeMaps(t *testing.T) {
	t.Run("both nil", func(t *testing.T) {
		result := mergeMaps(nil, nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("base nil", func(t *testing.T) {
		result := mergeMaps(nil, map[string]any{"a": "1"})
		if result["a"] != "1" {
			t.Errorf("expected a=1, got %v", result)
		}
	})

	t.Run("overlay nil", func(t *testing.T) {
		result := mergeMaps(map[string]any{"a": "1"}, nil)
		if result["a"] != "1" {
			t.Errorf("expected a=1, got %v", result)
		}
	})

	t.Run("overlay overrides base", func(t *testing.T) {
		result := mergeMaps(map[string]any{"a": "1"}, map[string]any{"a": "2"})
		if result["a"] != "2" {
			t.Errorf("expected a=2, got %v", result)
		}
	})
}

func TestBuildStackTrace(t *testing.T) {
	t.Run("includes error in trace", func(t *testing.T) {
		trace := buildStackTrace("myCtx", fmt.Errorf("fail"))
		if !strings.Contains(trace, "myCtx") {
			t.Errorf("expected trace to contain myCtx, got %s", trace)
		}
		if !strings.Contains(trace, "fail") {
			t.Errorf("expected trace to contain error, got %s", trace)
		}
	})

	t.Run("nil error produces trace without error suffix", func(t *testing.T) {
		trace := buildStackTrace("myCtx", nil)
		if !strings.Contains(trace, "myCtx") {
			t.Errorf("expected trace to contain myCtx, got %s", trace)
		}
		if strings.Contains(trace, ":") {
			t.Errorf("expected no colon (no error), got %s", trace)
		}
	})
}

func TestMergeData(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		result := mergeData(nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("single map", func(t *testing.T) {
		result := mergeData([]map[string]any{{"a": "1"}})
		if result["a"] != "1" {
			t.Errorf("expected a=1, got %v", result)
		}
	})

	t.Run("multiple maps merged", func(t *testing.T) {
		result := mergeData([]map[string]any{{"a": "1"}, {"b": "2"}})
		if result["a"] != "1" || result["b"] != "2" {
			t.Errorf("expected merged, got %v", result)
		}
	})
}
