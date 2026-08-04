package oauth

import "errors"

// Sentinel errors returned by the verifier and grant checker. Callers (the HTTP/gRPC adapters)
// use errors.Is to map them to a status without string-matching. The buckets are deliberately
// coarse: a partner is never told which specific check failed (that would leak validation
// internals) — the real reason is logged server-side.
var (
	// ErrTokenMissing is returned when no bearer token is present. -> 401.
	ErrTokenMissing = errors.New("oauth: bearer token missing")

	// ErrTokenInvalid wraps every "this is not a valid token" reason — bad signature, wrong
	// algorithm, malformed, expired, wrong issuer or audience, missing tenant/subject. -> 401.
	ErrTokenInvalid = errors.New("oauth: invalid access token")

	// ErrInsufficientScope is returned when the token is valid but lacks a required scope. -> 403.
	ErrInsufficientScope = errors.New("oauth: insufficient scope")

	// ErrGrantRevoked is returned when the merchant's grant for this client has been revoked
	// (or no longer exists). -> 403.
	ErrGrantRevoked = errors.New("oauth: grant revoked")

	// ErrGrantUnavailable is returned when the grant-status check could not be completed (DB
	// error with no usable cached value). It is NOT the client's fault, so it maps to 503 to
	// invite a retry rather than 401/403.
	ErrGrantUnavailable = errors.New("oauth: grant status unavailable")
)
