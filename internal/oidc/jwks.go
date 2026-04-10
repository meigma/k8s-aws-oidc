package oidc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// MaxJWKSKeys caps the total number of keys we will accept from upstream.
	// AWS supports at most 100 RSA + 100 EC keys per OIDC provider; this cut
	// only handles RSA so the practical cap is 100, but 200 leaves headroom
	// without softening the upstream-corruption defense.
	MaxJWKSKeys = 200

	// MaxJWKSBodyBytes caps the upstream response body. Real k8s JWKS bodies
	// are well under 10 KiB even with many keys.
	MaxJWKSBodyBytes = 1 << 20

	// AlgRS256 is the only signing algorithm we accept.
	AlgRS256 = "RS256"

	// KtyRSA is the only key type we accept.
	KtyRSA = "RSA"

	// UseSig is the only key use we accept.
	UseSig = "sig"
)

// JWK is the canonical re-emitted shape for a single RSA signing key.
// Field declaration order is the on-wire JSON order; do not reorder.
type JWK struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKS is the canonical re-emitted JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// Marshal returns the canonical (compact, deterministic) JSON serialization.
func (j *JWKS) Marshal() ([]byte, error) {
	return json.Marshal(j)
}

// ParseAndValidate decodes the upstream JSON, enforces the allowlist, and
// returns a clean re-emit-ready JWKS. Unknown JSON fields at either the
// top level or per-key are rejected.
func ParseAndValidate(body []byte) (*JWKS, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	var raw JWKS
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("jwks: decode: %w", err)
	}
	if dec.More() {
		return nil, errors.New("jwks: trailing data after JSON document")
	}

	if len(raw.Keys) == 0 {
		return nil, errors.New("jwks: empty key set")
	}
	if len(raw.Keys) > MaxJWKSKeys {
		return nil, fmt.Errorf("jwks: too many keys: %d (max %d)", len(raw.Keys), MaxJWKSKeys)
	}

	seen := make(map[string]struct{}, len(raw.Keys))
	out := &JWKS{Keys: make([]JWK, 0, len(raw.Keys))}
	for i, k := range raw.Keys {
		if err := validateKey(k); err != nil {
			return nil, fmt.Errorf("jwks: key[%d]: %w", i, err)
		}
		if _, dup := seen[k.Kid]; dup {
			return nil, fmt.Errorf("jwks: key[%d]: duplicate kid %q", i, k.Kid)
		}
		seen[k.Kid] = struct{}{}
		out.Keys = append(out.Keys, JWK{
			Kid: k.Kid,
			Kty: k.Kty,
			Alg: k.Alg,
			Use: k.Use,
			N:   k.N,
			E:   k.E,
		})
	}
	return out, nil
}

func validateKey(k JWK) error {
	if k.Kid == "" {
		return errors.New("missing kid")
	}
	if k.Kty != KtyRSA {
		return fmt.Errorf("kty %q not allowed (want %q)", k.Kty, KtyRSA)
	}
	if k.Alg != AlgRS256 {
		return fmt.Errorf("alg %q not allowed (want %q)", k.Alg, AlgRS256)
	}
	if k.Use != UseSig {
		return fmt.Errorf("use %q not allowed (want %q)", k.Use, UseSig)
	}
	if k.N == "" {
		return errors.New("missing n")
	}
	if k.E == "" {
		return errors.New("missing e")
	}
	if b, err := base64.RawURLEncoding.DecodeString(k.N); err != nil || len(b) == 0 {
		return fmt.Errorf("n is not valid base64url: %w", err)
	}
	if b, err := base64.RawURLEncoding.DecodeString(k.E); err != nil || len(b) == 0 {
		return fmt.Errorf("e is not valid base64url: %w", err)
	}
	return nil
}
