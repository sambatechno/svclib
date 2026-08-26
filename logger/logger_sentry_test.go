package logger

// Sentry-integration tests: what Error() reports, on which hub, with which
// trace identifiers. The plain formatting/severity tests live in logger_test.go.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/sambatechno/svclib"
	"google.golang.org/grpc/metadata"
)

func TestErrorCapturesOnContextHub(t *testing.T) {
	var captured []*sentry.Event

	client, err := sentry.NewClient(sentry.ClientOptions{
		EnableTracing:    true,
		TracesSampleRate: 1.0,
		BeforeSend: func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			captured = append(captured, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("sentry.NewClient: %v", err)
	}
	hub := sentry.NewHub(client, sentry.NewScope())
	ctx := sentry.SetHubOnContext(context.Background(), hub)
	span := sentry.StartSpan(ctx, "test.operation")
	ctx = span.Context()

	log := New("TestService").WithContext(ctx)
	captureOutput(func() {
		log.Error("boom", map[string]string{"provider": "revel"}, fmt.Errorf("db error"))
	})

	if len(captured) != 1 {
		t.Fatalf("expected 1 captured event, got %d", len(captured))
	}
	event := captured[0]
	if got := event.Tags["trace_id"]; got != span.TraceID.String() {
		t.Errorf("expected trace_id tag %s, got %s", span.TraceID.String(), got)
	}
	if got := event.Tags["provider"]; got != "revel" {
		t.Errorf("expected provider tag revel, got %s", got)
	}
	if got := event.Tags["transaction"]; got != "TestService > boom" {
		t.Errorf("expected transaction tag 'TestService > boom', got %s", got)
	}
	if event.Contexts["trace"]["trace_id"] != span.TraceID.String() {
		t.Errorf("expected trace context trace_id, got %v", event.Contexts["trace"])
	}
	if event.Level != sentry.LevelError {
		t.Errorf("expected level error, got %s", event.Level)
	}
}

type notFoundError struct{ resource string }

func (e *notFoundError) Error() string { return e.resource + " not found" }

func TestErrorReportsTheOriginalError(t *testing.T) {
	capture := func(fn func(ILogger)) *sentry.Event {
		t.Helper()
		var event *sentry.Event
		client, err := sentry.NewClient(sentry.ClientOptions{
			BeforeSend: func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
				event = e
				return nil
			},
		})
		if err != nil {
			t.Fatalf("sentry.NewClient: %v", err)
		}
		ctx := sentry.SetHubOnContext(context.Background(), sentry.NewHub(client, sentry.NewScope()))
		captureOutput(func() { fn(New("TestService").WithContext(ctx)) })
		if event == nil {
			t.Fatal("expected a sentry event")
		}
		return event
	}

	t.Run("keeps the error type and wrapping", func(t *testing.T) {
		wrapped := fmt.Errorf("loading store: %w", &notFoundError{resource: "store"})
		event := capture(func(log ILogger) {
			log.Error("GetStore", nil, wrapped)
		})

		if len(event.Exception) == 0 {
			t.Fatal("expected an exception on the event")
		}
		// sentry unwinds the wrap chain, innermost first.
		values := make([]string, 0, len(event.Exception))
		types := make([]string, 0, len(event.Exception))
		for _, e := range event.Exception {
			values = append(values, e.Value)
			types = append(types, e.Type)
		}
		if !slices.Contains(values, "store not found") {
			t.Errorf("expected the wrapped error to survive, got values %v", values)
		}
		if !slices.Contains(values, wrapped.Error()) {
			t.Errorf("expected the outer wrapper to survive, got values %v", values)
		}
		if !slices.Contains(types, "*logger.notFoundError") {
			t.Errorf("expected the error type to survive, got types %v", types)
		}
		if event.Extra["stack_trace"] == nil {
			t.Error("expected the call-stack string to be kept as the stack_trace extra")
		}
	})

	t.Run("falls back to the stack trace when err is nil", func(t *testing.T) {
		event := capture(func(log ILogger) {
			log.Error("GetStore", nil, nil)
		})
		if len(event.Exception) == 0 {
			t.Fatal("expected an exception on the event")
		}
		if !strings.Contains(event.Exception[0].Value, "GetStore") {
			t.Errorf("expected the stack trace as the reported error, got %q", event.Exception[0].Value)
		}
	})
}

func TestErrorTagsAreNotMixedAcrossGoroutines(t *testing.T) {
	var mu sync.Mutex
	type event struct{ tag, message string }
	var events []event

	client, err := sentry.NewClient(sentry.ClientOptions{
		EnableTracing:    true,
		TracesSampleRate: 1.0,
		BeforeSend: func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			message := ""
			if len(e.Exception) > 0 {
				message = e.Exception[0].Value
			}
			mu.Lock()
			events = append(events, event{tag: e.Tags["i"], message: message})
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("sentry.NewClient: %v", err)
	}
	hub := sentry.NewHub(client, sentry.NewScope())
	ctx := sentry.SetHubOnContext(context.Background(), hub)
	span := sentry.StartSpan(ctx, "http.server")

	// One logger built per request, then used from many goroutines at once.
	log := New("TestService").WithContext(span.Context())

	const n = 200
	// stdout is swapped once around the whole block: captureOutput itself is not
	// safe to call from several goroutines at the same time.
	captureOutput(func() {
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				log.Error(fmt.Sprintf("op-%d", i), map[string]string{"i": fmt.Sprint(i)}, fmt.Errorf("boom-%d", i))
			}(i)
		}
		wg.Wait()
	})

	mu.Lock()
	defer mu.Unlock()
	if len(events) != n {
		t.Fatalf("expected %d events, got %d", n, len(events))
	}
	for _, e := range events {
		if e.tag == "" {
			t.Fatalf("event lost its tag: %q", e.message)
		}
		if !strings.Contains(e.message, "boom-"+e.tag) {
			t.Fatalf("event tagged i=%s carries another goroutine's error: %q", e.tag, e.message)
		}
	}
}

func TestTraceID(t *testing.T) {
	t.Run("logs carry the trace and span id of the context", func(t *testing.T) {
		ctx, traceID := tracedContext(t)
		log := New("TestService").WithContext(ctx)

		output := captureOutput(func() {
			log.Info("hello")
		})
		entry := parseLogEntry(t, output)
		if entry.TraceID != traceID {
			t.Errorf("expected trace_id=%s, got %s", traceID, entry.TraceID)
		}
		if entry.SpanID == "" {
			t.Error("expected span_id to be non-empty")
		}
	})

	t.Run("no trace id without a context", func(t *testing.T) {
		output := captureOutput(func() {
			New("TestService").Info("hello")
		})
		entry := parseLogEntry(t, output)
		if entry.TraceID != "" {
			t.Errorf("expected empty trace_id, got %s", entry.TraceID)
		}
	})

	t.Run("reads the trace id stored by StartSpan in a goroutine context", func(t *testing.T) {
		client, err := sentry.NewClient(sentry.ClientOptions{EnableTracing: true, TracesSampleRate: 1.0})
		if err != nil {
			t.Fatalf("sentry.NewClient: %v", err)
		}
		hub := sentry.NewHub(client, sentry.NewScope())
		ctx := sentry.SetHubOnContext(context.Background(), hub)

		ctx, finish := svclib.StartSpan(ctx, "background.processing")
		defer finish(nil)

		want := svclib.TraceIDFromContext(ctx)
		if want == "" {
			t.Fatal("expected StartSpan to put a trace id on the context")
		}

		output := captureOutput(func() {
			New("TestService").WithContext(ctx).Info("working")
		})
		entry := parseLogEntry(t, output)
		if entry.TraceID != want {
			t.Errorf("expected trace_id=%s, got %s", want, entry.TraceID)
		}
	})
}

func TestGCPTraceFields(t *testing.T) {
	ctx, sentryTraceID := tracedContext(t)
	const cloudTraceID = "105445aa7843bc8bf206b12000100000"

	withHeader := func(ctx context.Context, key, value string) context.Context {
		md := metadata.New(map[string]string{key: value})
		return metadata.NewIncomingContext(ctx, md)
	}

	t.Run("emitted from the forwarded X-Cloud-Trace-Context header", func(t *testing.T) {
		t.Setenv(EnvProjectID, "cata-prod")
		hctx := withHeader(ctx, "fwd-x-cloud-trace-context", cloudTraceID+"/74;o=1")
		output := captureOutput(func() {
			New("TestService").WithContext(hctx).Info("hello")
		})
		entry := parseLogEntry(t, output)
		want := "projects/cata-prod/traces/" + cloudTraceID
		if entry.GCPTrace != want {
			t.Errorf("expected %s, got %s", want, entry.GCPTrace)
		}
		if entry.GCPSpanID != "000000000000004a" {
			t.Errorf("expected decimal span 74 as 000000000000004a, got %s", entry.GCPSpanID)
		}
		if entry.TraceID != sentryTraceID {
			t.Errorf("expected the Sentry trace to stay in trace_id, got %s", entry.TraceID)
		}
	})

	t.Run("emitted from a forwarded traceparent header", func(t *testing.T) {
		t.Setenv(EnvProjectID, "cata-prod")
		hctx := withHeader(ctx, "fwd-traceparent", "00-"+cloudTraceID+"-00f067aa0ba902b7-01")
		output := captureOutput(func() {
			New("TestService").WithContext(hctx).Info("hello")
		})
		entry := parseLogEntry(t, output)
		if entry.GCPTrace != "projects/cata-prod/traces/"+cloudTraceID {
			t.Errorf("expected traceparent trace, got %s", entry.GCPTrace)
		}
		if entry.GCPSpanID != "00f067aa0ba902b7" {
			t.Errorf("expected traceparent span, got %s", entry.GCPSpanID)
		}
	})

	t.Run("never built from the Sentry trace id", func(t *testing.T) {
		t.Setenv(EnvProjectID, "cata-prod")
		output := captureOutput(func() {
			New("TestService").WithContext(ctx).Info("hello")
		})
		entry := parseLogEntry(t, output)
		if entry.GCPTrace != "" {
			t.Errorf("expected no GCP trace without the platform header, got %s", entry.GCPTrace)
		}
		if entry.TraceID != sentryTraceID {
			t.Errorf("expected trace_id to be kept, got %s", entry.TraceID)
		}
	})

	t.Run("omitted without a project id", func(t *testing.T) {
		t.Setenv(EnvProjectID, "")
		t.Setenv(EnvProjectIDAlt, "")
		hctx := withHeader(ctx, "fwd-x-cloud-trace-context", cloudTraceID+"/74;o=1")
		output := captureOutput(func() {
			New("TestService").WithContext(hctx).Info("hello")
		})
		entry := parseLogEntry(t, output)
		if entry.GCPTrace != "" {
			t.Errorf("expected no GCP trace field, got %s", entry.GCPTrace)
		}
	})

	t.Run("SetProjectID overrides the environment", func(t *testing.T) {
		t.Setenv(EnvProjectID, "from-env")
		SetProjectID("from-code")
		defer ResetProjectID()

		hctx := withHeader(ctx, "fwd-x-cloud-trace-context", cloudTraceID+"/74;o=1")
		output := captureOutput(func() {
			New("TestService").WithContext(hctx).Info("hello")
		})
		entry := parseLogEntry(t, output)
		if !strings.Contains(entry.GCPTrace, "projects/from-code/") {
			t.Errorf("expected override project, got %s", entry.GCPTrace)
		}
	})

	t.Run("rejects malformed headers", func(t *testing.T) {
		t.Setenv(EnvProjectID, "cata-prod")
		for _, bad := range []string{"not-a-trace/1;o=1", "00000000000000000000000000000000/1", "short/2"} {
			hctx := withHeader(context.Background(), "fwd-x-cloud-trace-context", bad)
			output := captureOutput(func() {
				New("TestService").WithContext(hctx).Info("hello")
			})
			entry := parseLogEntry(t, output)
			if entry.GCPTrace != "" {
				t.Errorf("expected %q to be rejected, got %s", bad, entry.GCPTrace)
			}
		}
	})
}
