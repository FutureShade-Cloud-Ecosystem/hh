package gl

// post_external_guard_test.go, DIB-189 defense-in-depth at the GL posting seam. A journal can net to
// Σdebit==Σcredit while carrying a NEGATIVE-amount leg (e.g. landed-cost over-absorption booking COGS as −9
// against a +2709 inventory debit and a 2700 credit). balanced() sums the debit/credit columns unsigned, so it
// is fooled; only gl_journal_lines_check (debit_cents > 0) catches it, as an opaque DB error, deep inside the
// capitalize tx. postExternal now rejects any negative leg BEFORE the DB does, surfacing errUnbalanced in Go.

import (
	"context"
	"errors"
	"testing"
)

func TestPostExternal_RejectsNegativeLeg(t *testing.T) {
	ctx := context.Background()

	// The exact DIB-189 shape: DR 1200 = 2709, DR 5000 = −9, CR 2100 = 2700. Column sums balance
	// (2709 + (−9) = 2700 debit; 2700 credit) but leg 2 is a negative debit the DB constraint forbids.
	negLegJournal := ExternalJournal{
		Source:         SourceAdjustment,
		IdempotencyKey: "dib189.negleg",
		Memo:           "over-absorption negative COGS leg",
		Lines: []ExternalLine{
			{AccountCode: "1200", DebitCents: 2709},
			{AccountCode: "5000", DebitCents: -9},
			{AccountCode: "2100", CreditCents: 2700},
		},
	}

	f := newFakeStore()
	svc := newTestService(f)
	if _, err := svc.PostExternal(ctx, negLegJournal); !errors.Is(err, errUnbalanced) {
		t.Fatalf("negative-debit leg: want errUnbalanced, got %v", err)
	}

	// A negative CREDIT is rejected the same way.
	negCredit := negLegJournal
	negCredit.IdempotencyKey = "dib189.negcredit"
	negCredit.Lines = []ExternalLine{
		{AccountCode: "1200", DebitCents: 2691},
		{AccountCode: "2100", CreditCents: 2700},
		{AccountCode: "2100", CreditCents: -9},
	}
	if _, err := svc.PostExternal(ctx, negCredit); !errors.Is(err, errUnbalanced) {
		t.Fatalf("negative-credit leg: want errUnbalanced, got %v", err)
	}

	// A clean, all-positive balanced external journal still posts (the guard changes no correct behavior).
	ok := ExternalJournal{
		Source:         SourceAdjustment,
		IdempotencyKey: "dib189.clean",
		Memo:           "clean capitalize",
		Lines: []ExternalLine{
			{AccountCode: "1200", DebitCents: 2700},
			{AccountCode: "2100", CreditCents: 2700},
		},
	}
	if _, err := svc.PostExternal(ctx, ok); err != nil {
		t.Fatalf("clean balanced journal must post, got %v", err)
	}
}
