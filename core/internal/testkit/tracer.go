package testkit

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StatementCounter is the pgx tracer that turns "never reaches a
// repository" from an assertion into a measurement (QA finding F5). It
// counts executed statements through the pool's connection tracer: one
// per query, one per batched query, one per copy-from. The sweeps wrap
// the sweep database's pool with it, reset before a request, and assert
// the count after: zero on every refusal leg, exactly one on the
// provisioning gap, which is the only refusal that reaches the anchor
// read (B2 review notes, item 12).
type StatementCounter struct {
	mu    sync.Mutex
	count int
}

// Count reports executed statements since the last reset.
func (c *StatementCounter) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// Reset zeroes the count. The per-request pattern: reset, drive one
// request, assert.
func (c *StatementCounter) Reset() {
	c.mu.Lock()
	c.count = 0
	c.mu.Unlock()
}

func (c *StatementCounter) add(n int) {
	c.mu.Lock()
	c.count += n
	c.mu.Unlock()
}

// AssertCount fails the test unless the count is want. leg names the
// sweep leg in the failure, so a wrong count names the invariant at stake.
func (c *StatementCounter) AssertCount(t *testing.T, leg string, want int) {
	t.Helper()
	if got := c.Count(); got != want {
		t.Fatalf("testkit: leg %s: %d statements executed, want %d", leg, got, want)
	}
}

// TraceQueryStart implements pgx.QueryTracer: one executed statement. The
// context passes through unchanged; the kit tracer observes, never alters.
func (c *StatementCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.add(1)
	return ctx
}

// TraceQueryEnd implements pgx.QueryTracer.
func (c *StatementCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TraceBatchStart implements pgx.BatchTracer. The batch's statements are
// each counted by TraceBatchQuery, so the start adds nothing and no
// statement is double counted.
func (c *StatementCounter) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return ctx
}

// TraceBatchQuery implements pgx.BatchTracer: one executed statement.
func (c *StatementCounter) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {
	c.add(1)
}

// TraceBatchEnd implements pgx.BatchTracer.
func (c *StatementCounter) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

// TraceCopyFromStart implements pgx.CopyFromTracer: one executed
// statement.
func (c *StatementCounter) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	c.add(1)
	return ctx
}

// TraceCopyFromEnd implements pgx.CopyFromTracer.
func (c *StatementCounter) TraceCopyFromEnd(context.Context, *pgx.Conn, pgx.TraceCopyFromEndData) {}

// Compile-time proof that the counter satisfies the pgx tracer forms the
// pool accepts, so a pgx upgrade that changes them fails here, at build
// time, instead of silently not counting.
var (
	_ pgx.QueryTracer    = (*StatementCounter)(nil)
	_ pgx.BatchTracer    = (*StatementCounter)(nil)
	_ pgx.CopyFromTracer = (*StatementCounter)(nil)
)

// NewCountingPool parses the database URL, attaches the counter to every
// connection the pool will open, and connects. pgx v5.10.0 (the version
// the ERP baseline pins) carries one tracer per connection config, and the
// connection decomposes it into its tracer roles, which is why the counter
// implements them all. The pool is constructed with the tracer attached
// (the F5 form): statements cannot execute on it without being counted,
// so a zero count is a proof, not an observation gap.
func NewCountingPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, *StatementCounter, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, nil, err
	}
	counter := &StatementCounter{}
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return pool, counter, nil
}

// PgxPool adapts a pgx pool to the builder's DB interface, so the same
// fixture code runs over whatever handle the harness holds.
func PgxPool(pool *pgxpool.Pool) DB {
	return pgxDB{pool: pool}
}

type pgxDB struct{ pool *pgxpool.Pool }

// Exec implements DB.
func (d pgxDB) Exec(ctx context.Context, query string, args ...any) error {
	_, err := d.pool.Exec(ctx, query, args...)
	return err
}

// Count implements DB.
func (d pgxDB) Count(ctx context.Context, query string, args ...any) (int, error) {
	var n int
	err := d.pool.QueryRow(ctx, query, args...).Scan(&n)
	return n, err
}
