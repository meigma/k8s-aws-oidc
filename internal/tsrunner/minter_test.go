package tsrunner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeTSAPI emulates the two endpoints OAuthMinter calls.
type fakeTSAPI struct {
	tokenCalls atomic.Int32
	keyCalls   atomic.Int32

	mu          sync.Mutex
	lastKeyBody []byte
}

func (f *fakeTSAPI) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		f.tokenCalls.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("oauth/token method = %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "grant_type=client_credentials") {
			t.Errorf("oauth/token body missing grant_type: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"oauth-access-token","token_type":"Bearer","expires_in":3600}`)
	})
	mux.HandleFunc("/api/v2/tailnet/-/keys", func(w http.ResponseWriter, r *http.Request) {
		f.keyCalls.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("keys method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-access-token" {
			t.Errorf("Authorization = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.lastKeyBody = append([]byte(nil), body...)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"key123","key":"tskey-auth-fresh","capabilities":{}}`)
	})
	return mux
}

func TestOAuthMinter_HappyPath(t *testing.T) {
	api := &fakeTSAPI{}
	srv := httptest.NewServer(api.handler(t))
	defer srv.Close()

	m := &OAuthMinter{
		ClientID:     "id",
		ClientSecret: "secret",
		Tags:         []string{"tag:oidc-proxy"},
		BaseURL:      srv.URL,
	}
	key, err := m.Mint(context.Background())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if key != "tskey-auth-fresh" {
		t.Errorf("key = %q", key)
	}
	if api.tokenCalls.Load() == 0 {
		t.Error("oauth/token not called")
	}
	if api.keyCalls.Load() == 0 {
		t.Error("create key not called")
	}

	api.mu.Lock()
	body := api.lastKeyBody
	api.mu.Unlock()

	var req struct {
		Capabilities struct {
			Devices struct {
				Create struct {
					Ephemeral     bool     `json:"ephemeral"`
					Preauthorized bool     `json:"preauthorized"`
					Tags          []string `json:"tags"`
				} `json:"create"`
			} `json:"devices"`
		} `json:"capabilities"`
	}
	if uerr := json.Unmarshal(body, &req); uerr != nil {
		t.Fatalf("unmarshal create-key body: %v\nbody=%s", uerr, body)
	}
	c := req.Capabilities.Devices.Create
	if !c.Ephemeral {
		t.Error("Ephemeral = false")
	}
	if !c.Preauthorized {
		t.Error("Preauthorized = false")
	}
	if len(c.Tags) != 1 || c.Tags[0] != "tag:oidc-proxy" {
		t.Errorf("Tags = %v", c.Tags)
	}
}

func TestOAuthMinter_RequiresClientCredentials(t *testing.T) {
	cases := []struct {
		name string
		m    *OAuthMinter
	}{
		{"missing_id", &OAuthMinter{ClientSecret: "x", Tags: []string{"tag:y"}}},
		{"missing_secret", &OAuthMinter{ClientID: "x", Tags: []string{"tag:y"}}},
		{"missing_tags", &OAuthMinter{ClientID: "x", ClientSecret: "y"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.m.Mint(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestOAuthMinter_PropagatesAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"x","token_type":"Bearer","expires_in":3600}`)
	})
	mux.HandleFunc("/api/v2/tailnet/-/keys", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	m := &OAuthMinter{
		ClientID:     "id",
		ClientSecret: "secret",
		Tags:         []string{"tag:oidc-proxy"},
		BaseURL:      srv.URL,
	}
	_, err := m.Mint(context.Background())
	if err == nil {
		t.Fatal("expected propagated error")
	}
}
