package oidc

import (
	"encoding/json"
	"testing"
)

func TestRender_ByteExact(t *testing.T) {
	got, err := Render("https://oidc.example.ts.net")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := `{"issuer":"https://oidc.example.ts.net","jwks_uri":"https://oidc.example.ts.net/openid/v1/jwks","response_types_supported":["id_token"],"subject_types_supported":["public"],"id_token_signing_alg_values_supported":["RS256"],"claims_supported":["aud","iat","iss","sub"]}`
	if string(got) != want {
		t.Errorf("Render mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestRender_AllAWSRequiredFields(t *testing.T) {
	body, err := Render("https://oidc.example.ts.net")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var doc map[string]any
	if uerr := json.Unmarshal(body, &doc); uerr != nil {
		t.Fatalf("unmarshal: %v", uerr)
	}

	required := []string{
		"issuer",
		"jwks_uri",
		"response_types_supported",
		"subject_types_supported",
		"id_token_signing_alg_values_supported",
		"claims_supported",
	}
	for _, key := range required {
		if _, ok := doc[key]; !ok {
			t.Errorf("missing required field %q", key)
		}
	}
}

func TestRender_JWKSURIDerivation(t *testing.T) {
	cases := []struct {
		issuer string
		want   string
	}{
		{"https://oidc.example.ts.net", "https://oidc.example.ts.net/openid/v1/jwks"},
		{"https://a.b.c", "https://a.b.c/openid/v1/jwks"},
	}
	for _, tc := range cases {
		body, rerr := Render(tc.issuer)
		if rerr != nil {
			t.Fatalf("Render(%q): %v", tc.issuer, rerr)
		}
		var doc struct {
			JWKSURI string `json:"jwks_uri"`
		}
		if uerr := json.Unmarshal(body, &doc); uerr != nil {
			t.Fatalf("unmarshal: %v", uerr)
		}
		if doc.JWKSURI != tc.want {
			t.Errorf("issuer %q: jwks_uri = %q, want %q", tc.issuer, doc.JWKSURI, tc.want)
		}
	}
}

func TestRender_HardcodedValues(t *testing.T) {
	body, err := Render("https://example")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var doc discoveryDoc
	if uerr := json.Unmarshal(body, &doc); uerr != nil {
		t.Fatalf("unmarshal: %v", uerr)
	}
	mustEq := func(got, want []string, name string) {
		if len(got) != len(want) {
			t.Errorf("%s: len = %d, want %d", name, len(got), len(want))
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s[%d] = %q, want %q", name, i, got[i], want[i])
			}
		}
	}
	mustEq(doc.ResponseTypesSupported, []string{"id_token"}, "response_types_supported")
	mustEq(doc.SubjectTypesSupported, []string{"public"}, "subject_types_supported")
	mustEq(doc.IDTokenSigningAlgValuesSupported, []string{"RS256"}, "id_token_signing_alg_values_supported")
	mustEq(doc.ClaimsSupported, []string{"aud", "iat", "iss", "sub"}, "claims_supported")
}
