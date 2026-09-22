package customerquote

// The tenant-side lifecycle: the carried rules R2 to R8 with their ported
// sentences, the ported HH Pro regressions (freeze at send, rotation on
// revise, accepted never revisable), and the R8 change-request recording at
// the service seam. The tenant operations carry no route (the document that
// landed carries the send as a dealer desk action and no tenant
// customer-quote operations at all, ADR 0021 Decision 2), so they are
// driven here through their service seams with the refusal sentences
// asserted at full strength through refuseAs; the send gates run through
// the REGISTERED staff route, which shares the one send lifecycle.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func unpricedLine(name string) Line {
	return Line{Name: name, Qty: "1", UOM: "EA", UnitCostCents: 0,
		PriceSource: PriceSourceUnpriced, Presentation: PresentationCommodity}
}

func TestR2UnpricedLinesRefuseAtBuild(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	base := fixtureCreate(horizon)

	one := base
	one.Lines = append(fixtureLines(horizon), unpricedLine("Fixture Rail Kit"))
	_, err := b.svc.Create(ctx, benchTenantAlpha, one)
	refuseAs(t, "one unpriced line", err, http.StatusBadRequest, RefuseUnpriced([]string{"Fixture Rail Kit"}))

	three := base
	three.Lines = append(fixtureLines(horizon), unpricedLine("Fixture A"), unpricedLine("Fixture B"), unpricedLine("Fixture C"))
	_, err = b.svc.Create(ctx, benchTenantAlpha, three)
	refuseAs(t, "three unpriced lines", err, http.StatusBadRequest, RefuseUnpriced([]string{"Fixture A", "Fixture B", "Fixture C"}))

	// A lapsed price counts as unpriced (R2's priced-and-lapsed case).
	lapsed := b.kit.Clock.Now().Add(-time.Hour)
	two := base
	two.Lines = append(fixtureLines(horizon), Line{Name: "Fixture Lapsed", Qty: "1", UOM: "EA",
		UnitCostCents: 500, PriceSource: PriceSourceQuoted, PriceValidUntil: &lapsed,
		Presentation: PresentationCommodity})
	_, err = b.svc.Create(ctx, benchTenantAlpha, two)
	refuseAs(t, "lapsed line", err, http.StatusBadRequest, RefuseUnpriced([]string{"Fixture Lapsed"}))
}

func TestR3ExpiryCapsAtBuildAndUpdate(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	now := b.kit.Clock.Now()
	horizon := now.Add(2 * 24 * time.Hour)
	requested := now.Add(30 * 24 * time.Hour)
	in := fixtureCreate(horizon)
	in.ValidUntil = &requested
	created := b.createQuote(t, in)
	if created.ValidUntil == nil || !withinMillis(created.ValidUntil, horizon) {
		t.Fatalf("the offer bound must cap at the supplier horizon: %v", created.ValidUntil)
	}
	// Re-applied on update: requesting later never escapes the horizon.
	updated, err := b.svc.Update(ctx, benchTenantAlpha, created.ID, UpdateInput{ValidUntil: &requested})
	if err != nil {
		t.Fatalf("update with a later bound: %v", err)
	}
	if updated.ValidUntil == nil || !withinMillis(updated.ValidUntil, horizon) {
		t.Fatalf("the update re-applied the cap: %v", updated.ValidUntil)
	}
	if updated.SupplierHorizon == nil || !withinMillis(updated.SupplierHorizon, horizon) {
		t.Fatalf("the horizon is stored: %v", updated.SupplierHorizon)
	}
}

func TestR5EditRulesAndPortedSentences(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := b.createQuote(t, fixtureCreate(horizon))
	b.sendQuote(t, created.ID)

	// The already-sent refusal names the revise remedy, with the sentence
	// ported with normalized punctuation.
	_, err := b.svc.Update(ctx, benchTenantAlpha, created.ID, UpdateInput{MarkupBps: ptrInt(4500)})
	refuseAs(t, "update after send", err, http.StatusConflict, sentUpdateSentence(created.ID))

	// Revise reopens editing; the ported regression: the live link's numbers
	// must not move without it.
	b.reviseQuote(t, created.ID)
	if _, err := b.svc.Update(ctx, benchTenantAlpha, created.ID, UpdateInput{MarkupBps: ptrInt(3000)}); err != nil {
		t.Fatalf("update after revise: %v", err)
	}

	// An accepted quote refuses absolutely: no edit, no revise.
	resend := b.sendQuote(t, created.ID)
	rec := b.publicAccept(t, resend.MagicLinkToken, "Fixture Homeowner One", true)
	assertStatus(t, "accept", rec, http.StatusOK)
	_, err = b.svc.Update(ctx, benchTenantAlpha, created.ID, UpdateInput{MarkupBps: ptrInt(5000)})
	refuseAs(t, "update after accept", err, http.StatusConflict, SentenceAcceptedNoChange)
	_, err = b.svc.Revise(ctx, benchTenantAlpha, created.ID)
	refuseAs(t, "revise after accept", err, http.StatusConflict, SentenceAcceptedNoRevise)
}

func sentUpdateSentence(id string) string {
	return fmt.Sprintf(SentenceSentUpdate, id)
}

func ptrInt(v int64) *int64 { return &v }

func ptrTime(v time.Time) *time.Time { return &v }

// staffSendReq drives the registered send route and returns the recorder
// without asserting the status, for the gate legs that expect refusals.
func (b *bench) staffSendReq(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	return doReq(t, b.mux, http.MethodPost, "/api/v1/customer-quotes/"+id+"/send",
		b.kit.Minter.StaffToken(t, testkit.SubInsideSales), staffHeaders(testkit.OrgNorthSlug), struct{}{})
}

func TestR6SendGates(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)

	// No customer name: refuse.
	in := fixtureCreate(horizon)
	in.Customer = CustomerContact{Name: "  "}
	created := b.createQuote(t, in)
	rec := b.staffSendReq(t, created.ID)
	e := assertStatus(t, "send without a name", rec, http.StatusBadRequest)
	if e.Error != SentenceNeedCustomerName {
		t.Fatalf("no-name refusal %q", e.Error)
	}
	// With the name patched in, the send proceeds (the control leg).
	if _, err := b.svc.Update(ctx, benchTenantAlpha, created.ID,
		UpdateInput{Customer: &CustomerContact{Name: "Fixture Homeowner One"}}); err != nil {
		t.Fatalf("patch the customer name: %v", err)
	}
	assertStatus(t, "send with a name", b.staffSendReq(t, created.ID), http.StatusOK)

	// Unpriced lines refuse the send: a draft whose stored line lapses
	// between build and send (R2 re-checked at the gate).
	draft := b.createQuote(t, fixtureCreate(horizon))
	lapsedLines := fixtureLines(horizon)
	lapsedLines[1].PriceValidUntil = ptrTime(b.kit.Clock.Now().Add(-time.Hour))
	if _, err := b.pool.Exec(ctx,
		`UPDATE customer_quote SET lines = $2 WHERE id = $1`, draft.ID, mustJSON(lapsedLines)); err != nil {
		t.Fatalf("poke lines: %v", err)
	}
	rec = b.staffSendReq(t, draft.ID)
	e = assertStatus(t, "send over a lapsed line", rec, http.StatusBadRequest)
	if e.Error != RefuseUnpriced([]string{lapsedLines[1].Name}) {
		t.Fatalf("lapsed-at-send refusal %q", e.Error)
	}

	// An expired offer refuses the send.
	past := b.kit.Clock.Now().Add(-time.Hour)
	expiredIn := fixtureCreate(horizon)
	expiredIn.ValidUntil = &past
	expired := b.createQuote(t, expiredIn)
	rec = b.staffSendReq(t, expired.ID)
	e = assertStatus(t, "send with an expired offer", rec, http.StatusConflict)
	if e.Error != SentenceAlreadyExpiredSend {
		t.Fatalf("expired-send refusal %q", e.Error)
	}

	// An accepted quote refuses the send.
	sent := b.sendQuote(t, b.createQuote(t, fixtureCreate(horizon)).ID)
	b.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true)
	rec = b.staffSendReq(t, sent.ID)
	e = assertStatus(t, "send after accept", rec, http.StatusConflict)
	if e.Error != SentenceAlreadyAccepted {
		t.Fatalf("accepted-send refusal %q", e.Error)
	}
}

// withinMillis compares two instants across the database's microsecond
// truncation and the JSON round trip.
func withinMillis(got *time.Time, want time.Time) bool {
	if got == nil {
		return false
	}
	d := got.Sub(want)
	if d < 0 {
		d = -d
	}
	return d < time.Millisecond
}

func TestR4FreezeAndCounterReset(t *testing.T) {
	b := benchFor(t)
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := b.createQuote(t, fixtureCreate(horizon))
	sent := b.sendQuote(t, created.ID)

	// Two views: the counters advance, sent advances to viewed.
	b.publicView(t, sent.MagicLinkToken)
	b.publicView(t, sent.MagicLinkToken)
	if got := b.scalarInt(t, `SELECT view_count FROM customer_quote WHERE id = $1`, created.ID); got != 2 {
		t.Fatalf("view count %d, want 2", got)
	}
	if got := b.scalarInt(t, `SELECT count(*) FROM customer_quote WHERE id = $1 AND first_viewed_at IS NOT NULL`, created.ID); got != 1 {
		t.Fatalf("first_viewed_at did not land")
	}
	if status := b.scalarStr(t, `SELECT status FROM customer_quote WHERE id = $1`, created.ID); status != string(StatusViewed) {
		t.Fatalf("status %s, want viewed", status)
	}

	// The frozen payload survives a revise and re-send unchanged in content,
	// and the counters reset at the re-send.
	frozen := b.scalarStr(t, `SELECT lines::text FROM customer_quote WHERE id = $1`, created.ID)
	b.reviseQuote(t, created.ID)
	resend := b.sendQuote(t, created.ID)
	if got := b.scalarInt(t, `SELECT view_count FROM customer_quote WHERE id = $1`, created.ID); got != 0 {
		t.Fatalf("view count after re-send %d, want 0", got)
	}
	if got := b.scalarInt(t, `SELECT count(*) FROM customer_quote WHERE id = $1 AND first_viewed_at IS NULL`, created.ID); got != 1 {
		t.Fatalf("first_viewed_at did not reset at re-send")
	}
	if refrozen := b.scalarStr(t, `SELECT lines::text FROM customer_quote WHERE id = $1`, created.ID); refrozen != frozen {
		t.Fatalf("the frozen presentation lines changed across a revise and re-send")
	}
	_ = resend
}

func TestR8ChangeRequestKeepsOfferRespondable(t *testing.T) {
	b := benchFor(t)
	ctx := context.Background()
	horizon := b.kit.Clock.Now().Add(7 * 24 * time.Hour)
	created := b.createQuote(t, fixtureCreate(horizon))
	sent := b.sendQuote(t, created.ID)

	// A change request through the token: the request records, the status
	// moves, and the offer remains respondable (the service seam; the public
	// route spelling is the document's).
	view, err := b.svc.RecordChangeRequest(ctx, sent.MagicLinkToken, "Fixture change note: move the gate")
	if err != nil {
		t.Fatalf("change request: %v", err)
	}
	_ = view
	if status := b.scalarStr(t, `SELECT status FROM customer_quote WHERE id = $1`, created.ID); status != string(StatusChangesRequested) {
		t.Fatalf("status %s, want changes-requested", status)
	}
	stored := b.scalarStr(t, `SELECT change_requests::text FROM customer_quote WHERE id = $1`, created.ID)
	if !strings.Contains(stored, "Fixture change note: move the gate") {
		t.Fatalf("the change request did not record: %s", stored)
	}
	// The offer remains respondable: accept succeeds after the change request.
	rec := b.publicAccept(t, sent.MagicLinkToken, "Fixture Homeowner One", true)
	assertStatus(t, "accept after a change request", rec, http.StatusOK)
}
