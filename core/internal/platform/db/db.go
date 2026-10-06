// Package db owns the PostgreSQL connection pool (pgx v5).
// Modules receive the pool via constructor injection and own ONLY their
// tables, zero cross-module table access (COMMAND.md standing rule 3).
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	return ConnectWithMaxConns(ctx, url, 0)
}

// ConnectWithMaxConns is Connect with the pool's size pinned to maxConns (zero or less keeps the
// pgx default, which is what Connect asks for). It exists for a small dedicated pool that must
// carry exactly the session the shared pool carries (same URL, same connect path, same ping) and
// differ only in how many connections it may hold.
func ConnectWithMaxConns(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}
	return pool, nil
}
