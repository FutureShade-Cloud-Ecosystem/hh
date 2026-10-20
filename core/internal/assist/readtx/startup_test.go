package readtx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// pidTx answers pg_backend_pid() from a script, as a statement-pooling proxy
// would: a different server connection may serve each statement.
type pidTx struct {
	pgx.Tx
	pids       []int
	n          int
	rolledBack *bool
}

func (p *pidTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	pid := p.pids[p.n%len(p.pids)]
	p.n++
	return fakeRow{pid: pid}
}
func (p *pidTx) Rollback(context.Context) error { *p.rolledBack = true; return nil }

func readerOverPids(t *testing.T, pids ...int) (*Reader, *bool) {
	rb := new(bool)
	return &Reader{
		audience: AudienceStaff, role: "r", signer: fixedSigner(t), limits: Limits{Statement: time.Second, Lock: time.Second},
		begin: func(context.Context, pgx.TxOptions) (pgx.Tx, error) { return &pidTx{pids: pids, rolledBack: rb}, nil },
	}, rb
}

func TestStatementPoolingIsRefused(t *testing.T) {
	r, rolledBack := readerOverPids(t, 11, 12)
	err := r.checkSessionStable(context.Background())
	if !errors.Is(err, ErrStatementPooling) {
		t.Fatalf("err = %v, want ErrStatementPooling", err)
	}
	if !*rolledBack {
		t.Error("the check left its transaction open")
	}
}

func TestSessionPoolingAndDirectConnectionsPass(t *testing.T) {
	r, _ := readerOverPids(t, 11, 11)
	if err := r.checkSessionStable(context.Background()); err != nil {
		t.Fatalf("one pid across the transaction must pass: %v", err)
	}
}
