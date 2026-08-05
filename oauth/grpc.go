package oauth

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor authenticates the bearer token carried in the request's "authorization"
// metadata before the handler runs. On success it stores the validated Claims in the context (read
// via ClaimsFrom); on failure it returns a gRPC status error. Scope and grant options are shared
// with the HTTP Middleware.
//
//	grpc.NewServer(grpc.ChainUnaryInterceptor(
//	    oauth.UnaryServerInterceptor(v, oauth.WithRequiredScopes(oauth.ScopeOrders), oauth.WithGrantCheck(gc)),
//	))
func UnaryServerInterceptor(v Verifier, opts ...MWOption) grpc.UnaryServerInterceptor {
	cfg := newMWConfig(opts)
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		claims, err := authorize(ctx, v, cfg, bearerFromMetadata(ctx))
		if err != nil {
			return nil, status.Error(grpcCode(err), grpcMessage(err))
		}
		return handler(withClaims(ctx, claims), req)
	}
}

// bearerFromMetadata pulls the token from the "authorization" metadata entry ("Bearer <token>",
// scheme case-insensitive). Returns "" when absent -> ErrTokenMissing.
func bearerFromMetadata(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return ""
	}
	const prefix = "Bearer "
	h := vals[0]
	if len(h) >= len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// grpcCode maps a validation error to a gRPC status code, parallel to httpStatus: revoked/invalid/
// missing -> Unauthenticated, insufficient scope -> PermissionDenied, DB-unavailable -> Unavailable.
func grpcCode(err error) codes.Code {
	switch {
	case errors.Is(err, ErrInsufficientScope):
		return codes.PermissionDenied
	case errors.Is(err, ErrGrantUnavailable):
		return codes.Unavailable
	default:
		return codes.Unauthenticated
	}
}

// grpcMessage returns a coarse, non-leaking status message (the specific reason is logged
// server-side, never returned to the caller).
func grpcMessage(err error) string {
	switch {
	case errors.Is(err, ErrInsufficientScope):
		return "insufficient scope"
	case errors.Is(err, ErrGrantUnavailable):
		return "grant status unavailable"
	default:
		return "invalid token"
	}
}
