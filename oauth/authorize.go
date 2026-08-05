package oauth

import (
	"context"
	"net/http"
	"strings"
)

// mwConfig holds the options shared by the HTTP and gRPC adapters. errHandler is honored only by
// the HTTP adapter (gRPC has a fixed status-code mapping); it is nil unless WithErrorHandler is set.
type mwConfig struct {
	requiredScopes []string
	grants         *GrantChecker
	errHandler     func(http.ResponseWriter, *http.Request, error)
}

// MWOption configures Middleware / UnaryServerInterceptor.
type MWOption func(*mwConfig)

// WithRequiredScopes asserts that the token carries every listed scope (subset check). A request
// missing any of them is rejected with ErrInsufficientScope (403) before the handler runs.
func WithRequiredScopes(scopes ...string) MWOption {
	return func(c *mwConfig) { c.requiredScopes = scopes }
}

// WithGrantCheck enables the revocation check: after the token verifies, the merchant's grant must
// still be active. Omit it for a purely stateless (offline) validation with no DB dependency.
func WithGrantCheck(g *GrantChecker) MWOption {
	return func(c *mwConfig) { c.grants = g }
}

// authorize runs the full validation pipeline: verify (signature/iss/aud/exp) -> required scopes
// -> optional grant revocation. It returns the validated Claims or one of the package's sentinel
// errors, which the adapters map to a transport status.
func authorize(ctx context.Context, v Verifier, cfg *mwConfig, token string) (*Claims, error) {
	claims, err := v.Verify(ctx, token)
	if err != nil {
		return nil, err
	}
	if len(cfg.requiredScopes) > 0 && !claims.hasAllScopes(cfg.requiredScopes) {
		return nil, ErrInsufficientScope
	}
	if cfg.grants != nil {
		if _, err := cfg.grants.Active(ctx, claims); err != nil {
			return nil, err
		}
	}
	return claims, nil
}

func newMWConfig(opts []MWOption) *mwConfig {
	cfg := &mwConfig{}
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}

// parseBearer extracts the token from an "Authorization: Bearer <token>" value (scheme
// case-insensitive), shared by the HTTP and gRPC adapters. Returns "" when the value is empty or
// not a Bearer credential, which surfaces as ErrTokenMissing from Verify.
func parseBearer(header string) string {
	const prefix = "Bearer "
	if len(header) >= len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}
