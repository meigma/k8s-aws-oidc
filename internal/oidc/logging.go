package oidc

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/meigma/k8s-aws-oidc/internal/logx"
)

func logJWKSSuccess(ctx context.Context, logger *slog.Logger, event string, jwks *JWKS) {
	logx.Info(ctx, logger, "jwks_cache", event, "jwks cache updated",
		slog.Int("kid_count", len(jwks.Keys)),
		slog.Any("kids", sortedKids(jwks)),
	)
}

func logJWKSFailure(ctx context.Context, logger *slog.Logger, level slog.Level, event string, err error) {
	logx.Log(ctx, logger, level, "jwks_cache", event, "jwks cache update failed", jwksErrorAttrs(err)...)
}

func sortedKids(jwks *JWKS) []string {
	kids := make([]string, 0, len(jwks.Keys))
	for _, key := range jwks.Keys {
		kids = append(kids, key.Kid)
	}
	slices.Sort(kids)
	return kids
}

func jwksErrorAttrs(err error) []slog.Attr {
	var ferr *fetchError
	if errors.As(err, &ferr) {
		attrs := []slog.Attr{
			slog.String("error_kind", jwksErrorKind(err)),
			slog.String("error", safeFetchSummary(ferr.kind)),
		}
		if ferr.statusCode != 0 {
			attrs = append(attrs, slog.Int("status_code", ferr.statusCode))
		}
		if ferr.bodySizeBytes != 0 {
			attrs = append(attrs, slog.Int("body_size_bytes", ferr.bodySizeBytes))
		}
		return attrs
	}
	return []slog.Attr{
		slog.String("error_kind", jwksErrorKind(err)),
		slog.String("error", "jwks fetch failed"),
	}
}

func jwksErrorKind(err error) string {
	var ferr *fetchError
	if errors.As(err, &ferr) {
		return string(ferr.kind)
	}
	return "fetch_failed"
}

func safeFetchSummary(kind fetchErrorKind) string {
	switch kind {
	case fetchErrBuildRequest:
		return "build request failed"
	case fetchErrUpstream:
		return "upstream request failed"
	case fetchErrUpstreamCode:
		return "unexpected upstream status"
	case fetchErrReadBody:
		return "read response body failed"
	case fetchErrBodyTooLarge:
		return "response body too large"
	case fetchErrJWKSInvalid:
		return "jwks validation failed"
	case fetchErrTokenRead:
		return "read service account token failed"
	case fetchErrTokenEmpty:
		return "service account token file empty"
	default:
		return "jwks refresh failed"
	}
}
