package twitch

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeTwitch accepts one access token at a time and rotates both tokens on refresh.
type fakeTwitch struct {
	mu       sync.Mutex
	access   string
	refresh  string
	scopes   []string
	refreshs int
}

func (f *fakeTwitch) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /validate", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "OAuth "+f.access {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"login": "lukserbot", "scopes": f.scopes, "expires_in": 14000})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != f.refresh ||
			r.FormValue("client_id") != "id" || r.FormValue("client_secret") != "secret" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"Invalid refresh token"}`))
			return
		}
		f.refreshs++
		f.access, f.refresh = "access-new", "refresh-new"
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": f.access, "refresh_token": f.refresh})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestAuth(t *testing.T, srv *httptest.Server, creds Credentials, statePath string) *Auth {
	t.Helper()
	a, err := NewAuth(creds, statePath, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	a.validateURL, a.tokenURL = srv.URL+"/validate", srv.URL+"/token"
	return a
}

var chatScopes = []string{"chat:read", "chat:edit"}

func TestAuthValidToken(t *testing.T) {
	fake := &fakeTwitch{access: "access-old", refresh: "refresh-old", scopes: chatScopes}
	a := newTestAuth(t, fake.server(t), Credentials{ClientID: "id", ClientSecret: "secret", AccessToken: "access-old", RefreshToken: "refresh-old"}, "")
	login, err := a.Login(context.Background())
	if err != nil || login != "lukserbot" {
		t.Fatalf("login %q, err %v", login, err)
	}
	if tok, _ := a.Token(context.Background()); tok != "access-old" || fake.refreshs != 0 {
		t.Fatalf("token %q, refreshes %d", tok, fake.refreshs)
	}
}

func TestAuthRefreshesAndSavesRotatedTokens(t *testing.T) {
	fake := &fakeTwitch{access: "access-current", refresh: "refresh-old", scopes: chatScopes}
	srv := fake.server(t)
	state := filepath.Join(t.TempDir(), "state", "twitch-token.json")
	a := newTestAuth(t, srv, Credentials{ClientID: "id", ClientSecret: "secret", AccessToken: "expired", RefreshToken: "refresh-old"}, state)
	if _, err := a.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tok, _ := a.Token(context.Background()); tok != "access-new" {
		t.Fatalf("token %q", tok)
	}
	info, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode %v", info.Mode().Perm())
	}
	data, _ := os.ReadFile(state)
	if !strings.Contains(string(data), "refresh-new") {
		t.Fatalf("state file missing rotated refresh token: %s", data)
	}

	// A restart prefers the saved tokens over the stale ones from the environment.
	restarted := newTestAuth(t, srv, Credentials{ClientID: "id", ClientSecret: "secret", AccessToken: "expired", RefreshToken: "refresh-old"}, state)
	if _, err := restarted.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.refreshs != 1 {
		t.Fatalf("refreshes %d, want 1", fake.refreshs)
	}
}

func TestAuthInvalidateForcesRecheck(t *testing.T) {
	fake := &fakeTwitch{access: "access-old", refresh: "refresh-old", scopes: chatScopes}
	a := newTestAuth(t, fake.server(t), Credentials{ClientID: "id", ClientSecret: "secret", AccessToken: "access-old", RefreshToken: "refresh-old"}, "")
	if _, err := a.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.access = "revoked-elsewhere" // Twitch no longer accepts access-old
	fake.mu.Unlock()
	a.Invalidate()
	if tok, err := a.Token(context.Background()); err != nil || tok != "access-new" {
		t.Fatalf("token %q, err %v", tok, err)
	}
}

func TestAuthRejectsMissingScope(t *testing.T) {
	fake := &fakeTwitch{access: "a", refresh: "r", scopes: []string{"chat:read"}}
	a := newTestAuth(t, fake.server(t), Credentials{ClientID: "id", ClientSecret: "secret", AccessToken: "a", RefreshToken: "r"}, "")
	if _, err := a.Login(context.Background()); err == nil || !strings.Contains(err.Error(), "chat:edit") {
		t.Fatalf("expected missing scope error, got %v", err)
	}
}

func TestAuthReportsRejectedRefresh(t *testing.T) {
	fake := &fakeTwitch{access: "a", refresh: "r", scopes: chatScopes}
	a := newTestAuth(t, fake.server(t), Credentials{ClientID: "id", ClientSecret: "secret", AccessToken: "expired", RefreshToken: "wrong"}, "")
	_, err := a.Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), "re-authorize") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("got %v", err)
	}
}

func TestNewAuthRequiresClientAndToken(t *testing.T) {
	for _, creds := range []Credentials{
		{ClientSecret: "s", AccessToken: "a"},
		{ClientID: "i", AccessToken: "a"},
		{ClientID: "i", ClientSecret: "s"},
	} {
		if _, err := NewAuth(creds, "", nil); err == nil {
			t.Errorf("NewAuth(%+v) succeeded", creds)
		}
	}
}
