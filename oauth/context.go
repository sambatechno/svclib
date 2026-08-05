package oauth

import "context"

// claimsContextKey is the unexported key under which the adapters store validated Claims.
type claimsContextKey struct{}

// withClaims returns a copy of ctx carrying the validated claims.
func withClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, c)
}

// ClaimsFrom returns the validated Claims placed in ctx by Middleware / UnaryServerInterceptor.
// ok is false if the request did not pass through the adapter (e.g. an unauthenticated route).
func ClaimsFrom(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsContextKey{}).(*Claims)
	return c, ok
}
