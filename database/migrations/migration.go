package migrations

import (
	"fmt"
	"github.com/dayanchm/golar/database/schema"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Migration interface {
	Up(*schema.Schema) error
	Down(*schema.Schema) error
}
type Entry struct {
	Name      string
	Migration Migration
}
type Registry struct {
	mu      sync.RWMutex
	entries map[string]Migration
}

func NewRegistry() *Registry { return &Registry{entries: make(map[string]Migration)} }

var DefaultRegistry = NewRegistry()
var validName = regexp.MustCompile(`^[0-9]{8}(?:_[0-9]{6})?_[a-z][a-z0-9_]*$`)

func ValidateName(name string) error {
	if !validName.MatchString(name) || len(name) > 200 {
		return fmt.Errorf("migration: invalid name %q", name)
	}
	parts := strings.Split(name, "_")
	stamp := parts[0]
	layout := "20060102"
	if len(parts[1]) == 6 && parts[1][0] >= '0' && parts[1][0] <= '9' {
		stamp += "_" + parts[1]
		layout = "20060102_150405"
	}
	if _, err := time.Parse(layout, stamp); err != nil {
		return fmt.Errorf("migration: invalid timestamp: %w", err)
	}
	return nil
}
func (r *Registry) Register(name string, migration Migration) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if migration == nil {
		return fmt.Errorf("migration: nil migration")
	}
	value := reflect.ValueOf(migration)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return fmt.Errorf("migration: nil migration")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[name]; ok {
		return fmt.Errorf("migration: duplicate migration %q", name)
	}
	r.entries[name] = migration
	return nil
}
func (r *Registry) Entries() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := make([]Entry, 0, len(r.entries))
	for name, m := range r.entries {
		entries = append(entries, Entry{Name: name, Migration: m})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}
func Register(name string, m Migration) error { return DefaultRegistry.Register(name, m) }
func MustRegister(name string, m Migration) {
	if err := Register(name, m); err != nil {
		panic(err)
	}
}
