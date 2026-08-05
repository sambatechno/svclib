package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://eur1.samba-technologies.xyz"
	testAudience = AudienceOrdering
)

// mintToken builds a signed RS256 access token that mirrors what middleware issues. opts mutate
// the claims/header so individual tests can produce malformed or expired variants.
func mintToken(t *testing.T, key *rsa.PrivateKey, mut func(claims jwt.MapClaims, hdr map[string]interface{})) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":       testIssuer,
		"sub":       "merchant-1",
		"aud":       testAudience,
		"exp":       now.Add(time.Hour).Unix(),
		"iat":       now.Unix(),
		"region":    "eur1",
		"tenant":    "qapoints",
		"scope":     ScopeOrders,
		"client_id": "stream-sandbox",
	}
	header := map[string]interface{}{}
	if mut != nil {
		mut(claims, header)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	for k, val := range header {
		tok.Header[k] = val
	}
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func newTestVerifier(t *testing.T, pub *rsa.PublicKey) Verifier {
	t.Helper()
	v, err := NewVerifier(Config{Keys: StaticKey(pub), Issuer: testIssuer, Audience: testAudience})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func TestVerify_Valid(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := newTestVerifier(t, &key.PublicKey)

	claims, err := v.Verify(context.Background(), mintToken(t, key, nil))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "merchant-1" || claims.Tenant != "qapoints" || claims.Region != "eur1" {
		t.Errorf("unexpected claims: %+v", claims)
	}
	if claims.ClientID != "stream-sandbox" {
		t.Errorf("client_id = %q, want stream-sandbox", claims.ClientID)
	}
	if !claims.HasScope(ScopeOrders) {
		t.Errorf("scopes = %v, want to contain %q", claims.Scopes, ScopeOrders)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != testAudience {
		t.Errorf("aud = %v, want [%q]", claims.Audience, testAudience)
	}
}

func TestVerify_AudienceArray(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := newTestVerifier(t, &key.PublicKey)

	// fosite may emit aud as an array — our audience must still be found by membership.
	tok := mintToken(t, key, func(c jwt.MapClaims, _ map[string]interface{}) {
		c["aud"] = []string{"other-api", testAudience}
	})
	claims, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(claims.Audience) != 2 {
		t.Errorf("aud = %v, want 2 entries", claims.Audience)
	}
}

func TestVerify_Rejections(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := newTestVerifier(t, &key.PublicKey)

	cases := []struct {
		name string
		tok  string
	}{
		{"empty", ""},
		{"garbage", "not-a-jwt"},
		{"wrong-key", mintToken(t, other, nil)},
		{"expired", mintToken(t, key, func(c jwt.MapClaims, _ map[string]interface{}) {
			c["exp"] = time.Now().Add(-2 * time.Hour).Unix()
		})},
		{"no-exp", mintToken(t, key, func(c jwt.MapClaims, _ map[string]interface{}) {
			delete(c, "exp")
		})},
		{"wrong-issuer", mintToken(t, key, func(c jwt.MapClaims, _ map[string]interface{}) {
			c["iss"] = "https://evil.example.com"
		})},
		{"wrong-audience", mintToken(t, key, func(c jwt.MapClaims, _ map[string]interface{}) {
			c["aud"] = "some-other-api"
		})},
		{"missing-tenant", mintToken(t, key, func(c jwt.MapClaims, _ map[string]interface{}) {
			delete(c, "tenant")
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Verify(context.Background(), tc.tok)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			wantMissing := tc.name == "empty"
			if wantMissing && !errors.Is(err, ErrTokenMissing) {
				t.Errorf("err = %v, want ErrTokenMissing", err)
			}
			if !wantMissing && !errors.Is(err, ErrTokenInvalid) {
				t.Errorf("err = %v, want ErrTokenInvalid", err)
			}
		})
	}
}

func TestVerify_AlgConfusionRejected(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	v := newTestVerifier(t, &key.PublicKey)

	// Forge an HS256 token using the RSA public key bytes as the HMAC secret — the classic
	// alg-confusion attack. Pinning RS256 must reject it.
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": testIssuer, "sub": "x", "aud": testAudience, "tenant": "t",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	pubDER, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	forged, err := hs.SignedString(pubDER)
	if err != nil {
		t.Fatalf("sign hs: %v", err)
	}
	if _, err := v.Verify(context.Background(), forged); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("alg-confusion token: err = %v, want ErrTokenInvalid", err)
	}
}

func TestNewVerifier_Validation(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"no-keys", Config{Issuer: testIssuer, Audience: testAudience}},
		{"no-issuer", Config{Keys: StaticKey(&key.PublicKey), Audience: testAudience}},
		{"no-audience", Config{Keys: StaticKey(&key.PublicKey), Issuer: testIssuer}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewVerifier(tc.cfg); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}
