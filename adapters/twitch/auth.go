package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	defaultValidateURL = "https://id.twitch.tv/oauth2/validate"
	defaultTokenURL    = "https://id.twitch.tv/oauth2/token" //nolint:gosec // G101: an endpoint URL, not a credential

	// Twitch requires apps to validate tokens at startup and hourly.
	validateInterval = time.Hour
	// Refresh this long before the token expires.
	refreshMargin = 10 * time.Minute
)

// ChatScopes are required for the bot account that reads and sends chat.
var ChatScopes = []string{"chat:read", "chat:edit"}

var errInvalidToken = errors.New("twitch: access token is invalid")

// TokenSource supplies the access token used to log in to chat.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
	// Invalidate is called when Twitch rejects the token; the next Token call must recheck it.
	Invalidate()
}

type Credentials struct {
	ClientID     string
	ClientSecret string
	AccessToken  string
	RefreshToken string
	// RequiredScopes must all be granted, or validation fails.
	RequiredScopes []string
}

// Auth keeps a user access token valid. It validates the token at startup and
// hourly, and refreshes it when it is invalid or about to expire. Rotated tokens
// are saved to the state file, which takes precedence over Credentials on the
// next start.
type Auth struct {
	mu          sync.Mutex
	creds       Credentials
	statePath   string
	login       string
	userID      string
	scopes      []string
	expiresAt   time.Time // zero when unknown; far future when the token never expires
	http        *http.Client
	log         *slog.Logger
	validateURL string
	tokenURL    string
}

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// NewAuth loads saved tokens from statePath when the file exists. An empty
// statePath disables persistence.
func NewAuth(creds Credentials, statePath string, log *slog.Logger) (*Auth, error) {
	if creds.ClientID == "" || creds.ClientSecret == "" {
		return nil, errors.New("twitch: client ID and client secret are required")
	}
	if log == nil {
		log = slog.Default()
	}
	a := &Auth{
		creds:       creds,
		statePath:   statePath,
		http:        &http.Client{Timeout: 10 * time.Second},
		log:         log.With("adapter", Platform),
		validateURL: defaultValidateURL,
		tokenURL:    defaultTokenURL,
	}
	if statePath != "" {
		data, err := os.ReadFile(statePath) //nolint:gosec // G304: the path comes from application code, not user input
		switch {
		case err == nil:
			var saved tokenPair
			if err := json.Unmarshal(data, &saved); err != nil {
				return nil, fmt.Errorf("twitch: read %s: %w", statePath, err)
			}
			if saved.RefreshToken != "" {
				a.creds.AccessToken, a.creds.RefreshToken = saved.AccessToken, saved.RefreshToken
			}
		case !errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("twitch: read %s: %w", statePath, err)
		}
	}
	if a.creds.AccessToken == "" && a.creds.RefreshToken == "" {
		return nil, errors.New("twitch: an access token or refresh token is required")
	}
	return a, nil
}

// Login validates the token, refreshing it if needed, and returns the account login.
func (a *Auth) Login(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.check(ctx); err != nil {
		return "", err
	}
	return a.login, nil
}

// UserID returns the account's user ID. It is set after a successful Login.
func (a *Auth) UserID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.userID
}

// HasScope reports whether the token grants scope. It is set after a successful Login.
func (a *Auth) HasScope(scope string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Contains(a.scopes, scope)
}

func (a *Auth) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Until(a.expiresAt) <= refreshMargin {
		if err := a.check(ctx); err != nil {
			return "", err
		}
	}
	return a.creds.AccessToken, nil
}

func (a *Auth) Invalidate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expiresAt = time.Time{}
}

// Run revalidates the token every hour until ctx is cancelled.
func (a *Auth) Run(ctx context.Context) {
	ticker := time.NewTicker(validateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			err := a.check(ctx)
			a.mu.Unlock()
			if err != nil && ctx.Err() == nil {
				a.log.Error("token check failed", "error", err)
			}
		}
	}
}

type validation struct {
	Login     string   `json:"login"`
	UserID    string   `json:"user_id"`
	Scopes    []string `json:"scopes"`
	ExpiresIn int      `json:"expires_in"`
}

// check validates the token and refreshes it once if it is invalid or close to expiry.
// The caller holds a.mu.
func (a *Auth) check(ctx context.Context) error {
	v, err := a.validate(ctx)
	if err == nil && (v.ExpiresIn == 0 || time.Duration(v.ExpiresIn)*time.Second > refreshMargin) {
		return a.apply(v)
	}
	if err != nil && !errors.Is(err, errInvalidToken) {
		return err
	}
	if err := a.refresh(ctx); err != nil {
		return err
	}
	if v, err = a.validate(ctx); err != nil {
		return err
	}
	return a.apply(v)
}

func (a *Auth) apply(v validation) error {
	for _, scope := range a.creds.RequiredScopes {
		if !slices.Contains(v.Scopes, scope) {
			return fmt.Errorf("twitch: token for %q lacks the %q scope; re-authorize it with %s",
				v.Login, scope, strings.Join(a.creds.RequiredScopes, " "))
		}
	}
	a.login, a.userID, a.scopes = v.Login, v.UserID, v.Scopes
	if v.ExpiresIn == 0 {
		a.expiresAt = time.Now().AddDate(100, 0, 0)
	} else {
		a.expiresAt = time.Now().Add(time.Duration(v.ExpiresIn) * time.Second)
	}
	return nil
}

func (a *Auth) validate(ctx context.Context) (validation, error) {
	var v validation
	if a.creds.AccessToken == "" {
		return v, errInvalidToken
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.validateURL, nil)
	if err != nil {
		return v, err
	}
	req.Header.Set("Authorization", "OAuth "+a.creds.AccessToken)
	resp, err := a.http.Do(req)
	if err != nil {
		return v, fmt.Errorf("twitch: validate token: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v); err != nil {
			return v, fmt.Errorf("twitch: validate token: %w", err)
		}
		return v, nil
	case http.StatusUnauthorized:
		return v, errInvalidToken
	default:
		return v, fmt.Errorf("twitch: validate token: unexpected status %s", resp.Status)
	}
}

func (a *Auth) refresh(ctx context.Context) error {
	if a.creds.RefreshToken == "" {
		return errors.New("twitch: access token is invalid and no refresh token is set; re-authorize the bot")
	}
	form := url.Values{
		"client_id":     {a.creds.ClientID},
		"client_secret": {a.creds.ClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {a.creds.RefreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("twitch: refresh token: %w", err)
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, 1<<20)
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(body).Decode(&failure)
		return fmt.Errorf("twitch: refresh token rejected (%s: %s); re-authorize the bot", resp.Status, failure.Message)
	}
	var tokens tokenPair
	if err := json.NewDecoder(body).Decode(&tokens); err != nil {
		return fmt.Errorf("twitch: refresh token: %w", err)
	}
	if tokens.AccessToken == "" {
		return errors.New("twitch: refresh response has no access token")
	}
	a.creds.AccessToken = tokens.AccessToken
	if tokens.RefreshToken != "" {
		a.creds.RefreshToken = tokens.RefreshToken
	}
	a.log.Info("access token refreshed")
	// The new tokens already work; a failed save only matters after a restart.
	if err := a.save(); err != nil {
		a.log.Error("save refreshed tokens", "path", a.statePath, "error", err)
	}
	return nil
}

// save writes the tokens atomically with owner-only permissions.
func (a *Auth) save() error {
	if a.statePath == "" {
		return nil
	}
	dir := filepath.Dir(a.statePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	//nolint:gosec // G117: persisting the tokens is the purpose; the file is written with mode 0600
	data, err := json.Marshal(tokenPair{AccessToken: a.creds.AccessToken, RefreshToken: a.creds.RefreshToken})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".twitch-token-*") // created with mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), a.statePath)
}
