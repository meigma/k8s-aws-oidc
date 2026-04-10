// Package tsrunner contains the runtime wiring around tsnet: bringing the
// Funnel listener up, serving the HTTP handler, minting auth keys via OAuth
// client credentials, and reacting to lost-identity events with a fresh key.
package tsrunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

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

// Mint creates a single fresh ephemeral, preauthorized, tagged auth key and
// returns it. Errors are returned as-is for the runner to decide on backoff.
func (m *OAuthMinter) Mint(ctx context.Context) (string, error) {
	if m.ClientID == "" || m.ClientSecret == "" {
		return "", errors.New("oauth minter: client id and secret required")
	}
	if len(m.Tags) == 0 {
		return "", errors.New("oauth minter: at least one tag required")
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
		return "", fmt.Errorf("create auth key: %w", err)
	}
	logger.InfoContext(ctx, "minted ephemeral auth key", "tags", m.Tags)
	return key, nil
}
