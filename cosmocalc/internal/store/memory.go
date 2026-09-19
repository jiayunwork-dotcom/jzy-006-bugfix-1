package store

import (
	"context"
	"sync"
	"time"
)

// MemStore is an in-memory Store for tests and ephemeral runs.
type MemStore struct {
	mu      sync.Mutex
	records []Record
	nextID  int64
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore { return &MemStore{nextID: 1} }

// Save appends a record, assigning ID and timestamp.
func (m *MemStore) Save(_ context.Context, rec *Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec.ID = m.nextID
	m.nextID++
	rec.CreatedAt = time.Now().UTC()
	m.records = append(m.records, *rec)
	return nil
}

// List returns records newest-first, honouring the filter.
func (m *MemStore) List(_ context.Context, f Filter) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	out := make([]Record, 0, limit)
	for i := len(m.records) - 1; i >= 0; i-- {
		r := m.records[i]
		if f.Type != "" && r.Type != f.Type {
			continue
		}
		if f.Offset > 0 {
			f.Offset--
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, r)
	}
	return out, nil
}

// Ping always succeeds for the in-memory store.
func (m *MemStore) Ping(_ context.Context) error { return nil }

// Close is a no-op.
func (m *MemStore) Close() error { return nil }
