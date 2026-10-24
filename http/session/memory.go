package session

import (
	"context"
	"maps"
	"sync"
	"time"
)

// MemoryStore keeps sessions in process memory. Sessions are lost on restart
// and are not shared between instances; use it for development, tests and
// single-instance deployments.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]memoryEntry
	saves    int
}

type memoryEntry struct {
	data    map[string]string
	expires time.Time
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{sessions: map[string]memoryEntry{}} }

func (s *MemoryStore) Load(_ context.Context, id string) (map[string]string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.sessions[id]
	if !ok || !time.Now().Before(entry.expires) {
		delete(s.sessions, id)
		return nil, false, nil
	}
	return maps.Clone(entry.data), true, nil
}

func (s *MemoryStore) Save(_ context.Context, id string, data map[string]string, expires time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = memoryEntry{maps.Clone(data), expires}
	if s.saves++; s.saves%1024 == 0 { // prune expired sessions periodically
		now := time.Now()
		for key, entry := range s.sessions {
			if !now.Before(entry.expires) {
				delete(s.sessions, key)
			}
		}
	}
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}
