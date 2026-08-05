package oauth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// KeySource resolves the RSA public key used to verify a token's signature. The kid ("" for the
// default/only key) is taken from the JWT header, so a validator can hold several keys at once and
// pick the right one during a signing-key rotation. Implementations must be safe for concurrent
// use; a Verifier calls PublicKey on every request.
type KeySource interface {
	PublicKey(kid string) (*rsa.PublicKey, error)
}

// StaticKey is a KeySource backed by a single public key, returned regardless of kid. This is how
// the issuer (middleware-service) validates through the same path partners do: it passes
// &privKey.PublicKey, which it already holds.
func StaticKey(pub *rsa.PublicKey) KeySource {
	return staticKey{pub: pub}
}

type staticKey struct{ pub *rsa.PublicKey }

func (s staticKey) PublicKey(string) (*rsa.PublicKey, error) {
	if s.pub == nil {
		return nil, errors.New("oauth: nil public key")
	}
	return s.pub, nil
}

// KeysFromPEM builds a kid-keyed KeySource from base64-encoded PEM RSA public keys — the format
// distributed out-of-band via Infisical (OAUTH_RS256_PUBLIC_KEY), matching how middleware receives
// its base64-PEM private key. Use "" as the map key for the default key, which matches tokens that
// carry no kid header. During a rotation, publish both the old and new kids here.
func KeysFromPEM(byKID map[string]string) (KeySource, error) {
	if len(byKID) == 0 {
		return nil, errors.New("oauth: KeysFromPEM: no keys provided")
	}
	keys := make(map[string]*rsa.PublicKey, len(byKID))
	for kid, encoded := range byKID {
		pub, err := parseRSAPublicKey(encoded)
		if err != nil {
			return nil, fmt.Errorf("oauth: key %q: %w", kid, err)
		}
		keys[kid] = pub
	}
	return mapKeys(keys), nil
}

type mapKeys map[string]*rsa.PublicKey

func (m mapKeys) PublicKey(kid string) (*rsa.PublicKey, error) {
	if k, ok := m[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("oauth: no public key for kid %q", kid)
}

// parseRSAPublicKey decodes a base64-encoded PEM (PKIX or PKCS#1) RSA public key. The base64 wrap
// mirrors middleware's single-line base64-PEM key format (utils/config), so the same distribution
// tooling produces both the private key middleware loads and the public key validators load.
func parseRSAPublicKey(encoded string) (*rsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	block, _ := pem.Decode(der)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("not an RSA public key")
		}
		return rsaPub, nil
	}
	// Fall back to the PKCS#1 (RSA PUBLIC KEY) encoding.
	pub, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key (tried PKIX and PKCS#1): %w", err)
	}
	return pub, nil
}
