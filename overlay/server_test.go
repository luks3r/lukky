package overlay

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s := NewServer(slog.New(slog.DiscardHandler))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// stream opens the event stream and returns a function reading the next event.
func stream(t *testing.T, ctx context.Context, url string) func() (string, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	lines := bufio.NewScanner(resp.Body)
	return func() (event, data string) {
		for lines.Scan() {
			line := lines.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && event != "":
				return event, data
			}
		}
		return "", ""
	}
}

func TestServesMountedFiles(t *testing.T) {
	s, ts := newTestServer(t)
	unmount, err := s.Mount("alerts", fstest.MapFS{"index.html": {Data: []byte("<h1>alerts</h1>")}})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := get(t, ts.URL+"/overlays/alerts/"); code != 200 || !strings.Contains(body, "alerts") {
		t.Fatalf("index: %d %q", code, body)
	}
	if _, body := get(t, ts.URL+"/"); !strings.Contains(body, `/overlays/alerts/`) {
		t.Fatalf("listing: %q", body)
	}
	if code, body := get(t, ts.URL+"/lukky.js"); code != 200 || !strings.Contains(body, "EventSource") {
		t.Fatalf("client script: %d", code)
	}
	if _, err := s.Mount("alerts", fstest.MapFS{}); err == nil {
		t.Fatal("double mount accepted")
	}
	unmount()
	if code, _ := get(t, ts.URL+"/overlays/alerts/"); code != 404 {
		t.Fatalf("after unmount: %d", code)
	}
}

func TestStreamSendsStoredValuesThenLiveEvents(t *testing.T) {
	s, ts := newTestServer(t)
	ctx := context.Background()
	_ = s.Set(ctx, "goal", "count", 1)
	_ = s.Set(ctx, "goal", "title", "Sub goal")
	_ = s.Set(ctx, "goal", "count", 2) // replaces the stored value, keeps its position
	_ = s.Push(ctx, "goal", "alert", "missed: page not connected yet")

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	next := stream(t, streamCtx, ts.URL+"/overlays/goal/events")
	for _, want := range [][2]string{{"count", "2"}, {"title", `"Sub goal"`}} {
		if event, data := next(); event != want[0] || data != want[1] {
			t.Fatalf("got %s=%s, want %s=%s", event, data, want[0], want[1])
		}
	}
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		connected := len(s.clients["goal"]) == 1
		s.mu.Unlock()
		if connected || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = s.Push(ctx, "goal", "alert", map[string]any{"user": "ann"})
	_ = s.Push(ctx, "other", "alert", "not for this page")
	if event, data := next(); event != "alert" || data != `{"user":"ann"}` {
		t.Fatalf("live: %s=%s", event, data)
	}
}

func TestSlowPageIsDisconnected(t *testing.T) {
	s := NewServer(slog.New(slog.DiscardHandler))
	client := make(chan message, clientBuffer)
	s.clients["m"] = map[chan message]struct{}{client: {}}
	for range clientBuffer + 1 {
		_ = s.Push(context.Background(), "m", "e", 1)
	}
	if len(s.clients["m"]) != 0 {
		t.Fatal("slow client still registered")
	}
	for range client {
	}
}

func TestRejectsBadInput(t *testing.T) {
	s := NewServer(nil)
	if err := s.Push(context.Background(), "m", "", 1); err == nil {
		t.Fatal("empty event accepted")
	}
	if err := s.Set(context.Background(), "m", "k", func() {}); err == nil {
		t.Fatal("unencodable data accepted")
	}
	if _, err := s.Mount("", fstest.MapFS{}); err == nil {
		t.Fatal("empty module accepted")
	}
}

func TestListenAndServeStopsWithOpenStream(t *testing.T) {
	s := NewServer(slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	listener := "127.0.0.1:0"
	go func() { done <- s.ListenAndServe(ctx, listener) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop")
	}
}
