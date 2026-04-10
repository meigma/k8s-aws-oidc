package oidc

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	validN = "u1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gunVTLw7onLRnrq0_IzW7yWR7QkrmBL7jTKEn5u-qKhbwKfBstIs-bMY2Zkp18gnTxKLxoS2tFczGkPLPgizskuemMghRniWaoLcyehkd3qqGElvW_VDL5AaWTg0nLVkjRo9z-40RQzuVaE8AkAFmxZzow3x-VJYKdjykkJ0iT9wCS0DRTXu269V264Vf_3jvredZiKRkgwlL9xNAwxXFg0x_XFw005UWVRIkdgcKWTjpBP2dPwVZ4WWC-9aGVd-Gyn1o0CLelf4rEjGoXbAAEgAqeGUxrcIlbjXfbcmw"
	validE = "AQAB"
)

func validJWKS(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(JWKS{
		Keys: []JWK{
			{Kid: "k1", Kty: "RSA", Alg: "RS256", Use: "sig", N: validN, E: validE},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

func TestParseAndValidate_HappyPath(t *testing.T) {
	got, err := ParseAndValidate(validJWKS(t))
	if err != nil {
		t.Fatalf("ParseAndValidate: %v", err)
	}
	if len(got.Keys) != 1 {
		t.Fatalf("len(keys) = %d", len(got.Keys))
	}
	if got.Keys[0].Kid != "k1" {
		t.Errorf("kid = %q", got.Keys[0].Kid)
	}
	if got.Keys[0].N != validN {
		t.Errorf("n not preserved byte-for-byte")
	}
	if got.Keys[0].E != validE {
		t.Errorf("e not preserved byte-for-byte")
	}
}

func TestParseAndValidate_MultiKey(t *testing.T) {
	body, _ := json.Marshal(JWKS{
		Keys: []JWK{
			{Kid: "k1", Kty: "RSA", Alg: "RS256", Use: "sig", N: validN, E: validE},
			{Kid: "k2", Kty: "RSA", Alg: "RS256", Use: "sig", N: validN, E: validE},
		},
	})
	got, err := ParseAndValidate(body)
	if err != nil {
		t.Fatalf("ParseAndValidate: %v", err)
	}
	if len(got.Keys) != 2 {
		t.Fatalf("len(keys) = %d", len(got.Keys))
	}
}

func TestParseAndValidate_Marshal_Roundtrip(t *testing.T) {
	in := validJWKS(t)
	parsed, err := ParseAndValidate(in)
	if err != nil {
		t.Fatalf("ParseAndValidate: %v", err)
	}
	out, err := parsed.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Re-parse the output and verify it survives a second round.
	if _, perr := ParseAndValidate(out); perr != nil {
		t.Fatalf("re-parse: %v", perr)
	}
}

func TestParseAndValidate_Rejects(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			"missing_kid",
			`{"keys":[{"kid":"","kty":"RSA","alg":"RS256","use":"sig","n":"` + validN + `","e":"` + validE + `"}]}`,
		},
		{"missing_n", `{"keys":[{"kid":"k1","kty":"RSA","alg":"RS256","use":"sig","n":"","e":"` + validE + `"}]}`},
		{"missing_e", `{"keys":[{"kid":"k1","kty":"RSA","alg":"RS256","use":"sig","n":"` + validN + `","e":""}]}`},
		{
			"bad_use",
			`{"keys":[{"kid":"k1","kty":"RSA","alg":"RS256","use":"enc","n":"` + validN + `","e":"` + validE + `"}]}`,
		},
		{
			"bad_alg",
			`{"keys":[{"kid":"k1","kty":"RSA","alg":"HS256","use":"sig","n":"` + validN + `","e":"` + validE + `"}]}`,
		},
		{
			"bad_kty",
			`{"keys":[{"kid":"k1","kty":"EC","alg":"RS256","use":"sig","n":"` + validN + `","e":"` + validE + `"}]}`,
		},
		{
			"n_not_base64url",
			`{"keys":[{"kid":"k1","kty":"RSA","alg":"RS256","use":"sig","n":"!!!notb64!!!","e":"` + validE + `"}]}`,
		},
		{"empty_keys", `{"keys":[]}`},
		{
			"unknown_top_field",
			`{"keys":[{"kid":"k1","kty":"RSA","alg":"RS256","use":"sig","n":"` + validN + `","e":"` + validE + `"}],"extra":1}`,
		},
		{
			"unknown_key_field",
			`{"keys":[{"kid":"k1","kty":"RSA","alg":"RS256","use":"sig","n":"` + validN + `","e":"` + validE + `","x5c":["junk"]}]}`,
		},
		{"malformed_json", `{"keys":[{`},
		{"truncated", `{"keys":[{"kid":"k1","kty":"RSA"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAndValidate([]byte(tc.body))
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestParseAndValidate_DuplicateKid(t *testing.T) {
	body, _ := json.Marshal(JWKS{
		Keys: []JWK{
			{Kid: "k1", Kty: "RSA", Alg: "RS256", Use: "sig", N: validN, E: validE},
			{Kid: "k1", Kty: "RSA", Alg: "RS256", Use: "sig", N: validN, E: validE},
		},
	})
	_, err := ParseAndValidate(body)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate kid error, got %v", err)
	}
}

func TestParseAndValidate_TooManyKeys(t *testing.T) {
	keys := make([]JWK, MaxJWKSKeys+1)
	for i := range keys {
		keys[i] = JWK{Kid: kidN(i), Kty: "RSA", Alg: "RS256", Use: "sig", N: validN, E: validE}
	}
	body, _ := json.Marshal(JWKS{Keys: keys})
	_, err := ParseAndValidate(body)
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("want too-many-keys error, got %v", err)
	}
}

func TestParseAndValidate_BoundaryAtMax(t *testing.T) {
	keys := make([]JWK, MaxJWKSKeys)
	for i := range keys {
		keys[i] = JWK{Kid: kidN(i), Kty: "RSA", Alg: "RS256", Use: "sig", N: validN, E: validE}
	}
	body, _ := json.Marshal(JWKS{Keys: keys})
	out, err := ParseAndValidate(body)
	if err != nil {
		t.Fatalf("expected success at boundary, got %v", err)
	}
	if len(out.Keys) != MaxJWKSKeys {
		t.Fatalf("len = %d", len(out.Keys))
	}
}

func kidN(i int) string {
	const hex = "0123456789abcdef"
	a := byte(i / 16)
	b := byte(i % 16)
	return string([]byte{'k', hex[a], hex[b]})
}
