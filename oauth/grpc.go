package oauth

import (
	"context"
	"errors"

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
			return nil, status.Error(grpcStatus(err))
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
	return parseBearer(vals[0])
}

// grpcStatus maps a validation error to a gRPC code + coarse, non-leaking message, parallel to
// httpStatus: missing/invalid/revoked -> Unauthenticated, insufficient scope -> PermissionDenied,
// DB-unavailable -> Unavailable, and any UNRECOGNIZED error -> Internal (a server-side fault, not
// the client's token). The specific reason is logged server-side, never returned to the caller.
func grpcStatus(err error) (codes.Code, string) {
	switch {
	case errors.Is(err, ErrTokenMissing), errors.Is(err, ErrTokenInvalid), errors.Is(err, ErrGrantRevoked):
		return codes.Unauthenticated, "invalid token"
	case errors.Is(err, ErrInsufficientScope):
		return codes.PermissionDenied, "insufficient scope"
	case errors.Is(err, ErrGrantUnavailable):
		return codes.Unavailable, "grant status unavailable"
	default:
		return codes.Internal, "internal error"
	}
}
