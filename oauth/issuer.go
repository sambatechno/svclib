package oauth

import "strings"

// SubdomainPlaceholder is the token a SERVICE_URI template uses in place of the per-tenant
// subdomain, e.g. "https://{subdomain}.eur1.samba-technologies.xyz/service". Stripping it leaves
// the region apex — which is the OAuth issuer.
const SubdomainPlaceholder = "{subdomain}"

// SchemeAndBaseDomain extracts the scheme and base domain (host[:port], minus the "{subdomain}."
// template prefix and any path) from a SERVICE_URI. Returns ("", "") when serviceURI has no
// scheme. Example: "https://{subdomain}.eur1.samba-technologies.xyz/service" -> ("https",
// "eur1.samba-technologies.xyz").
func SchemeAndBaseDomain(serviceURI string) (scheme, domain string) {
	i := strings.Index(serviceURI, "://")
	if i < 0 {
		return "", ""
	}
	scheme = serviceURI[:i]
	rest := serviceURI[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j] // stop the authority at path/query/fragment; keep host[:port]
	}
	domain = strings.TrimPrefix(rest, SubdomainPlaceholder+".")
	return scheme, domain
}

// IssuerFromServiceURI derives the OAuth issuer (scheme://<region-apex>) from a SERVICE_URI — the
// exact value middleware-service mints into the access token's `iss` claim (it derives its own
// issuer the same way). A resource service configures its Verifier with this so the issuer check
// matches *by construction* instead of a hand-set literal that can silently drift (the token's
// `iss` is derived, not a provisioned constant). Returns "" when serviceURI is unparseable, in
// which case the caller should fall back to an explicit OAUTH_ISSUER env value.
//
//	oauth.NewVerifier(oauth.Config{
//	    Issuer: oauth.IssuerFromServiceURI(os.Getenv("SERVICE_URI")),
//	    ...
//	})
func IssuerFromServiceURI(serviceURI string) string {
	scheme, domain := SchemeAndBaseDomain(serviceURI)
	if scheme == "" || domain == "" {
		return ""
	}
	return scheme + "://" + domain
}
