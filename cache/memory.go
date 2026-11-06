package cache

import (
	"bytes"
	"context"
	"strconv"
	"sync"
	"time"
)

// Memory keeps values in process memory. They are lost on restart and not
// shared between instances. Expired values are removed periodically as new
// ones are written; nothing else limits its size.
type Memory struct {
	mu     sync.Mutex
	items  map[string]memoryItem
	writes int
}

type memoryItem struct {
	value   []byte
	expires time.Time
}

func (i memoryItem) expired(now time.Time) bool {
	return !i.expires.IsZero() && !now.Before(i.expires)
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{items: map[string]memoryItem{}} }

func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[key]
	if !ok || item.expired(time.Now()) {
		delete(m.items, key)
		return nil, false, nil
	}
	return bytes.Clone(item.value), true, nil
}

func (m *Memory) Put(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.set(key, memoryItem{bytes.Clone(value), expiry(ttl)})
	return nil
}

func (m *Memory) Add(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item, ok := m.items[key]; ok && !item.expired(time.Now()) {
		return false, nil
	}
	m.set(key, memoryItem{bytes.Clone(value), expiry(ttl)})
	return true, nil
}

func (m *Memory) Increment(_ context.Context, key string, delta int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[key]
	if !ok || item.expired(time.Now()) {
		m.set(key, memoryItem{value: strconv.AppendInt(nil, delta, 10)})
		return delta, nil
	}
	value, next, err := addInt(item.value, delta)
	if err != nil {
		return 0, err
	}
	item.value = value
	m.set(key, item)
	return next, nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
	return nil
}

func (m *Memory) Flush(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.items)
	return nil
}

// Prune deletes expired values.
func (m *Memory) Prune(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.prune(), nil
}

// set stores an item. Callers hold mu.
func (m *Memory) set(key string, item memoryItem) {
	m.items[key] = item
	if m.writes++; m.writes%1024 == 0 {
		m.prune()
	}
}

func (m *Memory) prune() int64 {
	var removed int64
	now := time.Now()
	for key, item := range m.items {
		if item.expired(now) {
			delete(m.items, key)
			removed++
		}
	}
	return removed
}
