package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// pubToBase64PEM encodes a public key the way it is distributed via Infisical: PKIX PEM, then
// base64 over the whole PEM block (single line).
func pubToBase64PEM(t *testing.T, pub *rsa.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return base64.StdEncoding.EncodeToString(pemBytes)
}

func TestKeysFromPEM_RoundTrip(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	ks, err := KeysFromPEM(map[string]string{"": pubToBase64PEM(t, &key.PublicKey)})
	if err != nil {
		t.Fatalf("KeysFromPEM: %v", err)
	}
	got, err := ks.PublicKey("")
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if got.N.Cmp(key.PublicKey.N) != 0 {
		t.Errorf("round-tripped key does not match original")
	}
}

func TestKeysFromPEM_Errors(t *testing.T) {
	if _, err := KeysFromPEM(nil); err == nil {
		t.Errorf("expected error for empty map")
	}
	if _, err := KeysFromPEM(map[string]string{"k": "!!!not base64!!!"}); err == nil {
		t.Errorf("expected error for bad base64")
	}
	if _, err := KeysFromPEM(map[string]string{"k": base64.StdEncoding.EncodeToString([]byte("not pem"))}); err == nil {
		t.Errorf("expected error for non-PEM payload")
	}
}

// TestVerify_KidRotation proves a validator holding two keys picks the right one by the token's
// kid header — the graceful-rotation path.
func TestVerify_KidRotation(t *testing.T) {
	oldKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	newKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	ks, err := KeysFromPEM(map[string]string{
		"2025": pubToBase64PEM(t, &oldKey.PublicKey),
		"2026": pubToBase64PEM(t, &newKey.PublicKey),
	})
	if err != nil {
		t.Fatalf("KeysFromPEM: %v", err)
	}
	v, err := NewVerifier(Config{Keys: ks, Issuer: testIssuer, Audience: testAudience})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	for kid, key := range map[string]*rsa.PrivateKey{"2025": oldKey, "2026": newKey} {
		tok := mintToken(t, key, func(_ jwt.MapClaims, hdr map[string]interface{}) {
			hdr["kid"] = kid
		})
		if _, err := v.Verify(context.Background(), tok); err != nil {
			t.Errorf("kid %s: Verify failed: %v", kid, err)
		}
	}

	// A token signed by newKey but stamped with the old kid must fail (key mismatch).
	wrong := mintToken(t, newKey, func(_ jwt.MapClaims, hdr map[string]interface{}) {
		hdr["kid"] = "2025"
	})
	if _, err := v.Verify(context.Background(), wrong); err == nil {
		t.Errorf("expected failure for kid/key mismatch")
	}
}
