package oauth

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func invoke(t *testing.T, v Verifier, md metadata.MD, opts ...MWOption) (any, error) {
	t.Helper()
	ctx := context.Background()
	if md != nil {
		ctx = metadata.NewIncomingContext(ctx, md)
	}
	interceptor := UnaryServerInterceptor(v, opts...)
	return interceptor(ctx, nil, &grpc.UnaryServerInfo{},
		func(ctx context.Context, _ any) (any, error) {
			if _, ok := ClaimsFrom(ctx); !ok {
				t.Error("claims not propagated to handler context")
			}
			return "ok", nil
		})
}

func TestGRPC_Success(t *testing.T) {
	claims := &Claims{Subject: "s", Tenant: "qapoints", Scopes: []string{ScopeOrders}}
	md := metadata.Pairs("authorization", "Bearer abc")
	resp, err := invoke(t, okVerifier(claims), md, WithRequiredScopes(ScopeOrders))
	if err != nil || resp != "ok" {
		t.Fatalf("invoke = (%v, %v), want (ok, nil)", resp, err)
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := invoke(t, tc.v, tc.md, tc.opts...)
			if status.Code(err) != tc.want {
				t.Errorf("code = %v, want %v (err=%v)", status.Code(err), tc.want, err)
			}
		})
	}
}
