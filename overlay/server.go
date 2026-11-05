// Package overlay serves module overlay pages, for example as OBS browser
// sources, and streams live data to them with server-sent events.
//
// Routes:
//
//	GET /                          list of mounted overlays
//	GET /lukky.js                  client helper for overlay pages
//	GET /overlays/{module}/        the module's files (index.html by default)
//	GET /overlays/{module}/events  event stream: stored values first, then live events
package overlay

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/luks3r/lukky/core"
)

//go:embed lukky.js
var clientScript []byte

const (
	clientBuffer = 64               // events queued per page before it is dropped
	keepalive    = 25 * time.Second // comment line that keeps idle streams open
)

// mount wraps a file system so unmount can tell its own mount from a later one.
type mount struct{ files fs.FS }

type message struct {
	event string
	data  []byte
}

// Server implements core.Overlays.
type Server struct {
	log     *slog.Logger
	mu      sync.Mutex
	mounts  map[string]*mount
	state   map[string]map[string][]byte // module, key: JSON value
	order   map[string][]string          // module: keys in first-set order
	clients map[string]map[chan message]struct{}
}

func NewServer(log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		log:     log.With("service", "overlay"),
		mounts:  map[string]*mount{},
		state:   map[string]map[string][]byte{},
		order:   map[string][]string{},
		clients: map[string]map[chan message]struct{}{},
	}
}

func (s *Server) Mount(module string, files fs.FS) (func(), error) {
	if module == "" || files == nil {
		return nil, errors.New("module and files are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.mounts[module]; exists {
		return nil, fmt.Errorf("overlay %q is already mounted", module)
	}
	m := &mount{files}
	s.mounts[module] = m
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.mounts[module] == m {
			delete(s.mounts, module)
		}
	}, nil
}

func (s *Server) Push(_ context.Context, module, event string, data any) error {
	msg, err := encode(event, data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcast(module, msg)
	return nil
}

func (s *Server) Set(_ context.Context, module, key string, data any) error {
	msg, err := encode(key, data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state[module] == nil {
		s.state[module] = map[string][]byte{}
	}
	if _, exists := s.state[module][key]; !exists {
		s.order[module] = append(s.order[module], key)
	}
	s.state[module][key] = msg.data
	s.broadcast(module, msg)
	return nil
}

func encode(event string, data any) (message, error) {
	if event == "" {
		return message{}, errors.New("event name is required")
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return message{}, fmt.Errorf("encode overlay data: %w", err)
	}
	return message{event: event, data: encoded}, nil
}

// broadcast queues msg for every page of module. A page that falls behind is
// disconnected; its browser reconnects and receives the stored values again.
// The caller holds s.mu.
func (s *Server) broadcast(module string, msg message) {
	for client := range s.clients[module] {
		select {
		case client <- msg:
		default:
			delete(s.clients[module], client)
			close(client)
			s.log.Warn("overlay page too slow; disconnected", "module", module)
		}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /lukky.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(clientScript)
	})
	mux.HandleFunc("GET /overlays/{module}/events", s.events)
	mux.HandleFunc("GET /overlays/{module}/{path...}", s.files)
	return mux
}

var indexPage = template.Must(template.New("index").Parse(`<!doctype html>
<meta charset="utf-8"><title>Lukky overlays</title>
<h1>Lukky overlays</h1>
<ul>{{range .}}<li><a href="/overlays/{{.}}/">{{.}}</a></li>{{else}}<li>No overlays mounted.</li>{{end}}</ul>`))

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	modules := slices.Sorted(maps.Keys(s.mounts))
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = indexPage.Execute(w, modules)
}

func (s *Server) files(w http.ResponseWriter, r *http.Request) {
	module := r.PathValue("module")
	s.mu.Lock()
	m := s.mounts[module]
	s.mu.Unlock()
	if m == nil {
		http.NotFound(w, r)
		return
	}
	http.StripPrefix("/overlays/"+module, http.FileServerFS(m.files)).ServeHTTP(w, r)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	module := r.PathValue("module")
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	client := make(chan message, clientBuffer)
	s.mu.Lock()
	var backlog []message
	for _, key := range s.order[module] {
		backlog = append(backlog, message{event: key, data: s.state[module][key]})
	}
	if s.clients[module] == nil {
		s.clients[module] = map[chan message]struct{}{}
	}
	s.clients[module][client] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.clients[module][client]; ok {
			delete(s.clients[module], client)
			close(client)
		}
	}()

	write := func(msg message) bool {
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.event, msg.data)
		return err == nil && rc.Flush() == nil
	}
	_, _ = fmt.Fprint(w, ": connected\n\n")
	for _, msg := range backlog {
		if !write(msg) {
			return
		}
	}
	if rc.Flush() != nil {
		return
	}
	ticker := time.NewTicker(keepalive)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-client:
			if !ok || !write(msg) {
				return
			}
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

// ListenAndServe serves overlays on addr until ctx is cancelled.
// Open streams end when ctx is cancelled, so shutdown does not wait for them.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	stop := context.AfterFunc(ctx, func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	})
	defer stop()
	s.log.Info("serving overlays", "url", "http://"+listener.Addr().String()+"/")
	if err := srv.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

var _ core.Overlays = (*Server)(nil)
