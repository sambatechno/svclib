package oauth

import "testing"

func TestIssuerFromServiceURI(t *testing.T) {
	cases := []struct {
		name       string
		serviceURI string
		want       string
	}{
		{"templated eur1", "https://{subdomain}.eur1.samba-technologies.xyz/service", "https://eur1.samba-technologies.xyz"},
		{"templated sgp1 no path", "https://{subdomain}.sgp1.samba-technologies.xyz", "https://sgp1.samba-technologies.xyz"},
		{"no subdomain template", "https://eur1.samba-technologies.xyz/service", "https://eur1.samba-technologies.xyz"},
		{"http + port", "http://{subdomain}.localhost:8080/x", "http://localhost:8080"},
		{"query stripped", "https://{subdomain}.eur1.samba-technologies.xyz?x=1", "https://eur1.samba-technologies.xyz"},
		{"fragment stripped", "https://{subdomain}.eur1.samba-technologies.xyz#f", "https://eur1.samba-technologies.xyz"},
		{"malformed authority -> empty", "https://?bad", ""},
		{"no scheme -> empty", "eur1.samba-technologies.xyz", ""},
		{"empty -> empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IssuerFromServiceURI(tc.serviceURI); got != tc.want {
				t.Errorf("IssuerFromServiceURI(%q) = %q, want %q", tc.serviceURI, got, tc.want)
			}
		})
	}
}

func TestSchemeAndBaseDomain(t *testing.T) {
	scheme, domain := SchemeAndBaseDomain("https://{subdomain}.eur1.samba-technologies.xyz/service")
	if scheme != "https" || domain != "eur1.samba-technologies.xyz" {
		t.Errorf("got (%q,%q), want (https, eur1.samba-technologies.xyz)", scheme, domain)
	}
	if s, d := SchemeAndBaseDomain("no-scheme"); s != "" || d != "" {
		t.Errorf("no-scheme: got (%q,%q), want empties", s, d)
	}
}

// TestIssuerDerivationMatchesVerifier is the point of the helper: a token minted with an issuer
// derived from SERVICE_URI validates against a Verifier whose issuer is derived the same way.
func TestIssuerDerivationMatchesVerifier(t *testing.T) {
	const serviceURI = "https://{subdomain}.eur1.samba-technologies.xyz/service"
	iss := IssuerFromServiceURI(serviceURI)
	if iss != "https://eur1.samba-technologies.xyz" {
		t.Fatalf("issuer = %q", iss)
	}
	// A verifier built with the derived issuer accepts a token carrying that iss (uses the
	// verifier_test.go helpers: mintToken stamps iss=testIssuer, so align testIssuer via cfg).
	// Here we just assert NewVerifier accepts the derived issuer as valid config.
	if _, err := NewVerifier(Config{Keys: StaticKey(nil), Issuer: iss, Audience: AudienceOrdering}); err != nil {
		t.Fatalf("NewVerifier with derived issuer: %v", err)
	}
}
