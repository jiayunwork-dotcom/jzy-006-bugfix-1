package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// PostgresStore persists records in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS computations (
    id         BIGSERIAL PRIMARY KEY,
    type       TEXT        NOT NULL,
    request    JSONB       NOT NULL,
    response   JSONB       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS computations_type_idx ON computations (type);
CREATE INDEX IF NOT EXISTS computations_created_idx ON computations (created_at DESC);
`

// NewPostgresStore connects to the database, verifies the connection and
// ensures the schema exists. It retries for up to ~30s so the service can
// start alongside a still-booting database container.
func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	deadline := time.Now().Add(30 * time.Second)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = db.PingContext(pingCtx)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			db.Close()
			return nil, fmt.Errorf("postgres not reachable within 30s: %w", err)
		}
		time.Sleep(time.Second)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("ensure schema: %w", err)
	}
	return &PostgresStore{db: db}, nil
}

// Save inserts a record and fills in its ID and timestamp.
func (p *PostgresStore) Save(ctx context.Context, rec *Record) error {
	row := p.db.QueryRowContext(ctx,
		`INSERT INTO computations (type, request, response) VALUES ($1, $2, $3)
		 RETURNING id, created_at`,
		rec.Type, rec.Request, rec.Response)
	return row.Scan(&rec.ID, &rec.CreatedAt)
}

// List returns records newest-first, honouring the filter.
func (p *PostgresStore) List(ctx context.Context, f Filter) ([]Record, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, type, request, response, created_at FROM computations`
	args := []any{}
	if f.Type != "" {
		query += ` WHERE type = $1`
		args = append(args, f.Type)
	}
	query += fmt.Sprintf(` ORDER BY id DESC LIMIT %d OFFSET %d`, limit, max(f.Offset, 0))

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.Type, &r.Request, &r.Response, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Ping checks database liveness.
func (p *PostgresStore) Ping(ctx context.Context) error {
	return p.db.PingContext(ctx)
}

// Close releases the connection pool.
func (p *PostgresStore) Close() error { return p.db.Close() }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
