package svclib

import (
	"context"
	"fmt"
	"testing"

	"github.com/getsentry/sentry-go"
	"google.golang.org/grpc"
)

func newTestHub(t *testing.T, onEvent func(*sentry.Event)) *sentry.Hub {
	t.Helper()
	client, err := sentry.NewClient(sentry.ClientOptions{
		EnableTracing:    true,
		TracesSampleRate: 1.0,
		BeforeSend: func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			if onEvent != nil {
				onEvent(e)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("sentry.NewClient: %v", err)
	}
	return sentry.NewHub(client, sentry.NewScope())
}

func TestSpanFromContext_InnermostWins(t *testing.T) {
	hub := newTestHub(t, nil)
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	t.Run("nil context", func(t *testing.T) {
		if SpanFromContext(nil) != nil {
			t.Error("expected nil for nil ctx")
		}
	})

	t.Run("interceptor span only", func(t *testing.T) {
		outer := sentry.StartSpan(ctx, "grpc.server")
		c := context.WithValue(ctx, grpcSpanContextKey{}, outer)
		if got := SpanFromContext(c); got != outer {
			t.Errorf("expected the interceptor span, got %v", got)
		}
	})

	t.Run("sentry child of the interceptor span wins", func(t *testing.T) {
		outer := sentry.StartSpan(ctx, "grpc.server")
		c := context.WithValue(outer.Context(), grpcSpanContextKey{}, outer)
		child := sentry.StartSpan(c, "db.query")
		c = child.Context()
		if got := SpanFromContext(c); got != child {
			t.Errorf("expected the db.query child, got %v (op %s)", got, got.Op)
		}
	})

	t.Run("grpc-key child of the sentry span wins (HTTP flow)", func(t *testing.T) {
		txn := sentry.StartSpan(ctx, "http.server")
		c := txn.Context()
		child := txn.StartChild("background.processing")
		c = context.WithValue(c, grpcSpanContextKey{}, child)
		if got := SpanFromContext(c); got != child {
			t.Errorf("expected the StartSpan child, got %v (op %s)", got, got.Op)
		}
	})

	t.Run("nested StartSpan resolves to the innermost span", func(t *testing.T) {
		txn := sentry.StartSpan(ctx, "http.server")
		c := txn.Context()

		c1, f1 := StartSpan(c, "level1")
		defer f1(nil)
		c2, f2 := StartSpan(c1, "level2")
		defer f2(nil)

		level2, _ := c2.Value(grpcSpanContextKey{}).(*sentry.Span)
		if got := SpanFromContext(c2); got != level2 {
			t.Errorf("expected level2 (%s), got %s (op %s)", level2.SpanID, got.SpanID, got.Op)
		}
		if level1 := SpanFromContext(c1); level1 == nil || level1.SpanID != level2.ParentSpanID {
			t.Errorf("expected level2 to be a child of level1")
		}
	})

	t.Run("detached transaction never captures the request", func(t *testing.T) {
		grpcSpan := sentry.StartSpan(ctx, "grpc.server")
		detached := sentry.StartSpan(ctx, "someone.elses.transaction")
		c := context.WithValue(detached.Context(), grpcSpanContextKey{}, grpcSpan)
		if got := SpanFromContext(c); got != grpcSpan {
			t.Errorf("expected the interceptor span over the detached transaction, got op %s", got.Op)
		}
	})
}

func TestStartSpan_DoesNotMutateCallerHub(t *testing.T) {
	var events []*sentry.Event
	hub := newTestHub(t, func(e *sentry.Event) { events = append(events, e) })
	ctx := sentry.SetHubOnContext(context.Background(), hub)
	handlerSpan := sentry.StartSpan(ctx, "grpc.server")
	ctx = context.WithValue(handlerSpan.Context(), grpcSpanContextKey{}, handlerSpan)

	// hub scope points at the handler span, like the interceptor leaves it
	hub.Scope().SetSpan(handlerSpan)

	_, finish := StartSpan(ctx, "background.processing")
	defer finish(nil)

	// A capture on the ORIGINAL hub must still land on the handler span, not
	// be repointed at the operation's child span.
	hub.CaptureMessage("from the handler")

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	trace, ok := events[0].Contexts["trace"]
	if !ok {
		t.Fatal("expected a trace context on the event")
	}
	if got := fmt.Sprintf("%v", trace["span_id"]); got != handlerSpan.SpanID.String() {
		t.Errorf("caller hub was repointed: expected span %s, got %s", handlerSpan.SpanID, got)
	}
}

func TestUnaryServerInterceptor_RegistersSpanUnderSentryKey(t *testing.T) {
	hub := newTestHub(t, nil)
	sentry.CurrentHub().BindClient(hub.Client())
	defer sentry.CurrentHub().BindClient(nil)

	interceptor := UnaryServerInterceptor()
	var handlerCtx context.Context
	_, err := interceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/svc/Method"},
		func(ctx context.Context, _ interface{}) (interface{}, error) {
			handlerCtx = ctx
			return nil, nil
		})
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}

	grpcSpan, _ := handlerCtx.Value(grpcSpanContextKey{}).(*sentry.Span)
	if grpcSpan == nil {
		t.Fatal("expected the grpc span on the handler ctx")
	}
	if got := sentry.SpanFromContext(handlerCtx); got != grpcSpan {
		t.Fatalf("expected the grpc span under sentry's key too, got %v", got)
	}

	// The payoff: a plain sentry.StartSpan in the handler now parents onto the
	// request instead of opening a detached transaction.
	child := sentry.StartSpan(handlerCtx, "db.query")
	defer child.Finish()
	if child.TraceID != grpcSpan.TraceID {
		t.Errorf("expected the child to stay on the request trace, got %s vs %s", child.TraceID, grpcSpan.TraceID)
	}
	if child.ParentSpanID != grpcSpan.SpanID {
		t.Errorf("expected the child to parent onto the grpc span, got parent %s", child.ParentSpanID)
	}
}
