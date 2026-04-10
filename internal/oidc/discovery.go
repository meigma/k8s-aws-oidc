// Package oidc implements the small subset of OpenID Connect discovery and
// JWKS handling that AWS IAM uses to validate IRSA service-account tokens.
package oidc

import "encoding/json"

// JWKSPath is the fixed sub-path appended to the issuer URL to form jwks_uri.
const JWKSPath = "/openid/v1/jwks"

// discoveryDoc is the AWS-minimal OIDC discovery document. The supported-*
// slices are hardcoded — never sourced from upstream — so the response cannot
// be influenced by apiserver drift.
//
// claims_supported is included because the upstream Kubernetes apiserver does
// not emit it, but AWS IAM expects it. Field declaration order is the on-wire
// JSON order; do not reorder.
type discoveryDoc struct {
	Issuer                           string   `json:"issuer"`
	JWKSURI                          string   `json:"jwks_uri"`
	ResponseTypesSupported           []string `json:"response_types_supported"`
	SubjectTypesSupported            []string `json:"subject_types_supported"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported"`
	ClaimsSupported                  []string `json:"claims_supported"`
}

// Render returns the canonical JSON bytes to serve at
// /.well-known/openid-configuration. The issuer must already be a validated
// https:// URL with no trailing slash; jwks_uri is derived from it.
func Render(issuer string) ([]byte, error) {
	return json.Marshal(discoveryDoc{
		Issuer:                           issuer,
		JWKSURI:                          issuer + JWKSPath,
		ResponseTypesSupported:           []string{"id_token"},
		SubjectTypesSupported:            []string{"public"},
		IDTokenSigningAlgValuesSupported: []string{"RS256"},
		ClaimsSupported:                  []string{"aud", "iat", "iss", "sub"},
	})
}
