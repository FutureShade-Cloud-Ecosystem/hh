package billing

// credit_memo_key_seed_test.go: the derived idempotency key keeps its pre JA-2 shape for a jobless
// credit (the delta read of PR 747, P3-1). A credit asked just before a deploy and retried just
// after it, on the same house day, must derive the SAME key, or the retry writes a second credit.

import (
	"context"
	"testing"
	"time"
)

func TestCreditMemoKey_AJobelessCreditKeepsItsPreJA2Key(t *testing.T) {
	s := &CreditMemoService{w: &InvoiceWriter{}, now: func() time.Time {
		return time.Date(2026, 3, 5, 15, 0, 0, 0, time.UTC)
	}}
	req := CreditMemoRouteRequest{
		BranchID: "b1", CustomerID: "c1", InvoiceID: " i1 ", AmountCents: 5_000,
	}
	// The literal is the sha256 of the seed the code derived before JA-2 added the job:
	// "2026-03-05|b1|c1|i1|5000|Goodwill", with the "credit-memo:" prefix.
	const want = "credit-memo:df70a2a6ee8f8792a0f344da3af68d8976dbf3c198d5c5bec3b3c0f04b66bd0f"
	if got := s.creditMemoKey(context.Background(), req, "Goodwill", ""); got != want {
		t.Fatalf("a jobless credit derives %q, want the pre JA-2 key %q", got, want)
	}
}

func TestCreditMemoKey_AJobIsPartOfTheKey(t *testing.T) {
	s := &CreditMemoService{w: &InvoiceWriter{}, now: func() time.Time {
		return time.Date(2026, 3, 5, 15, 0, 0, 0, time.UTC)
	}}
	req := CreditMemoRouteRequest{BranchID: "b1", CustomerID: "c1", AmountCents: 5_000}
	none := s.creditMemoKey(context.Background(), req, "Goodwill", "")
	a := s.creditMemoKey(context.Background(), req, "Goodwill", "job-a")
	b := s.creditMemoKey(context.Background(), req, "Goodwill", "job-b")
	if a == b || a == none || b == none {
		t.Fatalf("keys must differ by job: none=%q a=%q b=%q", none, a, b)
	}
	if again := s.creditMemoKey(context.Background(), req, "Goodwill", "job-a"); again != a {
		t.Fatalf("the same job derives two keys: %q and %q", a, again)
	}
}
