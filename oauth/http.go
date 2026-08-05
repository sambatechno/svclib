package oauth

import (
	"errors"
	"net/http"
	"strings"
)

// Middleware returns net/http middleware that authenticates the request's bearer token before the
// wrapped handler runs. On success it stores the validated Claims in the request context (read via
// ClaimsFrom); on failure it writes an RFC 6750 error response and does not call next.
//
//	mux.Handle("/service/stream-dsp/v1/locations",
//	    oauth.Middleware(v, oauth.WithRequiredScopes(oauth.ScopeOrders), oauth.WithGrantCheck(gc))(h))
//
// The handler bridges the token's tenant to its own tenant mechanism, e.g.:
//
//	claims, _ := oauth.ClaimsFrom(r.Context())
//	ctx := context.WithValue(r.Context(), core.TenantContextKey{}, claims.Tenant)
func Middleware(v Verifier, opts ...MWOption) func(http.Handler) http.Handler {
	cfg := newMWConfig(opts)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := authorize(r.Context(), v, cfg, bearerToken(r))
			if err != nil {
				if cfg.errHandler != nil {
					cfg.errHandler(w, r, err)
				} else {
					writeRFC6750(w, err)
				}
				return
			}
			next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
		})
	}
}

// WithErrorHandler overrides the default RFC 6750 error response (HTTP only). Use it to match a
// service's existing error envelope; err is one of the package sentinels, so map it with errors.Is.
func WithErrorHandler(h func(w http.ResponseWriter, r *http.Request, err error)) MWOption {
	return func(c *mwConfig) { c.errHandler = h }
}

// bearerToken extracts the token from "Authorization: Bearer <token>" (scheme case-insensitive).
// Returns "" when absent, which surfaces as ErrTokenMissing from Verify.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) >= len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// httpStatus maps a validation error to an HTTP status and the RFC 6750 error code (empty when
// none applies). Grant-revoked is invalid_token/401 per RFC 6750 ("expired, revoked, malformed…"),
// which also correctly nudges the partner's client to refresh/re-authorize.
func httpStatus(err error) (status int, code string) {
	switch {
	case errors.Is(err, ErrTokenMissing):
		return http.StatusUnauthorized, ""
	case errors.Is(err, ErrInsufficientScope):
		return http.StatusForbidden, "insufficient_scope"
	case errors.Is(err, ErrGrantUnavailable):
		return http.StatusServiceUnavailable, ""
	case errors.Is(err, ErrGrantRevoked), errors.Is(err, ErrTokenInvalid):
		return http.StatusUnauthorized, "invalid_token"
	default:
		return http.StatusUnauthorized, "invalid_token"
	}
}

// writeRFC6750 writes the default Bearer error response: a WWW-Authenticate challenge (per RFC
// 6750 §3) and the matching status. The body is intentionally minimal — the specific reason is
// never leaked to the caller (it is logged server-side by the service, not here).
func writeRFC6750(w http.ResponseWriter, err error) {
	status, code := httpStatus(err)
	if status != http.StatusServiceUnavailable {
		challenge := "Bearer"
		if code != "" {
			challenge = `Bearer error="` + code + `"`
		}
		w.Header().Set("WWW-Authenticate", challenge)
	}
	w.WriteHeader(status)
}
