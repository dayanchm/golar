package connection

import (
	"errors"
	"fmt"
	"sync"
)

type Manager struct {
	mu          sync.RWMutex
	connections map[string]*DB
}

func NewManager() *Manager { return &Manager{connections: make(map[string]*DB)} }
func (m *Manager) Register(name string, config Config) (*DB, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" {
		return nil, errors.New("database: connection name is required")
	}
	if _, ok := m.connections[name]; ok {
		return nil, fmt.Errorf("database: duplicate connection %q", name)
	}
	db, err := Open(config)
	if err != nil {
		return nil, err
	}
	m.connections[name] = db
	return db, nil
}
func (m *Manager) Connection(name string) (*DB, error) {
	if name == "" {
		name = "default"
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	db, ok := m.connections[name]
	if !ok {
		return nil, fmt.Errorf("database: connection %q not found", name)
	}
	return db, nil
}
func (m *Manager) Close(name string) error {
	if name == "" {
		name = "default"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	db, ok := m.connections[name]
	if !ok {
		return fmt.Errorf("database: connection %q not found", name)
	}
	err := db.Close()
	delete(m.connections, name)
	return err
}
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	for name, db := range m.connections {
		err = errors.Join(err, db.Close())
		delete(m.connections, name)
	}
	return err
}
