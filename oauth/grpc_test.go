package oauth

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// invoke runs the interceptor and reports whether the protected handler ran, so denied cases can
// assert it was skipped.
func invoke(t *testing.T, v Verifier, md metadata.MD, opts ...MWOption) (resp any, err error, handlerRan bool) {
	t.Helper()
	ctx := context.Background()
	if md != nil {
		ctx = metadata.NewIncomingContext(ctx, md)
	}
	interceptor := UnaryServerInterceptor(v, opts...)
	resp, err = interceptor(ctx, nil, &grpc.UnaryServerInfo{},
		func(ctx context.Context, _ any) (any, error) {
			handlerRan = true
			if _, ok := ClaimsFrom(ctx); !ok {
				t.Error("claims not propagated to handler context")
			}
			return "ok", nil
		})
	return resp, err, handlerRan
}

func TestGRPC_Success(t *testing.T) {
	claims := &Claims{Subject: "s", Tenant: "qapoints", Scopes: []string{ScopeOrders}}
	md := metadata.Pairs("authorization", "Bearer abc")
	resp, err, ran := invoke(t, okVerifier(claims), md, WithRequiredScopes(ScopeOrders))
	if err != nil || resp != "ok" || !ran {
		t.Fatalf("invoke = (%v, %v, ran=%v), want (ok, nil, true)", resp, err, ran)
	}
}

func TestGRPC_CodeMapping(t *testing.T) {
	claims := &Claims{Subject: "s", Tenant: "t", Scopes: []string{"other"}}
	cases := []struct {
		name string
		v    Verifier
		md   metadata.MD
		opts []MWOption
		want codes.Code
	}{
		{"no-metadata", okVerifier(claims), nil, nil, codes.Unauthenticated},
		{"missing", okVerifier(claims), metadata.MD{}, nil, codes.Unauthenticated},
		{"invalid", verifyFunc(func(context.Context, string) (*Claims, error) { return nil, ErrTokenInvalid }),
			metadata.Pairs("authorization", "Bearer bad"), nil, codes.Unauthenticated},
		{"insufficient-scope", okVerifier(claims),
			metadata.Pairs("authorization", "Bearer x"), []MWOption{WithRequiredScopes(ScopeOrders)}, codes.PermissionDenied},
		// Unrecognized verifier error -> Internal, not Unauthenticated (server fault, not bad token).
		{"unknown-error", verifyFunc(func(context.Context, string) (*Claims, error) { return nil, errors.New("boom") }),
			metadata.Pairs("authorization", "Bearer x"), nil, codes.Internal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err, ran := invoke(t, tc.v, tc.md, tc.opts...)
			if status.Code(err) != tc.want {
				t.Errorf("code = %v, want %v (err=%v)", status.Code(err), tc.want, err)
			}
			if ran {
				t.Error("handler must not run on a denied request")
			}
		})
	}
}
