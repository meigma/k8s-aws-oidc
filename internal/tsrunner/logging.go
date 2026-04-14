package tsrunner

import (
	"context"
	"errors"
	"log/slog"

	"github.com/meigma/k8s-aws-oidc/internal/logx"
)

const (
	metricsSuccess = "success"
	metricsFailure = "failure"
)

func logStateChange(ctx context.Context, logger *slog.Logger, state string) {
	if state == "" {
		return
	}
	logx.Info(ctx, logger, "tsnet_runner", "tsnet_state_change", "tsnet state changed",
		slog.String("state", state),
	)
}

func logStartFailure(ctx context.Context, logger *slog.Logger, state string, err error) {
	attrs := []slog.Attr{
		slog.String("error_kind", runnerErrorKind(err)),
		slog.String("error", runnerErrorSummary(err)),
	}
	if state != "" {
		attrs = append(attrs, slog.String("state", state))
	}
	logx.Error(ctx, logger, "tsnet_runner", "tsnet_start_failure", "tsnet start failed", attrs...)
}

func logListenerFailure(ctx context.Context, logger *slog.Logger, err error) {
	logx.Error(ctx, logger, "tsnet_runner", "public_listener_failure", "public listener failed",
		slog.String("error_kind", runnerErrorKind(err)),
		slog.String("error", runnerErrorSummary(err)),
	)
}

func logListenerRestart(ctx context.Context, logger *slog.Logger, reason string) {
	logx.Warn(ctx, logger, "tsnet_runner", "public_listener_restart", "restarting public listener",
		slog.String("reason", reason),
	)
}

func logStatePollFailure(ctx context.Context, logger *slog.Logger, err error) {
	logx.Warn(ctx, logger, "tsnet_runner", "tsnet_state_poll_failure", "tsnet state poll failed",
		slog.String("error_kind", runnerErrorKind(err)),
		slog.String("error", runnerErrorSummary(err)),
	)
}

func logHTTPShutdownFailure(ctx context.Context, logger *slog.Logger, err error) {
	logx.Warn(ctx, logger, "tsnet_runner", "http_shutdown_failure", "http shutdown failed",
		slog.String("error_kind", runnerErrorKind(err)),
		slog.String("error", runnerErrorSummary(err)),
	)
}

func logIssuerVerified(ctx context.Context, logger *slog.Logger, expectedHost string, domains []string) {
	logx.Info(ctx, logger, "tsnet_runner", "issuer_host_verified", "issuer host verified",
		slog.String("expected_host", expectedHost),
		slog.Any("cert_domains", append([]string(nil), domains...)),
		slog.Int("cert_domain_count", len(domains)),
	)
}

func logIssuerMismatch(ctx context.Context, logger *slog.Logger, expectedHost string, domains []string) {
	logx.Error(ctx, logger, "tsnet_runner", "issuer_host_mismatch", "issuer host mismatch",
		slog.String("expected_host", expectedHost),
		slog.Any("cert_domains", append([]string(nil), domains...)),
		slog.Int("cert_domain_count", len(domains)),
	)
}

func logMintFailure(ctx context.Context, logger *slog.Logger, err error) {
	logx.Error(ctx, logger, "tailscale_auth", "auth_key_mint_failure", "mint auth key failed",
		slog.String("error_kind", mintErrorKindOf(err)),
		slog.String("error", mintErrorSummary(err)),
	)
}

func runnerErrorKind(err error) string {
	switch {
	case err == nil:
		return "unexpected_state"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "operation_failed"
	}
}

func runnerErrorSummary(err error) string {
	switch runnerErrorKind(err) {
	case "unexpected_state":
		return "backend did not reach running"
	case "context_canceled":
		return "context canceled"
	case "deadline_exceeded":
		return "deadline exceeded"
	default:
		return "operation failed"
	}
}

func mintErrorKindOf(err error) string {
	var merr *mintError
	if errors.As(err, &merr) {
		return string(merr.kind)
	}
	return "mint_auth_key_failed"
}

func mintErrorSummary(err error) string {
	var merr *mintError
	if errors.As(err, &merr) {
		switch merr.kind {
		case mintErrMissingCredentials:
			return "missing client credentials"
		case mintErrMissingTags:
			return "missing tags"
		case mintErrCreateKey:
			return "create auth key failed"
		}
	}
	return "mint auth key failed"
}
