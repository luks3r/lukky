package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/luks3r/lukky/core"
)

const (
	defaultEventSubURL = "wss://eventsub.wss.twitch.tv/ws"
	defaultHelixURL    = "https://api.twitch.tv/helix"
	eventQueueSize     = 256
	rememberedIDs      = 256 // message IDs kept to drop duplicate deliveries
)

// EventSub receives channel events from Twitch EventSub over a WebSocket and
// publishes them on the topics in events.go. It needs the broadcaster's token.
type EventSub struct {
	clientID string
	auth     *Auth
	events   core.Publisher
	log      *slog.Logger
	http     *http.Client
	queue    chan notification

	// Overridable in tests.
	wsURL          string
	helixURL       string
	keepaliveSlack time.Duration
	welcomeTimeout time.Duration
}

type notification struct {
	kind string
	raw  json.RawMessage
}

func NewEventSub(clientID string, auth *Auth, events core.Publisher, log *slog.Logger) (*EventSub, error) {
	if clientID == "" || auth == nil || events == nil {
		return nil, errors.New("twitch: client ID, broadcaster auth, and event bus are required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &EventSub{
		clientID:       clientID,
		auth:           auth,
		events:         events,
		log:            log.With("adapter", "twitch-eventsub"),
		http:           &http.Client{Timeout: 10 * time.Second},
		queue:          make(chan notification, eventQueueSize),
		wsURL:          defaultEventSubURL,
		helixURL:       defaultHelixURL,
		keepaliveSlack: 5 * time.Second,
		welcomeTimeout: 10 * time.Second,
	}, nil
}

// Run receives events until ctx is cancelled. A lost connection starts a new
// session with fresh subscriptions; Twitch does not replay missed events.
func (e *EventSub) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() { e.publish(ctx) })

	backoff := time.Second
	for {
		started, err := e.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if started {
			backoff = time.Second
		}
		e.log.Warn("session ended; reconnecting", "error", err, "in", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

type wsMessage struct {
	Metadata struct {
		MessageID   string `json:"message_id"`
		MessageType string `json:"message_type"`
	} `json:"metadata"`
	Payload struct {
		Session struct {
			ID                      string `json:"id"`
			KeepaliveTimeoutSeconds int    `json:"keepalive_timeout_seconds"`
			ReconnectURL            string `json:"reconnect_url"`
		} `json:"session"`
		Subscription struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"subscription"`
		Event json.RawMessage `json:"event"`
	} `json:"payload"`
}

type session struct {
	id        string
	keepalive time.Duration
}

// session runs one EventSub session. started reports whether a welcome arrived.
func (e *EventSub) session(ctx context.Context) (started bool, err error) {
	conn, s, err := e.dial(ctx, e.wsURL)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.CloseNow() }()
	e.log.Info("connected")
	if err := e.subscribeAll(ctx, s.id); err != nil {
		return true, err
	}
	seen := newRecentIDs(rememberedIDs)
	for {
		msg, err := e.read(ctx, conn, s.keepalive+e.keepaliveSlack)
		if err != nil {
			return true, err
		}
		if seen.contains(msg.Metadata.MessageID) {
			continue
		}
		switch msg.Metadata.MessageType {
		case "notification":
			e.enqueue(notification{kind: msg.Payload.Subscription.Type, raw: msg.Payload.Event})
		case "session_reconnect":
			// The new connection keeps the subscriptions. Close the old one
			// only after the new one is welcomed.
			newConn, newSession, err := e.dial(ctx, msg.Payload.Session.ReconnectURL)
			if err != nil {
				return true, fmt.Errorf("reconnect: %w", err)
			}
			// Close in the background: the handshake can take seconds, and
			// the new connection must be read meanwhile.
			go func(old *websocket.Conn) { _ = old.Close(websocket.StatusNormalClosure, "") }(conn)
			conn, s = newConn, newSession
			e.log.Info("moved to new session")
		case "revocation":
			e.log.Warn("subscription revoked", "type", msg.Payload.Subscription.Type, "status", msg.Payload.Subscription.Status)
		case "session_keepalive":
		}
	}
}

// dial connects to url and waits for the welcome message.
func (e *EventSub) dial(ctx context.Context, url string) (*websocket.Conn, session, error) {
	conn, _, err := websocket.Dial(ctx, url, nil) //nolint:bodyclose // websocket.Dial closes the response body
	if err != nil {
		return nil, session{}, err
	}
	conn.SetReadLimit(1 << 20)
	msg, err := e.read(ctx, conn, e.welcomeTimeout)
	if err != nil {
		_ = conn.CloseNow()
		return nil, session{}, fmt.Errorf("wait for welcome: %w", err)
	}
	if msg.Metadata.MessageType != "session_welcome" || msg.Payload.Session.ID == "" {
		_ = conn.CloseNow()
		return nil, session{}, fmt.Errorf("expected session_welcome, got %q", msg.Metadata.MessageType)
	}
	keepalive := time.Duration(msg.Payload.Session.KeepaliveTimeoutSeconds) * time.Second
	if keepalive <= 0 {
		keepalive = 10 * time.Second
	}
	return conn, session{id: msg.Payload.Session.ID, keepalive: keepalive}, nil
}

// read waits up to timeout for the next message. Twitch sends at least a
// keepalive within the keepalive timeout, so longer silence means the
// connection is dead.
func (e *EventSub) read(ctx context.Context, conn *websocket.Conn, timeout time.Duration) (wsMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		return wsMessage{}, err
	}
	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return wsMessage{}, fmt.Errorf("decode message: %w", err)
	}
	return msg, nil
}

// subscribeAll requests every event type the token has scopes for. Types it
// cannot request are logged and skipped, so the others still work.
func (e *EventSub) subscribeAll(ctx context.Context, sessionID string) error {
	if _, err := e.auth.Token(ctx); err != nil { // validates, so scopes are known
		return err
	}
	broadcasterID := e.auth.UserID()
	var missing []string
	for _, sub := range subscriptions {
		if sub.scope != "" && !e.auth.HasScope(sub.scope) {
			missing = append(missing, sub.kind+" ("+sub.scope+")")
			continue
		}
		if err := e.subscribe(ctx, sub, broadcasterID, sessionID); err != nil {
			e.log.Error("subscribe failed", "type", sub.kind, "error", err)
		}
	}
	if len(missing) > 0 {
		e.log.Warn("event types skipped: broadcaster token lacks scopes", "types", strings.Join(missing, ", "))
	}
	return nil
}

func (e *EventSub) subscribe(ctx context.Context, sub subscription, broadcasterID, sessionID string) error {
	body, err := json.Marshal(map[string]any{
		"type":      sub.kind,
		"version":   sub.version,
		"condition": sub.condition(broadcasterID),
		"transport": map[string]string{"method": "websocket", "session_id": sessionID},
	})
	if err != nil {
		return err
	}
	for attempt := range 2 {
		status, message, err := e.post(ctx, "/eventsub/subscriptions", body)
		if err != nil {
			return err
		}
		switch {
		case status >= 200 && status < 300, status == http.StatusConflict: // 409: already subscribed
			return nil
		case status == http.StatusUnauthorized && attempt == 0:
			e.auth.Invalidate()
			continue
		default:
			return fmt.Errorf("status %d: %s", status, message)
		}
	}
	return errors.New("token rejected twice")
}

func (e *EventSub) post(ctx context.Context, path string, body []byte) (status int, message string, err error) {
	token, err := e.auth.Token(ctx)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.helixURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", e.clientID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	var failure struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&failure)
	return resp.StatusCode, failure.Message, nil
}

// enqueue never blocks the reader, so keepalive deadlines hold under load.
func (e *EventSub) enqueue(n notification) {
	select {
	case e.queue <- n:
	default:
		e.log.Warn("event queue full; event dropped", "type", n.kind)
	}
}

func (e *EventSub) publish(ctx context.Context) {
	byKind := map[string]subscription{}
	for _, sub := range subscriptions {
		byKind[sub.kind] = sub
	}
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-e.queue:
			sub, ok := byKind[n.kind]
			if !ok {
				e.log.Warn("unknown event type", "type", n.kind)
				continue
			}
			if err := sub.publish(ctx, e.events, n.raw); err != nil {
				e.log.Error("handle event", "type", n.kind, "error", err)
			}
		}
	}
}

// recentIDs remembers the last n message IDs. Twitch may deliver a message twice.
type recentIDs struct {
	ids  map[string]struct{}
	ring []string
	next int
}

func newRecentIDs(n int) *recentIDs {
	return &recentIDs{ids: make(map[string]struct{}, n), ring: make([]string, n)}
}

// contains reports whether id was seen, and remembers it if not.
func (r *recentIDs) contains(id string) bool {
	if id == "" {
		return false
	}
	if _, ok := r.ids[id]; ok {
		return true
	}
	delete(r.ids, r.ring[r.next])
	r.ring[r.next] = id
	r.ids[id] = struct{}{}
	r.next = (r.next + 1) % len(r.ring)
	return false
}
