// Package storagetest checks that a core.Storage implementation follows the contract.
package storagetest

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/luks3r/lukky/core"
)

// Run tests a fresh storage created by open.
func Run(t *testing.T, open func(t *testing.T) core.Storage) {
	ctx := context.Background()

	t.Run("missing key", func(t *testing.T) {
		s := open(t)
		if _, err := s.Get(ctx, "ns", "nope"); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("Get: %v", err)
		}
		if err := s.Delete(ctx, "ns", "nope"); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("Delete: %v", err)
		}
		if got, err := s.List(ctx, "ns"); err != nil || len(got) != 0 {
			t.Fatalf("List: %v %v", got, err)
		}
	})

	t.Run("put get delete", func(t *testing.T) {
		s := open(t)
		if err := s.Put(ctx, "ns", "key", []byte("one")); err != nil {
			t.Fatal(err)
		}
		if err := s.Put(ctx, "ns", "key", []byte("two")); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Get(ctx, "ns", "key"); err != nil || string(got) != "two" {
			t.Fatalf("Get: %q %v", got, err)
		}
		if err := s.Delete(ctx, "ns", "key"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, "ns", "key"); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("after delete: %v", err)
		}
	})

	t.Run("empty value exists", func(t *testing.T) {
		s := open(t)
		if err := s.Put(ctx, "ns", "empty", nil); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Get(ctx, "ns", "empty"); err != nil || len(got) != 0 {
			t.Fatalf("Get: %q %v", got, err)
		}
	})

	t.Run("namespaces are isolated", func(t *testing.T) {
		s := open(t)
		_ = s.Put(ctx, "a", "key", []byte("from a"))
		_ = s.Put(ctx, "b", "key", []byte("from b"))
		if got, _ := s.Get(ctx, "a", "key"); string(got) != "from a" {
			t.Fatalf("a: %q", got)
		}
		list, err := s.List(ctx, "b")
		if err != nil || !maps.EqualFunc(list, map[string][]byte{"key": []byte("from b")}, func(x, y []byte) bool { return string(x) == string(y) }) {
			t.Fatalf("List b: %q %v", list, err)
		}
	})

	t.Run("returned values are copies", func(t *testing.T) {
		s := open(t)
		value := []byte("abc")
		_ = s.Put(ctx, "ns", "key", value)
		value[0] = 'X'
		got, _ := s.Get(ctx, "ns", "key")
		got[1] = 'Y'
		if again, _ := s.Get(ctx, "ns", "key"); string(again) != "abc" {
			t.Fatalf("stored value changed: %q", again)
		}
	})
}
