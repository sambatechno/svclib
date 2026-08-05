package oauth

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// defaultLeeway tolerates small clock skew between the issuer and a validator on exp/nbf/iat, so a
// validator whose clock is slightly behind does not wrongly reject a freshly minted token.
const defaultLeeway = 60 * time.Second

// Verifier validates an access token offline: RS256 signature (alg-pinned), issuer, audience
// membership and expiry, returning the trusted Claims. It performs NO I/O — no DB, no network, no
// call to the issuer — so it is safe on any hot path and adds no single point of failure. Scope
// enforcement and the optional revocation check live in the transport adapter, not here, so one
// verified token can serve endpoints with different scope requirements.
type Verifier interface {
	Verify(ctx context.Context, rawToken string) (*Claims, error)
}

// Config configures a Verifier. Keys, Issuer and Audience are required.
type Config struct {
	// Keys resolves the RSA public key by kid. Use StaticKey (issuer/single-key) or KeysFromPEM.
	Keys KeySource
	// Issuer is the expected iss claim (e.g. https://eur1.samba-technologies.xyz).
	Issuer string
	// Audience is THIS service's audience identity. The token's aud must contain it (membership,
	// not equality) — a token may legitimately list several audiences.
	Audience string
	// Leeway is the clock-skew tolerance for exp/nbf/iat. Defaults to 60s when zero.
	Leeway time.Duration
}

// NewVerifier builds a Verifier from cfg.
func NewVerifier(cfg Config) (Verifier, error) {
	switch {
	case cfg.Keys == nil:
		return nil, errors.New("oauth: Config.Keys is required")
	case cfg.Issuer == "":
		return nil, errors.New("oauth: Config.Issuer is required")
	case cfg.Audience == "":
		return nil, errors.New("oauth: Config.Audience is required")
	}
	leeway := cfg.Leeway
	if leeway <= 0 {
		leeway = defaultLeeway
	}
	return &verifier{keys: cfg.Keys, issuer: cfg.Issuer, audience: cfg.Audience, leeway: leeway}, nil
}

type verifier struct {
	keys     KeySource
	issuer   string
	audience string
	leeway   time.Duration
}

// tokenClaims is the JWT payload we parse. Embedding RegisteredClaims gives iss/sub/exp/iat/nbf
// and, via ClaimStrings, an aud that is either a JSON string or a JSON array (fosite emits either
// form). The region/tenant/client_id/scope fields are the read side of what the issuer writes
// (session.go Extra map + fosite's JWT strategy) — keep the json keys in lockstep with the
// Claim* constants.
type tokenClaims struct {
	jwt.RegisteredClaims
	Region   string `json:"region"`
	Tenant   string `json:"tenant"`
	Scope    string `json:"scope"`
	ClientID string `json:"client_id"`
}

func (v *verifier) Verify(_ context.Context, rawToken string) (*Claims, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return nil, ErrTokenMissing
	}

	var tc tokenClaims
	_, err := jwt.ParseWithClaims(rawToken, &tc, v.keyFunc,
		jwt.WithValidMethods([]string{"RS256"}), // pin RS256: blocks alg=none and RS/HS confusion
		jwt.WithIssuer(v.issuer),                // iss must match
		jwt.WithAudience(v.audience),            // our audience must be present in aud (membership)
		jwt.WithLeeway(v.leeway),                // clock-skew tolerance
		jwt.WithExpirationRequired(),            // reject tokens with no exp (fail closed)
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}
	if tc.Tenant == "" || tc.Subject == "" {
		return nil, fmt.Errorf("%w: missing tenant/subject", ErrTokenInvalid)
	}

	var exp time.Time
	if tc.ExpiresAt != nil {
		exp = tc.ExpiresAt.Time
	}
	return &Claims{
		Subject:   tc.Subject,
		ClientID:  tc.ClientID,
		Tenant:    tc.Tenant,
		Region:    tc.Region,
		Scopes:    strings.Fields(tc.Scope),
		Audience:  []string(tc.Audience),
		ExpiresAt: exp,
	}, nil
}

// keyFunc double-checks the signing method is RSA (WithValidMethods already pins RS256) and
// resolves the public key by the token's kid header ("" when absent -> the default key).
func (v *verifier) keyFunc(t *jwt.Token) (interface{}, error) {
	if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
		return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
	}
	kid, _ := t.Header["kid"].(string)
	return v.publicKey(kid)
}

func (v *verifier) publicKey(kid string) (*rsa.PublicKey, error) {
	return v.keys.PublicKey(kid)
}
