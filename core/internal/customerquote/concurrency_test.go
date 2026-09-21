package customerquote

// The concurrency pins, made permanent per the C2 review (R1 and the
// reviewer's standing probes) and the C3 re-review (N1): the one-transaction
// accept serializes under the quote row lock (one 200, one 409, one
// acceptance row); a change request's append is re-based on the locked row,
// so concurrent change requests cannot silently drop a customer's message
// (the demonstrated R1 loss: against the pre-lock append shape, every racing
// writer built from the same stale column and the later commit overwrote the
// earlier message with both requests still answering 200); and a change
// request racing a rotation through a generation that ended dead refuses
// under the lock instead of writing through it (the demonstrated N1 window).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestConcurrentAcceptsSerialize pins the accept path's serialization: two
// accepts racing on one live token answer one 200 and one 409 naming the
// standing state, exactly one acceptance row exists, and that row's artifact
// verifies. The under-lock re-checks are what make this true; the test keeps
// them from being removed as redundant. All assertions run on the test
// goroutine; the racers only record what came back.
func TestConcurrentAcceptsSerialize(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := b.createQuote(t, fixtureCreate(horizon))
	sent := b.sendQuote(t, created.ID)

	const racers = 2
	payload, err := json.Marshal(AcceptRequest{TypedName: "Fixture Homeowner One", Accepted: true})
	if err != nil {
		t.Fatalf("marshal accept body: %v", err)
	}
	start := make(chan struct{})
	type outcome struct {
		code int
		body string
	}
	outcomes := make([]outcome, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, b.acceptURL(sent.MagicLinkToken), bytes.NewReader(payload))
			rec := httptest.NewRecorder()
			b.mux.ServeHTTP(rec, req)
			outcomes[i] = outcome{code: rec.Code, body: rec.Body.String()}
		}(i)
	}
	close(start)
	wg.Wait()

	oneOK, oneConflict := 0, 0
	for _, o := range outcomes {
		switch o.code {
		case http.StatusOK:
			oneOK++
		case http.StatusConflict:
			oneConflict++
			var e errorBody
			_ = json.Unmarshal([]byte(o.body), &e)
			if e.Error != SentenceAlreadyResponded {
				t.Fatalf("the losing accept body %q, want the already-responded sentence", e.Error)
			}
		default:
			t.Fatalf("a racing accept answered %d, want only 200 or 409", o.code)
		}
	}
	if oneOK != 1 || oneConflict != 1 {
		t.Fatalf("racing accepts answered %d x 200 and %d x 409, want exactly one of each", oneOK, oneConflict)
	}
	if got := b.scalarInt(t, `SELECT count(*) FROM quote_acceptance WHERE customer_quote_id = $1`, created.ID); got != 1 {
		t.Fatalf("the race produced %d acceptance rows, want exactly 1", got)
	}
	var acceptanceID string
	if err := b.pool.QueryRow(ctx,
		`SELECT id::text FROM quote_acceptance WHERE customer_quote_id = $1`, created.ID).Scan(&acceptanceID); err != nil {
		t.Fatalf("acceptance row: %v", err)
	}
	if v, err := b.svc.VerifyAcceptance(ctx, acceptanceID); err != nil || !v.OK {
		t.Fatalf("the winning acceptance must verify: %+v %v", v, err)
	}
}

// TestConcurrentChangeRequestsAllSurvive is R1's regression test: change
// requests racing on one live token must all land, because the append base is
// the locked row. Against the pre-lock append shape the later commits
// overwrote the earlier messages silently while every request answered 200.
func TestConcurrentChangeRequestsAllSurvive(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := b.createQuote(t, fixtureCreate(horizon))
	sent := b.sendQuote(t, created.ID)

	const racers = 6
	messages := make([]string, racers)
	for i := range messages {
		messages[i] = "Fixture change request number " + string(rune('A'+i))
	}
	start := make(chan struct{})
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := b.svc.RecordChangeRequest(ctx, sent.MagicLinkToken, messages[i])
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("racing change request %d failed: %v", i, err)
		}
	}
	stored := b.scalarStr(t, `SELECT change_requests::text FROM customer_quote WHERE id = $1`, created.ID)
	var recorded []ChangeRequest
	if err := json.Unmarshal([]byte(stored), &recorded); err != nil {
		t.Fatalf("stored change requests did not parse: %v: %s", err, stored)
	}
	if len(recorded) != racers {
		t.Fatalf("the column holds %d change requests, want all %d (a silent loss is exactly the R1 defect)", len(recorded), racers)
	}
	seen := map[string]bool{}
	for _, r := range recorded {
		seen[r.Message] = true
	}
	for _, m := range messages {
		if !seen[m] {
			t.Fatalf("the change request %q was silently lost", m)
		}
	}
}

// TestConcurrentChangeRequestVsRotation is N1's regression leg: a change
// request racing a rotation must refuse, not write, when the rotation commits
// inside the resolve-to-lock window. The window is forced open
// deterministically instead of by luck: the test itself holds the quote row
// lock, the racer's pre-lock resolve has already classified the still-live
// generation on the pre-rotation snapshot when it parks on that lock
// (confirmed through pg_stat_activity before the rotation commits), and the
// production rotation write then commits as the lock releases, so the racer's
// locked read observes the post-rotation row while its presented generation
// ended dead. Through that generation the answer is 410 with the dead-link
// sentence and no message is stored; the unfixed shape appended first and
// answered 410 after the message had already landed (the re-review's probe:
// a write performed through a dead generation, answered as a dead link).
func TestConcurrentChangeRequestVsRotation(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := b.createQuote(t, fixtureCreate(horizon))
	gen1 := b.sendQuote(t, created.ID)

	// Hold the row lock, which parks the racer between its pre-lock resolve
	// and its locked read: exactly the re-review's race window.
	txHold, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin hold transaction: %v", err)
	}
	defer txHold.Rollback(ctx)
	locked, ok, lerr := b.svc.store.GetQuoteLockTx(ctx, txHold, created.ID)
	if lerr != nil || !ok {
		t.Fatalf("take the row lock for the hold: %v (found %v)", lerr, ok)
	}

	done := make(chan error, 1)
	go func() {
		_, rerr := b.svc.RecordChangeRequest(ctx, gen1.MagicLinkToken, "Fixture change request racing a rotation")
		done <- rerr
	}()

	// The window is open only once the racer's FOR UPDATE is parked: its
	// pre-lock resolve has run and its locked read has not. The probe
	// excludes this backend and counts only lock waiters, so it cannot
	// count itself.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var parked int
		if perr := b.pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND pid <> pg_backend_pid()
			   AND wait_event_type = 'Lock'
			   AND query LIKE '%FOR UPDATE%' AND query LIKE '%customer_quote%'`).Scan(&parked); perr != nil {
			t.Fatalf("parking probe: %v", perr)
		}
		if parked >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the change-request racer never parked on the row lock")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// The rotation, the production re-send write, commits as the lock
	// releases: the racer's locked read sees the post-rotation row.
	state, perr := ParseTokenState(*locked.ShareTokenHash)
	if perr != nil {
		t.Fatalf("stored token value under hold: %v", perr)
	}
	_, freshHash, merr := MintToken()
	if merr != nil {
		t.Fatalf("mint the rotation's fresh token: %v", merr)
	}
	if locked.ValidUntil == nil {
		t.Fatalf("the sent quote carries no offer bound")
	}
	if rerr := b.svc.store.RotateOnSend(ctx, txHold, created.ID, state.InstallLive(freshHash),
		locked.LinesJSON, *locked.ValidUntil, *locked.ValidUntil); rerr != nil {
		t.Fatalf("rotate under hold: %v", rerr)
	}
	if cerr := txHold.Commit(ctx); cerr != nil {
		t.Fatalf("commit the hold with the rotation: %v", cerr)
	}

	rerr := <-done
	refusal, isRefusal := rerr.(*Error)
	if !isRefusal {
		t.Fatalf("the racing change request returned %v, want the dead-link refusal", rerr)
	}
	if refusal.Status != http.StatusGone || refusal.Message != SentenceLinkNoLongerValid {
		t.Fatalf("the racing change request answered %d %q, want 410 with the dead-link sentence", refusal.Status, refusal.Message)
	}

	// The rotation truly committed inside the window: generation one ended
	// dead and the rotation's fresh hash is live, so this leg exercised the
	// race rather than the ordinary dead-token path.
	stored := b.scalarStr(t, `SELECT share_token_hash FROM customer_quote WHERE id = $1`, created.ID)
	final, perr := ParseTokenState(stored)
	if perr != nil {
		t.Fatalf("stored token value after the rotation: %v", perr)
	}
	if final.IsLive(HashToken(gen1.MagicLinkToken)) || !final.ContainsDead(HashToken(gen1.MagicLinkToken)) {
		t.Fatalf("generation one did not end dead inside the window")
	}
	if !final.IsLive(freshHash) {
		t.Fatalf("the rotation's fresh hash is not live after the window")
	}

	// The N1 line itself: no message is stored through the dead generation.
	storedCR := b.scalarStr(t, `SELECT coalesce(change_requests::text, 'null') FROM customer_quote WHERE id = $1`, created.ID)
	if storedCR != "null" {
		var recorded []ChangeRequest
		if jerr := json.Unmarshal([]byte(storedCR), &recorded); jerr != nil {
			t.Fatalf("stored change requests did not parse: %v: %s", jerr, storedCR)
		}
		if len(recorded) != 0 {
			t.Fatalf("the column holds %d change request(s) through a dead generation, want none (the N1 defect: the write landed before the 410)", len(recorded))
		}
	}
}
