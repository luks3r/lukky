package core

import (
	"bytes"
	"context"
	"errors"
	"sync"
)

var ErrNotFound = errors.New("key not found")

// Storage is the key-value backend shared by all modules. Each module works in
// its own namespace, so modules cannot see each other's keys.
type Storage interface {
	Get(ctx context.Context, namespace, key string) ([]byte, error)
	Put(ctx context.Context, namespace, key string, value []byte) error
	// Delete returns ErrNotFound when the key does not exist.
	Delete(ctx context.Context, namespace, key string) error
	List(ctx context.Context, namespace string) (map[string][]byte, error)
}

// Store is one module's view of Storage. The namespace is the module name,
// so data survives unload and reload of the same module.
type Store struct {
	backend   Storage
	namespace string
}

func (s Store) Get(ctx context.Context, key string) ([]byte, error) {
	return s.backend.Get(ctx, s.namespace, key)
}

func (s Store) Put(ctx context.Context, key string, value []byte) error {
	return s.backend.Put(ctx, s.namespace, key, value)
}

func (s Store) Delete(ctx context.Context, key string) error {
	return s.backend.Delete(ctx, s.namespace, key)
}

func (s Store) List(ctx context.Context) (map[string][]byte, error) {
	return s.backend.List(ctx, s.namespace)
}

// MemoryStorage keeps data until the process exits.
type MemoryStorage struct {
	mu   sync.RWMutex
	data map[string]map[string][]byte
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{data: make(map[string]map[string][]byte)}
}

func (m *MemoryStorage) Get(_ context.Context, namespace, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.data[namespace][key]
	if !ok {
		return nil, ErrNotFound
	}
	return bytes.Clone(value), nil
}

func (m *MemoryStorage) Put(_ context.Context, namespace, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[namespace] == nil {
		m.data[namespace] = make(map[string][]byte)
	}
	m.data[namespace][key] = bytes.Clone(value)
	return nil
}

func (m *MemoryStorage) Delete(_ context.Context, namespace, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[namespace][key]; !ok {
		return ErrNotFound
	}
	delete(m.data[namespace], key)
	return nil
}

func (m *MemoryStorage) List(_ context.Context, namespace string) (map[string][]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string][]byte, len(m.data[namespace]))
	for key, value := range m.data[namespace] {
		out[key] = bytes.Clone(value)
	}
	return out, nil
}
