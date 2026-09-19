// Package store persists every computation request and its result so
// history can be queried later. PostgreSQL is the production backend;
// an in-memory implementation backs unit tests and local runs.
package store

import (
	"context"
	"encoding/json"
	"time"
)

// Record is one persisted computation request/response pair.
type Record struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"` // "redshift" or "distance" (each batch line persists as one "distance" record)
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response"`
	CreatedAt time.Time       `json:"created_at"`
}

// Filter narrows history queries.
type Filter struct {
	Type   string // empty = all types
	Limit  int    // <=0 means default
	Offset int
}

// Store is the persistence contract. Implementations must be safe for
// concurrent use.
type Store interface {
	Save(ctx context.Context, rec *Record) error
	List(ctx context.Context, f Filter) ([]Record, error)
	Ping(ctx context.Context) error
	Close() error
}
