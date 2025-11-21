package svclib

import (
	"context"

	"github.com/getsentry/sentry-go"
	"github.com/sambatechno/svclib/internal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryClientInterceptor is a gRPC client interceptor that propagates Sentry context
// from HTTP requests to gRPC calls made by grpc-gateway.
//
// Example usage:
//
//	conn, err := grpc.NewClient(
//	    "localhost:8080",
//	    grpc.WithChainUnaryInterceptor(svclib.UnaryClientInterceptor()),
//	)
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		span := sentry.SpanFromContext(ctx)
		if span == nil {
			return invoker(ctx, method, req, reply, cc, opts...)
		}

		// Store the span in registry for server interceptor to retrieve
		traceID := span.TraceID.String()
		internal.StoreSpan(traceID, span)
		defer internal.DeleteSpan(traceID)

		// Add trace headers to outgoing metadata
		md, _ := metadata.FromOutgoingContext(ctx)
		if md == nil {
			md = metadata.New(nil)
		} else {
			md = md.Copy()
		}
		md.Set("sentry-trace", span.ToSentryTrace())
		md.Set("sentry-trace-id", traceID)

		return invoker(metadata.NewOutgoingContext(ctx, md), method, req, reply, cc, opts...)
	}
}

// UnaryServerInterceptor returns a new unary server interceptor for Sentry.
// It creates child spans within the parent HTTP transaction for proper trace continuity.
//
// Example usage:
//
//	gs := grpc.NewServer(
//	    grpc.ChainUnaryInterceptor(
//	        recovery.UnaryServerInterceptor(recoveryOpt),
//	        svclib.UnaryServerInterceptor(),
//	    ),
//	)
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract trace information from metadata
		parentSpan, hub, md := extractSentryContext(ctx)

		// Ensure we have a hub
		if hub == nil {
			hub = sentry.CurrentHub().Clone()
		}
		ctx = sentry.SetHubOnContext(ctx, hub)

		// Create span for this gRPC call
		grpcSpan := createGRPCSpan(ctx, parentSpan, info.FullMethod)
		defer grpcSpan.Finish()

		// Add tags from metadata
		tagSpanFromMetadata(grpcSpan, hub, md, info.FullMethod)

		// Add the span to the context so handlers can access it
		ctx = context.WithValue(ctx, grpcSpanContextKey{}, grpcSpan)

		// Also store trace ID for easy access in goroutines
		ctx = context.WithValue(ctx, sentryTraceContextKey{}, grpcSpan.TraceID.String())

		// Execute handler
		resp, err := handler(ctx, req)

		// Set span status and capture errors
		handleGRPCError(grpcSpan, hub, err, info.FullMethod)

		return resp, err
	}
}

// extractSentryContext extracts parent span, hub, and metadata from the context
func extractSentryContext(ctx context.Context) (*sentry.Span, *sentry.Hub, metadata.MD) {
	var parentSpan *sentry.Span
	var sentryTraceHeader string

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, sentry.GetHubFromContext(ctx), nil
	}

	// Try to get parent span from registry (internal grpc-gateway calls)
	if traceIDs := md.Get("sentry-trace-id"); len(traceIDs) > 0 {
		if span, ok := internal.LoadSpan(traceIDs[0]); ok {
			parentSpan = span
		}
	}

	// Get trace header for fallback (external calls or if registry fails)
	if traces := md.Get("sentry-trace"); len(traces) > 0 {
		sentryTraceHeader = traces[0]
	} else if traces := md.Get("fwd-sentry-trace"); len(traces) > 0 {
		sentryTraceHeader = traces[0]
	}

	// If we didn't find parent span but have trace header, try to continue from it
	if parentSpan == nil && sentryTraceHeader != "" {
		// Create a transaction that continues the trace
		// This is used for external gRPC calls or when registry lookup fails
		// NOTE: This transaction is assigned to parentSpan, and a child span will be
		// created from it in createGRPCSpan(). The child span's Finish() will be called
		// in the interceptor's defer, but the transaction itself won't be finished here.
		// For internal calls via grpc-gateway, this path shouldn't be hit since the
		// registry lookup should succeed. For external gRPC calls, the transaction
		// represents the full server-side handling.
		transaction := sentry.StartTransaction(ctx,
			"grpc.server",
			sentry.ContinueFromTrace(sentryTraceHeader),
		)
		parentSpan = transaction
	}

	return parentSpan, sentry.GetHubFromContext(ctx), md
}

// createGRPCSpan creates a span for the gRPC call
func createGRPCSpan(ctx context.Context, parentSpan *sentry.Span, method string) *sentry.Span {
	var span *sentry.Span

	if parentSpan != nil {
		span = parentSpan.StartChild("grpc.server")
	} else {
		span = sentry.StartSpan(ctx, "grpc.server")
	}

	span.Description = method
	return span
}

// tagSpanFromMetadata adds tags to the span from gRPC metadata
func tagSpanFromMetadata(span *sentry.Span, hub *sentry.Hub, md metadata.MD, method string) {
	if md == nil {
		return
	}

	// Add tenant tags
	if tenantIDs := md.Get("fwd-x-tenant-id"); len(tenantIDs) > 0 {
		tenantID := tenantIDs[0]
		span.SetTag("tenant.id", tenantID)
		span.SetData("tenant.id", tenantID)
		hub.Scope().SetTag("tenant.id", tenantID)
	}

	// Add subdomain tags
	if subDomains := md.Get("fwd-x-sub-domain"); len(subDomains) > 0 {
		subdomain := subDomains[0]
		span.SetTag("tenant.subdomain", subdomain)
		span.SetData("tenant.subdomain", subdomain)
		hub.Scope().SetTag("tenant.subdomain", subdomain)
	}

	// Add gRPC method tags
	span.SetTag("grpc.method", method)
	span.SetData("grpc.method", method)
	hub.Scope().SetTag("grpc.method", method)
}

// handleGRPCError sets the span status and captures errors with proper context
func handleGRPCError(span *sentry.Span, hub *sentry.Hub, err error, method string) {
	if err == nil {
		span.Status = sentry.SpanStatusOK
		return
	}

	// Map gRPC status code to Sentry status
	if st, ok := status.FromError(err); ok {
		span.Status = grpcCodeToSentryStatus(st.Code())
		span.SetData("grpc.code", st.Code().String())
		span.SetData("grpc.message", st.Message())
	} else {
		span.Status = sentry.SpanStatusInternalError
	}

	// Capture exception with context
	hub.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetContext("grpc", map[string]interface{}{
			"method": method,
			"error":  err.Error(),
		})
		scope.SetSpan(span)
	})
	hub.CaptureException(err)
}

// grpcCodeToSentryStatus maps gRPC codes to Sentry span status
func grpcCodeToSentryStatus(code codes.Code) sentry.SpanStatus {
	switch code {
	case codes.OK:
		return sentry.SpanStatusOK
	case codes.Canceled:
		return sentry.SpanStatusCanceled
	case codes.InvalidArgument:
		return sentry.SpanStatusInvalidArgument
	case codes.DeadlineExceeded:
		return sentry.SpanStatusDeadlineExceeded
	case codes.NotFound:
		return sentry.SpanStatusNotFound
	case codes.AlreadyExists:
		return sentry.SpanStatusAlreadyExists
	case codes.PermissionDenied:
		return sentry.SpanStatusPermissionDenied
	case codes.ResourceExhausted:
		return sentry.SpanStatusResourceExhausted
	case codes.FailedPrecondition:
		return sentry.SpanStatusFailedPrecondition
	case codes.Aborted:
		return sentry.SpanStatusAborted
	case codes.OutOfRange:
		return sentry.SpanStatusOutOfRange
	case codes.Unimplemented:
		return sentry.SpanStatusUnimplemented
	case codes.Internal:
		return sentry.SpanStatusInternalError
	case codes.Unavailable:
		return sentry.SpanStatusUnavailable
	case codes.DataLoss:
		return sentry.SpanStatusDataLoss
	case codes.Unauthenticated:
		return sentry.SpanStatusUnauthenticated
	default:
		return sentry.SpanStatusUnknown
	}
}

