package oauth

import "time"

// Claims are the validated, trusted outputs of Verify: the identity and authority a request
// carries once signature, issuer, audience and expiry have all passed. A handler reads these via
// ClaimsFrom(ctx). Region/Tenant come straight from the token, so a resource service can route to
// the correct tenant schema (`tenant<Tenant>`) with no DB lookup.
type Claims struct {
	Subject   string    // sub — the merchant grant subject (stable revocation key)
	ClientID  string    // client_id — the partner app that obtained the token
	Tenant    string    // tenant claim = tenant schema id
	Region    string    // region claim (e.g. eur1, sgp1)
	Scopes    []string  // parsed from the space-delimited `scope` claim
	Audience  []string  // aud (may be multi-valued)
	ExpiresAt time.Time // exp
}

// HasScope reports whether the token was granted scope s.
func (c *Claims) HasScope(s string) bool {
	for _, got := range c.Scopes {
		if got == s {
			return true
		}
	}
	return false
}

// hasAllScopes reports whether every scope in required is present on the token (subset check).
func (c *Claims) hasAllScopes(required []string) bool {
	for _, r := range required {
		if !c.HasScope(r) {
			return false
		}
	}
	return true
}
