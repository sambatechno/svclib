// Package oauth validates Cata OAuth 2.0 access tokens (the StreamOrders integration and any
// future partner-facing surface). It is the shared, single source of truth for token validation:
// the issuer (middleware-service) mints RS256 JWT access tokens, and any resource service verifies
// them here — offline, with just the RSA public key — so authentication never depends on a call to
// middleware (no single point of failure). The only optional server-side dependency is a
// grant-status revocation check against the same MySQL every service already talks to.
package oauth

// Custom JWT claim keys. The issuer (middleware-service, session.go) writes region/tenant into the
// access token's Extra map; every validator reads them from here so issuer and validators cannot
// drift. scope is the standard OAuth scope claim, kept here for symmetry.
const (
	ClaimRegion = "region"
	ClaimTenant = "tenant"
	ClaimScope  = "scope"
)

// Well-known token vocabulary (D-11/D-12), relocated from middleware's provider.go so the issuer
// and every validator reference one definition. These are the values in use today; new surfaces
// add their own audience/scope strings without touching this library.
const (
	ScopeOrders      = "orders"
	AudienceOrdering = "ordering-api"
)
