// Package readcache is the shared skeleton for pull-only scope Stores
// (HH-3.5 #169, Colton's START ruling on undeclared scopes: GET
// implemented, POST refused in the contract's own shape at the route).
// Every owning module hands its OWN query and scan to New: the SQL
// names the module's table and carries the caller's scope predicate,
// so the fence rule (a module's repository touches only the tables it
// owns) holds at the query, and this package is plumbing the way the
// pool itself is: it executes the owning module's statement, it never
// authors one.
//
// The write primitives fail closed with sync.ErrOpRefused. The route
// refuses a pull-only scope's POST before the engine consults the
// store, so these arms are the second lock on the same door, not a
// path any caller can reach.
package readcache

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// Scanner is the shared scan surface a row or row-set carries.
type Scanner interface {
	Scan(dest ...any) error
}

// Query is one scope's read shape, authored by the owning module.
type Query struct {
	// Page selects the scope's rows with seq greater than the
	// next-to-last placeholder, at most the last placeholder rows, in
	// seq ascending order, under the caller's predicate. The predicate
	// values come from Args first, then since, then limit.
	Page string
	// ByID selects one row by id: Args' values, then the id, matched to
	// the statement's placeholders in that order. A scope whose rows
	// carry a composite natural key (the feed derives the entity id)
	// leaves ByID empty: its Load answers not-found, the honest miss for
	// a scope whose feed is its whole read surface (no write path ever
	// addresses one row by id).
	ByID string
	// Args builds the predicate values from the caller's filter; ok
	// false fails closed and answers nothing (the wrong caller class or
	// an empty scope stamp never reads past its scope).
	Args func(f sync.ScopeFilter) ([]any, bool)
	// Scan reads one row of Page/ByID's projection: the entity id, its
	// seq, its tombstone flag, and the wire projection. A tombstoned
	// row may return a nil projection.
	Scan func(row Scanner) (id string, seq int64, deleted bool, data map[string]any, err error)
}

// Store is the pull-only sync.Store the skeleton builds.
type Store struct {
	pool *pgxpool.Pool
	q    Query
}

// New wires one scope's Store.
func New(pool *pgxpool.Pool, q Query) *Store { return &Store{pool: pool, q: q} }

// Pull returns the page of rows past since under the caller's
// predicate.
func (s *Store) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	args, ok := s.q.Args(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	args = append(append([]any{}, args...), since, limit)
	rows, err := s.pool.Query(ctx, s.q.Page, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("readcache: pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		id, seq, deleted, data, err := s.q.Scan(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("readcache: pull scan: %w", err)
		}
		if deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: id, Seq: seq})
			continue
		}
		items = append(items, sync.Entity{ID: id, Seq: seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("readcache: pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied fails closed: the scope has no push.
func (s *Store) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	return sync.RowState{}, false, sync.ErrOpRefused
}

// Load reads one row by id under the caller's predicate.
func (s *Store) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	if s.q.ByID == "" {
		return sync.RowState{}, false, nil
	}
	args, ok := s.q.Args(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	args = append(append([]any{}, args...), id)
	idOut, seq, deleted, data, err := s.q.Scan(s.pool.QueryRow(ctx, s.q.ByID, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("readcache: load: %w", err)
	}
	if idOut == "" {
		return sync.RowState{}, false, nil
	}
	return sync.RowState{ID: idOut, Seq: seq, Deleted: deleted, Data: data}, true, nil
}

// Create fails closed: the scope has no push.
func (s *Store) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, sync.ErrOpRefused
}

// Write fails closed: the scope has no push.
func (s *Store) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, sync.ErrOpRefused
}

// Delete fails closed: the scope has no push.
func (s *Store) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	return sync.RowState{}, sync.ErrOpRefused
}
