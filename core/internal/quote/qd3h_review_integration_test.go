//go:build integration

// qd3h_review_integration_test.go, QD-3d review round 3: a keyed accept whose link died between its
// link resolve and its quote row lock is refused under that lock, so a keyed 410 is never followed by
// a commit. Fixtures use role words and times relative to the clock, never a person or a calendar date.
package quote

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pausedAccept is a shareStore whose AcceptQuoteGuarded stops before it does anything (before its
// Begin, which is where a pool that has no free connection makes a request wait) until released.
type pausedAccept struct {
	shareStore
	reached chan struct{}
	release chan struct{}
}

func (p pausedAccept) AcceptQuoteGuarded(ctx context.Context, in acceptWrite, hooks acceptHooks) (AcceptanceResult, error) {
	close(p.reached)
	<-p.release
	return p.shareStore.AcceptQuoteGuarded(ctx, in, hooks)
}

// qd3hKillLink is how the link dies in the window: revoked, or moved past its expiry on the database
// clock.
func qd3hKillLink(t *testing.T, pool *pgxpool.Pool, linkID string, expire bool) {
	t.Helper()
	q := `UPDATE quote_share_links SET revoked_at = now() WHERE id = $1`
	if expire {
		q = `UPDATE quote_share_links SET expires_at = now() - interval '1 hour' WHERE id = $1`
	}
	if _, err := pool.Exec(context.Background(), q, linkID); err != nil {
		t.Fatalf("kill the link: %v", err)
	}
}

// P2-1: the exact probe of the review. Attempt 1 resolved the live link and is paused before its
// transaction; the link dies; the keyed retry is sent. Whatever the retry answers, a 410 must never be
// followed by a commit: either the retry is 200 with the first outcome, or attempt 1 is refused with
// the same 410 and no acceptance exists.
func TestIntegration_QD3h_KeyedGoneNeverFollowedByCommit(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	for _, multi := range []bool{true, false} {
		for _, expire := range []bool{false, true} {
			name := "multi_off"
			if multi {
				name = "multi_on"
			}
			if expire {
				name += "_expired"
			} else {
				name += "_revoked"
			}
			t.Run(name, func(t *testing.T) {
				setMultiAccept(t, pool, multi)
				svc, quoteID, token, linkID := qd3fMinted(t, pool, now)
				ctx := context.Background()
				p := pausedAccept{shareStore: svc.shares, reached: make(chan struct{}), release: make(chan struct{})}
				slow := *svc
				slow.shares = p
				first := make(chan answerOrErr, 1)
				go func() {
					r, err := slow.AcceptByTokenKeyed(ctx, token, qd3fCustomer, true, qd3dKeyA)
					first <- answerOrErr{res: r, err: err}
				}()
				select {
				case <-p.reached:
				case <-time.After(15 * time.Second):
					t.Fatalf("attempt 1 never reached the store")
				}
				qd3hKillLink(t, pool, linkID, expire)
				w := qd3dPost(t, svc, ctx, token, qd3dKeyA, qd3fCustomer)
				close(p.release)
				a := <-first
				rows := acceptanceRows(t, pool, quoteID)
				switch w.Code {
				case http.StatusGone:
					if !errors.Is(a.err, ErrLinkGone) {
						t.Errorf("the retry was told 410 but attempt 1 answered err=%v, want the same 410 (ErrLinkGone)", a.err)
					}
					if rows != 0 {
						t.Errorf("a keyed 410 was followed by %d acceptance rows, want 0", rows)
					}
				case http.StatusOK:
					if a.err != nil {
						t.Fatalf("attempt 1: %v", a.err)
					}
					if rows != 1 {
						t.Errorf("acceptance rows = %d, want 1", rows)
					}
				default:
					t.Errorf("retry = %d (%s), want 410 or 200", w.Code, w.Body.String())
				}
			})
		}
	}
}

// An unkeyed accept is unchanged: the same window with no key still commits, as it did before the key
// existed (a behaviour change for unkeyed accepts needs a ruling).
func TestIntegration_QD3h_UnkeyedAcceptUnchanged(t *testing.T) {
	pool := intPool(t)
	now := qd3fNow()
	setMultiAccept(t, pool, true)
	svc, quoteID, token, linkID := qd3fMinted(t, pool, now)
	p := pausedAccept{shareStore: svc.shares, reached: make(chan struct{}), release: make(chan struct{})}
	slow := *svc
	slow.shares = p
	first := make(chan answerOrErr, 1)
	go func() {
		r, err := slow.AcceptByTokenKeyed(context.Background(), token, qd3fCustomer, true, "")
		first <- answerOrErr{res: r, err: err}
	}()
	<-p.reached
	qd3hKillLink(t, pool, linkID, false)
	close(p.release)
	if a := <-first; a.err != nil {
		t.Fatalf("unkeyed attempt 1: %v", a.err)
	}
	if n := acceptanceRows(t, pool, quoteID); n != 1 {
		t.Errorf("acceptance rows = %d, want 1", n)
	}
}
