package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/luks3r/lukky/core"
)

// fakeEventSub is a scripted EventSub WebSocket server plus Helix endpoint.
type fakeEventSub struct {
	t        *testing.T
	ws       *httptest.Server
	helix    *httptest.Server
	mu       sync.Mutex
	sessions int
	conns    chan *websocket.Conn
	requests []map[string]any
}

func newFakeEventSub(t *testing.T) *fakeEventSub {
	f := &fakeEventSub{t: t, conns: make(chan *websocket.Conn, 4)}
	f.ws = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.sessions++
		id := fmt.Sprintf("session-%d", f.sessions)
		f.mu.Unlock()
		f.send(conn, "session_welcome", id, map[string]any{
			"session": map[string]any{"id": id, "keepalive_timeout_seconds": 1},
		})
		f.conns <- conn
		<-r.Context().Done()
	}))
	f.helix = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Header.Get("Client-Id") != "client" || r.Header.Get("Authorization") != "Bearer access-old" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(f.ws.Close)
	t.Cleanup(f.helix.Close)
	return f
}

var messageCounter int

func (f *fakeEventSub) send(conn *websocket.Conn, kind, id string, payload any) {
	messageCounter++
	msg := map[string]any{
		"metadata": map[string]any{"message_id": fmt.Sprintf("%s-%d", id, messageCounter), "message_type": kind},
		"payload":  payload,
	}
	data, _ := json.Marshal(msg)
	_ = conn.Write(context.Background(), websocket.MessageText, data)
}

func (f *fakeEventSub) sendRaw(conn *websocket.Conn, msg map[string]any) {
	data, _ := json.Marshal(msg)
	_ = conn.Write(context.Background(), websocket.MessageText, data)
}

func (f *fakeEventSub) nextConn() *websocket.Conn {
	f.t.Helper()
	select {
	case c := <-f.conns:
		return c
	case <-time.After(3 * time.Second):
		f.t.Fatal("client did not connect")
		return nil
	}
}

func (f *fakeEventSub) subscribedTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		out = append(out, r["type"].(string))
	}
	return out
}

type capture struct {
	mu     sync.Mutex
	events []core.Event
}

func (c *capture) Publish(_ context.Context, e core.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return nil
}

func (c *capture) wait(t *testing.T, n int) []core.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := slices.Clone(c.events)
		c.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("got %d events, want %d", len(c.events), n)
	return nil
}

func startEventSub(t *testing.T, scopes []string) (*fakeEventSub, *capture) {
	t.Helper()
	fake := newFakeEventSub(t)
	twitchAuth := &fakeTwitch{access: "access-old", refresh: "r", scopes: scopes}
	auth := newTestAuth(t, twitchAuth.server(t), Credentials{ClientID: "id", ClientSecret: "secret", AccessToken: "access-old"}, "")
	events := &capture{}
	es, err := NewEventSub("client", auth, events, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	es.wsURL = "ws" + strings.TrimPrefix(fake.ws.URL, "http")
	es.helixURL = fake.helix.URL
	es.keepaliveSlack = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- es.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("Run did not stop")
		}
	})
	return fake, events
}

func TestEventSubSubscribesByScope(t *testing.T) {
	fake, _ := startEventSub(t, []string{"moderator:read:followers", "bits:read"})
	fake.nextConn()
	deadline := time.Now().Add(2 * time.Second)
	for len(fake.subscribedTypes()) < 4 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	want := []string{"channel.follow", "channel.cheer", "channel.raid", "stream.online", "stream.offline"}
	if got := fake.subscribedTypes(); !slices.Equal(got, want) {
		t.Fatalf("subscribed %v, want %v", got, want)
	}
	fake.mu.Lock()
	first := fake.requests[0]
	fake.mu.Unlock()
	condition := first["condition"].(map[string]any)
	transport := first["transport"].(map[string]any)
	if condition["broadcaster_user_id"] != "42" || condition["moderator_user_id"] != "42" || transport["session_id"] != "session-1" {
		t.Fatalf("request %v", first)
	}
}

func TestEventSubPublishesTypedEventsOnce(t *testing.T) {
	fake, events := startEventSub(t, nil)
	conn := fake.nextConn()
	notification := map[string]any{
		"metadata": map[string]any{"message_id": "dup-1", "message_type": "notification"},
		"payload": map[string]any{
			"subscription": map[string]any{"type": "channel.raid"},
			"event": map[string]any{
				"from_broadcaster_user_id": "7", "from_broadcaster_user_login": "friend",
				"from_broadcaster_user_name": "Friend", "to_broadcaster_user_login": "luks3r", "viewers": 12,
			},
		},
	}
	fake.sendRaw(conn, notification)
	fake.sendRaw(conn, notification) // duplicate delivery
	got := events.wait(t, 1)
	time.Sleep(50 * time.Millisecond)
	if len(events.events) != 1 {
		t.Fatalf("duplicate published: %d events", len(events.events))
	}
	want := RaidEvent{Channel: "luks3r", From: User{ID: "7", Login: "friend", Name: "Friend"}, Viewers: 12}
	if got[0].Topic != "twitch.raid" || got[0].Payload != want {
		t.Fatalf("got %+v", got[0])
	}
}

func TestEventSubReconnectKeepsSubscriptions(t *testing.T) {
	fake, events := startEventSub(t, nil)
	old := fake.nextConn()
	time.Sleep(100 * time.Millisecond)
	before := len(fake.subscribedTypes())
	fake.send(old, "session_reconnect", "r", map[string]any{
		"session": map[string]any{"id": "session-1", "reconnect_url": "ws" + strings.TrimPrefix(fake.ws.URL, "http")},
	})
	newConn := fake.nextConn()
	fake.send(newConn, "notification", "n", map[string]any{
		"subscription": map[string]any{"type": "stream.offline"},
		"event":        map[string]any{"broadcaster_user_login": "luks3r"},
	})
	got := events.wait(t, 1)
	if got[0].Payload != (StreamOfflineEvent{Channel: "luks3r"}) {
		t.Fatalf("got %+v", got[0])
	}
	if after := len(fake.subscribedTypes()); after != before {
		t.Fatalf("resubscribed after reconnect: %d then %d requests", before, after)
	}
}

func TestEventSubStartsNewSessionAfterSilence(t *testing.T) {
	fake, _ := startEventSub(t, nil)
	fake.nextConn() // never sends a keepalive
	fake.nextConn() // client gave up after keepalive timeout and started over
	deadline := time.Now().Add(2 * time.Second)
	for len(fake.subscribedTypes()) < 6 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := len(fake.subscribedTypes()); n != 6 { // raid, online, offline twice
		t.Fatalf("subscription requests %d, want 6", n)
	}
}

func TestEventDecoders(t *testing.T) {
	cases := []struct {
		kind  string
		event string
		want  any
	}{
		{
			"channel.follow", `{"user_id":"1","user_login":"ann","user_name":"Ann","broadcaster_user_login":"luks3r","followed_at":"2026-11-05T10:00:00Z"}`,
			FollowEvent{Channel: "luks3r", User: User{"1", "ann", "Ann"}, FollowedAt: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)},
		},
		{
			"channel.subscribe", `{"user_id":"1","user_login":"ann","user_name":"Ann","broadcaster_user_login":"luks3r","tier":"1000","is_gift":false}`,
			SubscribeEvent{Channel: "luks3r", User: User{"1", "ann", "Ann"}, Tier: "1000"},
		},
		{
			"channel.subscription.gift", `{"user_id":null,"user_login":null,"user_name":null,"broadcaster_user_login":"luks3r","total":5,"tier":"1000","cumulative_total":null,"is_anonymous":true}`,
			GiftEvent{Channel: "luks3r", Anonymous: true, Total: 5, Tier: "1000"},
		},
		{
			"channel.subscription.message", `{"user_id":"1","user_login":"ann","user_name":"Ann","broadcaster_user_login":"luks3r","tier":"2000","message":{"text":"hi"},"cumulative_months":10,"streak_months":null,"duration_months":1}`,
			ResubEvent{Channel: "luks3r", User: User{"1", "ann", "Ann"}, Tier: "2000", Message: "hi", CumulativeMonths: 10, DurationMonths: 1},
		},
		{
			"channel.cheer", `{"is_anonymous":false,"user_id":"1","user_login":"ann","user_name":"Ann","broadcaster_user_login":"luks3r","message":"cheer100","bits":100}`,
			CheerEvent{Channel: "luks3r", User: User{"1", "ann", "Ann"}, Bits: 100, Message: "cheer100"},
		},
		{
			"channel.channel_points_custom_reward_redemption.add", `{"user_id":"1","user_login":"ann","user_name":"Ann","broadcaster_user_login":"luks3r","user_input":"song","status":"unfulfilled","reward":{"id":"r1","title":"Song","cost":500}}`,
			RedemptionEvent{Channel: "luks3r", User: User{"1", "ann", "Ann"}, RewardID: "r1", RewardTitle: "Song", Cost: 500, Input: "song", Status: "unfulfilled"},
		},
		{
			"stream.online", `{"broadcaster_user_login":"luks3r","type":"live","started_at":"2026-11-05T10:00:00Z"}`,
			StreamOnlineEvent{Channel: "luks3r", Type: "live", StartedAt: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)},
		},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			i := slices.IndexFunc(subscriptions, func(s subscription) bool { return s.kind == c.kind })
			events := &capture{}
			if err := subscriptions[i].publish(context.Background(), events, json.RawMessage(c.event)); err != nil {
				t.Fatal(err)
			}
			if got := events.events[0].Payload; got != c.want {
				t.Fatalf("got  %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestRecentIDs(t *testing.T) {
	r := newRecentIDs(2)
	if r.contains("a") || !r.contains("a") || r.contains("b") || r.contains("c") || r.contains("a") {
		t.Fatal("unexpected membership")
	}
}
