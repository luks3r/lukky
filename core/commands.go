package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
)

type (
	// Match carries arguments parsed by a Matcher.
	Match struct{ Args []string }
	// Matcher decides whether a message is a command. It must be fast and do no I/O.
	Matcher        func(Message) (Match, bool)
	CommandHandler func(context.Context, Message, Match) error
)

type route struct {
	id      uint64
	scope   *scope
	match   Matcher
	handler CommandHandler
}

// Router runs every matching command handler, in registration order.
type Router struct {
	mu     sync.RWMutex
	next   uint64
	routes []route
}

func NewRouter() *Router { return &Router{} }

func (r *Router) handle(s *scope, matcher Matcher, handler CommandHandler) error {
	if matcher == nil || handler == nil {
		return errors.New("matcher and handler are required")
	}
	r.mu.Lock()
	r.next++
	id := r.next
	r.routes = append(r.routes, route{id, s, matcher, handler})
	r.mu.Unlock()
	return s.own(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.routes = slices.DeleteFunc(r.routes, func(rt route) bool { return rt.id == id })
	})
}

// Dispatch runs every route that matches msg. A failing handler does not stop
// later ones; all errors are returned together.
func (r *Router) Dispatch(ctx context.Context, msg Message) error {
	r.mu.RLock()
	routes := slices.Clone(r.routes)
	r.mu.RUnlock()
	var errs []error
	for _, rt := range routes {
		callCtx, done, ok := rt.scope.acquire(ctx)
		if !ok {
			continue
		}
		err := func() error {
			defer done()
			return rt.scope.call("command", func() error {
				match, matched := rt.match(msg)
				if !matched {
					return nil
				}
				return rt.handler(callCtx, msg, match)
			})
		}()
		if err != nil {
			errs = append(errs, fmt.Errorf("route %d: %w", rt.id, err))
		}
	}
	return errors.Join(errs...)
}

// Prefix matches when the first word is exactly prefix. The other words become Args.
func Prefix(prefix string) Matcher {
	return func(msg Message) (Match, bool) {
		fields := strings.Fields(msg.Text)
		if len(fields) == 0 || fields[0] != prefix {
			return Match{}, false
		}
		return Match{Args: fields[1:]}, true
	}
}

// Exact matches when the trimmed text equals text.
func Exact(text string) Matcher {
	return func(msg Message) (Match, bool) {
		return Match{}, strings.TrimSpace(msg.Text) == text
	}
}

// Regex matches the trimmed text against re; capture groups become Args.
// Use ^ and $ in re to match the whole message.
func Regex(re *regexp.Regexp) Matcher {
	if re == nil {
		panic("core.Regex: nil regexp")
	}
	return func(msg Message) (Match, bool) {
		groups := re.FindStringSubmatch(strings.TrimSpace(msg.Text))
		if groups == nil {
			return Match{}, false
		}
		return Match{Args: groups[1:]}, true
	}
}

// RequireRole wraps next so that senders ranked below minRole are ignored.
func RequireRole(minRole Role, next Matcher) Matcher {
	if next == nil {
		panic("core.RequireRole: nil matcher")
	}
	return func(msg Message) (Match, bool) {
		if !msg.User.Roles.AtLeast(minRole) {
			return Match{}, false
		}
		return next(msg)
	}
}
