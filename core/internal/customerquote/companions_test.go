package customerquote

// The companions of T1 and T2: the F-3 classification cells on real rows,
// the S2 cross-quote leg, the S5 artifact verification leg with its mutation
// matrix and key rotation, the mandatory-write rollback (the M1d proof), and
// the tenant-side lifecycle rules R2 to R8 with their ported sentences and
// the ported HH Pro regressions.

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// S4 families and the F-3 cells, each asserted by status and by the shared
// body per family (no oracle: malformed and never-existed share one body).
func TestTokenClassificationMatrix(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := b.createQuote(t, fixtureCreate(horizon))
	sent := b.sendQuote(t, created.ID)

	never := strings.Repeat("Q", 43) // well-formed, never minted
	malformed := "not-a-token!!"
	r1 := b.publicView(t, never)
	e1 := assertStatus(t, "never existed", r1, http.StatusNotFound)
	r2 := b.publicView(t, malformed)
	e2 := assertStatus(t, "malformed", r2, http.StatusNotFound)
	if e1.Error != e2.Error || e1.Error != MsgLinkNotExist {
		t.Fatalf("malformed and never-existed must share one body: %q vs %q", e1.Error, e2.Error)
	}
	if r1.Body.String() != r2.Body.String() {
		t.Fatalf("the two 404 bodies differ byte for byte")
	}

	// Link bound passed while the offer still stands: 410.
	if _, err := b.pool.Exec(ctx,
		`UPDATE customer_quote SET share_expires_at = now() - interval '1 hour' WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("poke: %v", err)
	}
	if rec := b.publicView(t, sent.MagicLinkToken); rec.Code != http.StatusGone {
		t.Fatalf("link-expired view: %d, want 410", rec.Code)
	}
	if rec := b.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true); rec.Code != http.StatusGone {
		t.Fatalf("link-expired accept: %d, want 410", rec.Code)
	}

	// Offer bound passed with the link bound still ahead (a planted row; the
	// deployment never writes a link bound later than the offer bound): the
	// view serves with expired true and acceptable false, accept refuses 409.
	if _, err := b.pool.Exec(ctx,
		`UPDATE customer_quote SET valid_until = now() - interval '1 hour', share_expires_at = now() + interval '1 day' WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("poke: %v", err)
	}
	rec := b.publicView(t, sent.MagicLinkToken)
	assertStatus(t, "offer-expired view", rec, http.StatusOK)
	var view PublicView
	decode(t, rec, &view)
	if !view.Expired || view.Acceptable {
		t.Fatalf("offer-expired view: expired %v acceptable %v", view.Expired, view.Acceptable)
	}
	rec = b.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true)
	e := assertStatus(t, "offer-expired accept", rec, http.StatusConflict)
	if e.Error != SentenceOfferExpired {
		t.Fatalf("offer-expired accept body %q", e.Error)
	}

	// A tombstoned quote: all its tokens answer 410 and nothing renders.
	if _, err := b.pool.Exec(ctx,
		`UPDATE customer_quote SET deleted_at = now() WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("poke: %v", err)
	}
	if rec := b.publicView(t, sent.MagicLinkToken); rec.Code != http.StatusGone {
		t.Fatalf("tombstoned view: %d, want 410", rec.Code)
	}
	if _, err := b.pool.Exec(ctx, `UPDATE customer_quote SET deleted_at = NULL WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("poke: %v", err)
	}

	// A live hash with a NULL link bound: fail-closed 410, counted loudly.
	if _, err := b.pool.Exec(ctx,
		`UPDATE customer_quote SET share_expires_at = NULL WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("poke: %v", err)
	}
	if rec := b.publicView(t, sent.MagicLinkToken); rec.Code != http.StatusGone {
		t.Fatalf("null-bound view: %d, want 410 fail closed", rec.Code)
	}

	// Multi-match: one presented hash matching two rows is a data defect
	// refused closed in the 503 family.
	other := b.createQuote(t, fixtureCreate(horizon))
	b.sendQuote(t, other.ID)
	stored := b.scalarStr(t, `SELECT share_token_hash FROM customer_quote WHERE id = $1`, created.ID)
	if _, err := b.pool.Exec(ctx,
		`UPDATE customer_quote SET share_token_hash = $2, share_expires_at = now() + interval '1 day' WHERE id = $1`,
		other.ID, stored); err != nil {
		t.Fatalf("poke: %v", err)
	}
	if rec := b.publicView(t, sent.MagicLinkToken); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("multi-match view: %d, want 503 refused closed", rec.Code)
	}
	if _, err := b.pool.Exec(ctx,
		`UPDATE customer_quote SET share_token_hash = NULL WHERE id = $1`, other.ID); err != nil {
		t.Fatalf("poke: %v", err)
	}

	// An unparsable stored value matches nothing even when the hash is a
	// substring of the corrupted content: 404, and the defect is counted.
	doubled := HashToken(sent.MagicLinkToken) + HashToken(sent.MagicLinkToken)
	if _, err := b.pool.Exec(ctx,
		`UPDATE customer_quote SET share_token_hash = $2 WHERE id = $1`, created.ID, doubled); err != nil {
		t.Fatalf("poke: %v", err)
	}
	unparsableBefore, _, _, _ := b.svc.Defects().Counts()
	if rec := b.publicView(t, sent.MagicLinkToken); rec.Code != http.StatusNotFound {
		t.Fatalf("unparsable stored value: %d, want 404 (parse before match)", rec.Code)
	}
	unparsableAfter, _, _, _ := b.svc.Defects().Counts()
	if unparsableAfter <= unparsableBefore {
		t.Fatalf("the unparsable stored value was not counted loudly")
	}
}

// S2: cross-quote token use. Every resolution of A's token names A only; B's
// row, status, counters and acceptance rows are untouched; a body field
// naming another quote neither widens nor narrows.
func TestS2CrossQuoteTokenUse(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	a := b.createQuote(t, fixtureCreate(horizon))
	sentA := b.sendQuote(t, a.ID)

	// Quote B under a different tenant (beta).
	in := fixtureCreate(horizon)
	in.Customer = CustomerContact{Name: "Fixture Homeowner Beta"}
	quoteB := b.createQuoteAs(t, benchTenantBeta, in)
	b.sendQuote(t, quoteB.ID)

	// A's token resolves A only.
	rec := b.publicView(t, sentA.MagicLinkToken)
	assertStatus(t, "A token view", rec, http.StatusOK)
	var view PublicView
	decode(t, rec, &view)
	if view.QuoteID != a.ID || view.CustomerName != "Fixture Homeowner One" {
		t.Fatalf("A's token served the wrong quote: %s / %s", view.QuoteID, view.CustomerName)
	}
	if strings.Contains(rec.Body.String(), quoteB.ID) {
		t.Fatalf("A's token view leaks B's quote id")
	}

	// A's accept records against A; B is untouched. A body field naming
	// another quote neither widens nor narrows: the token alone selects
	// the row, and unknown body fields are ignored by the decoder.
	rec = b.publicAccept(t, sentA.MagicLinkToken, "Fixture Homeowner One", true)
	assertStatus(t, "A accept", rec, http.StatusOK)
	var result AcceptanceResult
	decode(t, rec, &result)
	if result.QuoteID != a.ID {
		t.Fatalf("A's acceptance recorded against %s", result.QuoteID)
	}
	a2 := b.createQuote(t, fixtureCreate(horizon))
	sentA2 := b.sendQuote(t, a2.ID)
	acceptNamingB := doReq(t, b.mux, http.MethodPost, b.acceptURL(sentA2.MagicLinkToken), "", nil,
		map[string]any{"typed_name": "Fixture Homeowner Two", "accepted": true,
			"quote_id": quoteB.ID, "customer_quote_id": quoteB.ID})
	assertStatus(t, "accept naming B in the body", acceptNamingB, http.StatusOK)
	var resultA2 AcceptanceResult
	decode(t, acceptNamingB, &resultA2)
	if resultA2.QuoteID != a2.ID {
		t.Fatalf("a body naming B moved the acceptance to %s; the token alone must select", resultA2.QuoteID)
	}
	if strings.Contains(acceptNamingB.Body.String(), quoteB.ID) {
		t.Fatalf("the acceptance result echoes B's quote id")
	}
	if got := b.scalarInt(t, `SELECT count(*) FROM quote_acceptance WHERE customer_quote_id = $1`, quoteB.ID); got != 0 {
		t.Fatalf("B gained %d acceptance rows from A's token", got)
	}
	if status := b.scalarStr(t, `SELECT status FROM customer_quote WHERE id = $1`, quoteB.ID); status != string(StatusSent) {
		t.Fatalf("B's status moved to %s", status)
	}
	if got := b.scalarInt(t, `SELECT view_count FROM customer_quote WHERE id = $1`, quoteB.ID); got != 0 {
		t.Fatalf("B's view counter moved")
	}
	_ = ctx
}

// The seq allocation (DESIGN 5.6, the review's R2): the creation write draws
// the quote's first seq under the customer_quote scope, the acceptance
// transaction draws the acceptance row's seq under quote_acceptance and a
// fresh seq for the flip under customer_quote, all through the
// scope_sequences upsert on the write's own transaction, so a rolled-back
// acceptance leaves the counters exactly where they were.
func TestSeqAllocationOnTheAcceptanceTransaction(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)

	counter := func(scope string) int64 {
		var next int64
		if err := b.pool.QueryRow(ctx,
			`SELECT next_value FROM scope_sequences WHERE scope = $1`, scope).Scan(&next); err != nil {
			t.Fatalf("counter for scope %s: %v", scope, err)
		}
		return next
	}

	// The creation write draws under customer_quote, monotonically per scope.
	created := b.createQuote(t, fixtureCreate(horizon))
	createSeq := b.scalarInt(t, `SELECT seq FROM customer_quote WHERE id = $1`, created.ID)
	other := b.createQuote(t, fixtureCreate(horizon))
	otherCreateSeq := b.scalarInt(t, `SELECT seq FROM customer_quote WHERE id = $1`, other.ID)
	if createSeq < 1 || otherCreateSeq <= createSeq {
		t.Fatalf("creation seq draws %d then %d, want monotonically increasing values from 1", createSeq, otherCreateSeq)
	}
	sent := b.sendQuote(t, created.ID)
	acceptBefore := counter(ScopeQuoteAcceptance)

	// A rolled-back acceptance does not consume a value: the allocation
	// rides the acceptance transaction and rolls back with it.
	b.seam.FailNext()
	rec := b.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true)
	assertStatus(t, "accept with a failing audit seam", rec, http.StatusServiceUnavailable)
	if got := counter(ScopeQuoteAcceptance); got != acceptBefore {
		t.Fatalf("a rolled-back acceptance moved the quote_acceptance counter from %d to %d; the allocation must ride the transaction", acceptBefore, got)
	}

	// The green acceptance: the acceptance row draws under quote_acceptance,
	// the flip re-tails the quote's seq under customer_quote.
	rec = b.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true)
	assertStatus(t, "accept", rec, http.StatusOK)
	var result AcceptanceResult
	decode(t, rec, &result)
	acceptanceSeq := b.scalarInt(t, `SELECT seq FROM quote_acceptance WHERE id = $1`, result.AcceptanceID)
	flipSeq := b.scalarInt(t, `SELECT seq FROM customer_quote WHERE id = $1`, created.ID)
	if acceptanceSeq < 1 {
		t.Fatalf("the acceptance row carries seq %d, want a drawn value >= 1", acceptanceSeq)
	}
	if int64(flipSeq) <= int64(createSeq) {
		t.Fatalf("the flip did not re-tail the quote's seq: create %d, after flip %d", createSeq, flipSeq)
	}
	if after := counter(ScopeQuoteAcceptance); after <= acceptBefore {
		t.Fatalf("the quote_acceptance counter did not advance: %d then %d", acceptBefore, after)
	}
}

// S5: the artifact verification leg. Every single-field mutation of the
// stored row fails verification; a mutated audit signature fails; a deleted
// audit row fails closed; a signature made under a previous key verifies
// while the previous key is configured and fails naming the key id once it
// is withdrawn.
func TestS5ArtifactVerificationLeg(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)

	// greenAcceptance runs one full green flow and returns the acceptance id.
	greenAcceptance := func() string {
		created := b.createQuote(t, fixtureCreate(horizon))
		sent := b.sendQuote(t, created.ID)
		rec := b.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true)
		assertStatus(t, "accept", rec, http.StatusOK)
		var result AcceptanceResult
		decode(t, rec, &result)
		return result.AcceptanceID
	}

	id := greenAcceptance()
	v, err := b.svc.VerifyAcceptance(ctx, id)
	if err != nil || !v.OK {
		t.Fatalf("green acceptance must verify: %+v %v", v, err)
	}

	// The fixture attachment row for the signature-attachment mutation.
	if _, err := b.pool.Exec(ctx,
		`INSERT INTO attachment (id, org_id, tenant_id, kind, appwrite_file_id, bucket, mime)
		 VALUES ('3ef10000-0000-4000-8000-00000000e0f1', $1, $2, 'signature', 'FXFILE', 'FXBUCKET', 'image/png')
		 ON CONFLICT (id) DO NOTHING`,
		testkit.OrgNorthID, testkit.TenantAlphaID); err != nil {
		t.Fatalf("attachment fixture: %v", err)
	}

	// Each mutation runs against a fresh acceptance, one at a time.
	mutations := []struct {
		name string
		sql  string
	}{
		{"signed_name", `UPDATE quote_acceptance SET signed_name = 'Mutated Name' WHERE id = $1`},
		{"accepted_total_cents", `UPDATE quote_acceptance SET accepted_total_cents = accepted_total_cents + 1 WHERE id = $1`},
		{"consent_text", `UPDATE quote_acceptance SET consent_text = 'mutated consent' WHERE id = $1`},
		{"accepted_at", `UPDATE quote_acceptance SET accepted_at = accepted_at + interval '1 microsecond' WHERE id = $1`},
		{"affirmative", `UPDATE quote_acceptance SET affirmative = FALSE WHERE id = $1`},
		{"signature_attachment_id", `UPDATE quote_acceptance SET signature_attachment_id = '3ef10000-0000-4000-8000-00000000e0f1'::uuid WHERE id = $1`},
	}
	for _, m := range mutations {
		id := greenAcceptance()
		if _, err := b.pool.Exec(ctx, m.sql, id); err != nil {
			t.Fatalf("mutation %s: %v", m.name, err)
		}
		v, err := b.svc.VerifyAcceptance(ctx, id)
		if err != nil {
			t.Fatalf("mutation %s: verification errored rather than failing closed: %v", m.name, err)
		}
		if v.OK {
			t.Fatalf("mutation %s survived verification", m.name)
		}
	}

	// The quote-binding mutation swaps the acceptance onto another quote.
	id = greenAcceptance()
	if _, err := b.pool.Exec(ctx,
		`UPDATE quote_acceptance SET customer_quote_id = (
		   SELECT c.id FROM customer_quote c WHERE c.id <> quote_acceptance.customer_quote_id LIMIT 1)
		 WHERE id = $1`, id); err != nil {
		t.Fatalf("quote binding mutation: %v", err)
	}
	if v, _ := b.svc.VerifyAcceptance(ctx, id); v.OK {
		t.Fatalf("a swapped quote binding survived verification")
	}

	// A mutated audit-row signature fails.
	id = greenAcceptance()
	if _, err := b.pool.Exec(ctx,
		`UPDATE audit_log SET changes = changes || jsonb_build_object('artifact_sig', '0000')
		 WHERE entity_type = 'quote_acceptance' AND entity_id = $1::uuid`, id); err != nil {
		t.Fatalf("audit signature mutation: %v", err)
	}
	if v, _ := b.svc.VerifyAcceptance(ctx, id); v.OK {
		t.Fatalf("a mutated audit signature survived verification")
	}

	// A deleted audit row fails verification closed (the seam's linkage rule:
	// zero rows is ambiguity).
	id = greenAcceptance()
	if _, err := b.pool.Exec(ctx,
		`DELETE FROM audit_log WHERE entity_type = 'quote_acceptance' AND entity_id = $1::uuid`, id); err != nil {
		t.Fatalf("audit deletion: %v", err)
	}
	v, err = b.svc.VerifyAcceptance(ctx, id)
	if err != nil {
		t.Fatalf("deleted audit row errored rather than failing closed: %v", err)
	}
	if v.OK || !strings.Contains(v.Reason, "audit row") {
		t.Fatalf("deleted audit row must fail closed naming the audit row: %+v", v)
	}

	// A signature made under a previous key: verifies while the previous key
	// is configured; fails naming the key id once withdrawn.
	id = greenAcceptance()
	store, err := NewStore(b.pool)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	rotated, err := NewService(store, b.seam, &SigningKeys{
		KeyID: "fx-accept-next", Key: bytes.Repeat([]byte{0x77}, 32),
		PreviousKeyID: "fx-accept-key", PreviousKey: bytes.Repeat([]byte{0x51}, 32),
	}, Config{Now: b.kit.Clock.Now})
	if err != nil {
		t.Fatalf("rotated service: %v", err)
	}
	if v, err := rotated.VerifyAcceptance(ctx, id); err != nil || !v.OK {
		t.Fatalf("previous-key signature must verify during rotation: %+v %v", v, err)
	}
	withdrawn, err := NewService(store, b.seam, &SigningKeys{
		KeyID: "fx-accept-next", Key: bytes.Repeat([]byte{0x77}, 32),
	}, Config{Now: b.kit.Clock.Now})
	if err != nil {
		t.Fatalf("withdrawn service: %v", err)
	}
	v, err = withdrawn.VerifyAcceptance(ctx, id)
	if err != nil || v.OK || !strings.Contains(v.Reason, "key id") {
		t.Fatalf("a withdrawn key must fail naming the key id: %+v %v", v, err)
	}
}

// The mandatory audit write (F-5): a failed audit write rolls the whole
// acceptance back, so the status flip, the acceptance row and the audit row
// are all present or all absent (the M1d proof, and the divergence from the
// degrading dealer precedent).
func TestAcceptanceRollsBackWhenAuditWriteFails(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := b.createQuote(t, fixtureCreate(horizon))
	sent := b.sendQuote(t, created.ID)

	b.seam.FailNext()
	rec := b.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true)
	assertStatus(t, "accept with a failing audit seam", rec, http.StatusServiceUnavailable)
	if got := b.scalarInt(t, `SELECT count(*) FROM quote_acceptance WHERE customer_quote_id = $1`, created.ID); got != 0 {
		t.Fatalf("the acceptance row survived its audit write failing (%d rows)", got)
	}
	if status := b.scalarStr(t, `SELECT status FROM customer_quote WHERE id = $1`, created.ID); status != string(StatusSent) {
		t.Fatalf("the status flip survived its audit write failing: %s", status)
	}

	// With the seam healthy again the same acceptance proceeds and verifies.
	rec = b.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true)
	assertStatus(t, "accept after recovery", rec, http.StatusOK)
	var result AcceptanceResult
	decode(t, rec, &result)
	if v, err := b.svc.VerifyAcceptance(ctx, result.AcceptanceID); err != nil || !v.OK {
		t.Fatalf("recovered acceptance must verify: %+v %v", v, err)
	}
}
