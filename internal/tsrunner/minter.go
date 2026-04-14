// Package tsrunner contains the runtime wiring around tsnet: bringing the
// Funnel listener up, serving the HTTP handler, minting auth keys via OAuth
// client credentials, and reacting to lost-identity events with a fresh key.
package tsrunner

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/meigma/k8s-aws-oidc/internal/logx"
	"github.com/meigma/k8s-aws-oidc/internal/metrics"
	"golang.org/x/oauth2/clientcredentials"
	//nolint:staticcheck // SA1019: tailscale.com/client/tailscale is bundled with tsnet (already a transitive dep) and the v2 client lives in a separate module. Isolated behind AuthKeyMinter so a future swap is one file.
	"tailscale.com/client/tailscale"
)

// AuthKeyMinter mints a fresh ephemeral, tagged, preauthorized auth key.
// It is the seam that lets the runner request a new key when tsnet reports
// NeedsLogin without coupling the runner to a specific control-plane client.
type AuthKeyMinter interface {
	Mint(ctx context.Context) (string, error)
}

// OAuthMinter mints auth keys via the Tailscale control plane API using
// OAuth 2.0 client credentials.
type OAuthMinter struct {
	ClientID     string
	ClientSecret string
	Tags         []string
	BaseURL      string
	Logger       *slog.Logger
	Metrics      *metrics.Metrics
}

// ackUnstableOnce sets the unlock flag on the deprecated client/tailscale
// package exactly once per process. The package is bundled with tsnet, so it
// adds zero new direct dependencies, and the AuthKeyMinter interface lets us
// migrate to github.com/tailscale/tailscale-client-go/v2 later by editing
// only this file.
//
//nolint:gochecknoglobals // sync.Once is intentionally process-wide; the flag it guards is also process-wide.
var ackUnstableOnce sync.Once

const defaultTSAPIBaseURL = "https://api.tailscale.com"

type mintErrorKind string

const (
	mintErrMissingCredentials mintErrorKind = "missing_client_credentials"
	mintErrMissingTags        mintErrorKind = "missing_tags"
	mintErrCreateKey          mintErrorKind = "create_auth_key_failed"
)

type mintError struct {
	kind mintErrorKind
	err  error
}

func (e *mintError) Error() string {
	switch e.kind {
	case mintErrMissingCredentials:
		return "oauth minter: client id and secret required"
	case mintErrMissingTags:
		return "oauth minter: at least one tag required"
	case mintErrCreateKey:
		return fmt.Sprintf("create auth key: %v", e.err)
	default:
		if e.err != nil {
			return e.err.Error()
		}
		return "mint auth key failed"
	}
}

func (e *mintError) Unwrap() error { return e.err }

// Mint creates a single fresh ephemeral, preauthorized, tagged auth key and
// returns it. Errors are returned as-is for the runner to decide on backoff.
func (m *OAuthMinter) Mint(ctx context.Context) (string, error) {
	if m.ClientID == "" || m.ClientSecret == "" {
		err := &mintError{kind: mintErrMissingCredentials}
		if m.Metrics != nil {
			m.Metrics.RecordAuthKeyMint(metricsFailure, mintErrorKindOf(err))
		}
		logMintFailure(ctx, m.Logger, err)
		return "", err
	}
	if len(m.Tags) == 0 {
		err := &mintError{kind: mintErrMissingTags}
		if m.Metrics != nil {
			m.Metrics.RecordAuthKeyMint(metricsFailure, mintErrorKindOf(err))
		}
		logMintFailure(ctx, m.Logger, err)
		return "", err
	}

	logger := m.Logger
	if logger == nil {
		logger = slog.Default()
	}

	baseURL := m.BaseURL
	if baseURL == "" {
		baseURL = defaultTSAPIBaseURL
	}

	creds := clientcredentials.Config{
		ClientID:     m.ClientID,
		ClientSecret: m.ClientSecret,
		TokenURL:     baseURL + "/api/v2/oauth/token",
	}

	//nolint:reassign // I_Acknowledge_This_API_Is_Unstable is the documented opt-in for the deprecated package; setting it once at process start is the API contract.
	ackUnstableOnce.Do(func() { tailscale.I_Acknowledge_This_API_Is_Unstable = true })

	//nolint:staticcheck // SA1019: see import comment.
	c := tailscale.NewClient("-", nil)
	c.HTTPClient = creds.Client(ctx)
	c.BaseURL = baseURL

	caps := tailscale.KeyCapabilities{
		Devices: tailscale.KeyDeviceCapabilities{
			Create: tailscale.KeyDeviceCreateCapabilities{
				Ephemeral:     true,
				Preauthorized: true,
				Tags:          m.Tags,
			},
		},
	}

	key, _, err := c.CreateKey(ctx, caps)
	if err != nil {
		mintErr := &mintError{kind: mintErrCreateKey, err: err}
		if m.Metrics != nil {
			m.Metrics.RecordAuthKeyMint(metricsFailure, mintErrorKindOf(mintErr))
		}
		logMintFailure(ctx, logger, mintErr)
		return "", mintErr
	}
	if m.Metrics != nil {
		m.Metrics.RecordAuthKeyMint(metricsSuccess, "")
	}
	logx.Info(ctx, logger, "tailscale_auth", "auth_key_mint_success", "minted auth key",
		slog.Any("tags", append([]string(nil), m.Tags...)),
		slog.Int("tag_count", len(m.Tags)),
	)
	return key, nil
}
