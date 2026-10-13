//go:build integration

// keybody_cancel_integration_test.go: the fourth guarantee when the client
// goes away between the module's commit and the ledger write. net/http
// cancels the request context when the connection drops, so the ledger
// write must not ride it: an unbound key is applied for good and, once its
// row moves on, applies a second time under another body.
package sync_test

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// cancelAfter cancels the request context right after the wrapped store's
// write returns, the instant the module has committed.
type cancelAfter struct {
	sync.Store
	cancel func()
	after  string // "create" or "write"
}

func (s cancelAfter) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	r, err := s.Store.Create(ctx, f, key, fields)
	if s.after == "create" {
		s.cancel()
	}
	return r, err
}

func (s cancelAfter) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	r, err := s.Store.Write(ctx, f, id, key, fields)
	if s.after == "write" {
		s.cancel()
	}
	return r, err
}

func ledgerRows(t *testing.T, f *the169Fixture, key string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM sync_op_hashes WHERE client_id = $1::uuid`, key).Scan(&n); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	return n
}

// A push whose request context dies right after the module committed still
// binds its key, and the key stays bound when the row moves on.
func TestKeyBodyCancelAfterCommitStillBindsTheKey(t *testing.T) {
	for _, after := range []string{"create", "write"} {
		t.Run(after, func(t *testing.T) {
			f := new169Fixture(t)
			cleanLedger(t, f)
			led := sync.NewPgKeyLedger(f.pool)
			f.engine.WithKeyLedger(led)
			k := f.quotesKit()

			// An engine over the same store whose request context is
			// cancelled the moment the write returns.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			dropped := sync.NewEngine().WithKeyLedger(led)
			if err := dropped.RegisterScope("customer_quotes", cancelAfter{
				Store: customerquote.NewSyncStore(f.cqStore, f.cqSvc), cancel: cancel, after: after,
			}); err != nil {
				t.Fatalf("register: %v", err)
			}

			var bound string
			var original, moved apigen.SyncPushOperation
			if after == "create" {
				original = apigen.SyncPushOperation{ClientId: newKey(1100), Op: "create", Payload: k.Create("")}
				bound = original.ClientId
				_, _ = dropped.Push(ctx, k.Scope, k.Filter, []apigen.SyncPushOperation{original})
			} else {
				if r := d30Push(t, f, apigen.SyncPushOperation{ClientId: newKey(1101), Op: "create", Payload: k.Create("")}); r.Status != "applied" {
					t.Fatalf("setup create: %+v", r)
				}
				id := pullOf(t, f.engine, "customer_quotes", k.Filter, 0, 500)[0]
				original = apigen.SyncPushOperation{ClientId: newKey(1102), Op: "update", Payload: map[string]any{"id": id, "markup_bps": 1100}}
				bound = original.ClientId
				_, _ = dropped.Push(ctx, k.Scope, k.Filter, []apigen.SyncPushOperation{original})
			}
			if ctx.Err() == nil {
				t.Fatalf("the wrapper never cancelled the request context")
			}
			if n := ledgerRows(t, f, bound); n != 1 {
				t.Fatalf("%d ledger rows for the key after a cancel past the commit, want 1", n)
			}

			// The row moves on, so the module no longer dedups the key.
			id := pullOf(t, f.engine, "customer_quotes", k.Filter, 0, 500)[0]
			moved = apigen.SyncPushOperation{ClientId: newKey(1103), Op: "update", Payload: map[string]any{"id": id, "markup_bps": 1200}}
			if r := d30Push(t, f, moved); r.Status != "applied" {
				t.Fatalf("moving the row on: %+v", r)
			}

			// The retry with the first body is a replay; another body is refused.
			evil := original
			if after == "create" {
				evil.Payload = k.Create("")
			} else {
				evil.Payload = map[string]any{"id": id, "markup_bps": 9900}
			}
			if r := d30Push(t, f, evil); r.Status != "rejected" || r.Error == nil || len(*r.Error) < 10 || (*r.Error)[:10] != "key_reused" {
				t.Fatalf("moved-on key with another body answered %+v, want key_reused", r)
			}
			if r := d30Push(t, f, original); r.Status != "applied" {
				t.Fatalf("moved-on key with its own body answered %+v, want applied", r)
			}
			if m := d30Markup(t, f, id); after == "write" && m != 1200 {
				t.Fatalf("markup %v, want 1200 (the refused body never applied)", m)
			}
			if n := quoteCount(t, f); after == "create" && n != 1 {
				t.Fatalf("%d documents, want the one", n)
			}
		})
	}
}
