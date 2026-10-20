//go:build integration

// drawpark.go: the shared park of the draw before lock legs (PR 556, rounds 3c and 3d).
//
// THE METHOD. A leg that parks a writer through an AFTER trigger on scope_sequences can never stand a CTE
// statement in its draw-before-lock window: a data modifying CTE's AFTER triggers fire at the end of the whole
// statement, after the outer UPDATE has locked every row. So the legs park the OTHER writer, the single row
// writer that relies on the trigger, between its row lock and its draw, with a BEFORE INSERT trigger on
// scope_sequences, which fires before the upsert takes the counter row. That writer holds the row and no
// counter. The writer under test then runs: in the crossing shape it draws the counter and waits for the row;
// the test releases the park only when it is seen waiting; the parked writer then wants the counter.
package testkit

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DrawPark parks sessions of its own pool (application name sf1-park) right before they take the counter row
// of one scope, on an advisory lock the test holds.
type DrawPark struct {
	t       *testing.T
	base    *pgxpool.Pool
	Pool    *pgxpool.Pool // the park pool: run the single row writer on it
	hold    *pgxpool.Conn
	key     int64
	scope   string
	release func()
}

func NewDrawPark(t *testing.T, base *pgxpool.Pool, scope string, key int64) *DrawPark {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION sf1_park_before() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN
		   IF NEW.scope = TG_ARGV[0] AND current_setting('application_name') = 'sf1-park' THEN
		     PERFORM pg_advisory_xact_lock(TG_ARGV[1]::bigint);
		   END IF;
		   RETURN NEW;
		 END $$`,
		`DROP TRIGGER IF EXISTS sf1_park_before ON scope_sequences`,
		fmt.Sprintf(`CREATE TRIGGER sf1_park_before BEFORE INSERT ON scope_sequences FOR EACH ROW EXECUTE FUNCTION sf1_park_before('%s', '%d')`, scope, key),
		fmt.Sprintf(`INSERT INTO scope_sequences (scope, next_value) VALUES ('%s', 0) ON CONFLICT (scope) DO NOTHING`, scope),
	} {
		if _, err := base.Exec(ctx, q); err != nil {
			t.Fatalf("park trigger: %v", err)
		}
	}
	cfg := base.Config().Copy()
	cfg.ConnConfig.RuntimeParams["application_name"] = "sf1-park"
	cfg.MaxConns = 2
	park, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("park pool: %v", err)
	}
	hold, err := base.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := hold.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatalf("hold: %v", err)
	}
	p := &DrawPark{t: t, base: base, Pool: park, hold: hold, key: key, scope: scope}
	released := false
	p.release = func() {
		if !released {
			released = true
			_, _ = hold.Exec(ctx, `SELECT pg_advisory_unlock($1)`, key)
		}
	}
	t.Cleanup(func() {
		p.release()
		hold.Release()
		park.Close()
		c := context.Background()
		_, _ = base.Exec(c, `DROP TRIGGER IF EXISTS sf1_park_before ON scope_sequences`)
		_, _ = base.Exec(c, `DROP FUNCTION IF EXISTS sf1_park_before()`)
	})
	return p
}

// WaitParked blocks until a park pool session stands on the advisory lock.
func (p *DrawPark) WaitParked(what string) {
	p.t.Helper()
	WaitCount(p.t, p.base, "parked: "+what, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`)
}

// WaitBlocked blocks until a session waits on a row or counter lock (not the park).
func (p *DrawPark) WaitBlocked(what string) {
	p.t.Helper()
	WaitCount(p.t, p.base, "blocked: "+what,
		`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event <> 'advisory'`)
}

func WaitCount(t *testing.T, pool *pgxpool.Pool, what, q string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var n int
		if err := pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("never saw: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Answers releases the park and requires every writer to finish without a deadlock victim.
func (p *DrawPark) Answers(chs map[string]chan error) {
	p.t.Helper()
	p.release()
	for who, ch := range chs {
		select {
		case err := <-ch:
			if err != nil && (strings.Contains(err.Error(), "40P01") || strings.Contains(err.Error(), "deadlock")) {
				p.t.Fatalf("%s was killed as a deadlock victim: %v", who, err)
			}
			if err != nil {
				p.t.Logf("%s answered: %v", who, err)
			}
		case <-time.After(30 * time.Second):
			p.t.Fatalf("%s never finished", who)
		}
	}
}

// TouchRow is the trigger relying single row writer: it changes a row and lets the BEFORE UPDATE trigger draw.
// Run on the park pool it stands between its row lock and its draw.
func (p *DrawPark) TouchRow(table, set, id string) chan error {
	ch := make(chan error, 1)
	go func() {
		_, err := p.Pool.Exec(context.Background(),
			fmt.Sprintf(`UPDATE %s SET %s WHERE id = $1::uuid`, table, set), id)
		ch <- err
	}()
	return ch
}

// - Part B: S2's two multi row writers, and the freight raise path -

// The sync cancel's un-assign: a statement that draws the stops counter in a CTE and updates the run's READY
// stops. A single stop write (here the trigger relying touch) holds one of those rows between its lock and its
