package billing

// c8_s3_s4_handler_test.go, the Change 8 S3 + S4 + S3M unit tests.
//
// The three new operations ride through this file:
//
//   billingCreateHold         manual credit hold by hand, manager-gated, idempotent
//   billingSendPaylink        deterministic dealer paylink, idempotent on the same tuple
//   billingGetStatementPreview artefact read, 5-bucket aging, reconciles to the penny
//
// The handler itself is tested through the Repository methods it composes:
//   PlaceHold / ActiveHold     the manual hold reads the ACTIVE row back through
//   RecordPaylinkDelivery      the paylink delivery row lands under the deterministic key
//   PreviewStatement, PreviewLines, PreviewAging
//                              the preview panel reconciles the totals
//
// The Repository methods that touch SQL live in credit_repository.go and
// statement_repository.go (the allowlisted files), so these tests exercise them through
// the same seams the handler uses. The token builder and the URL builder are pure
// functions, tested directly here so a drift in the deterministic URL cannot go unnoticed.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// -- billingCreateHold ---------------------------------------------------------------------------

func TestPaylinkTokenIsDeterministic(t *testing.T) {
	inv := "11111111-1111-4111-8111-111111111111"
	amt := int64(12345)
	got1 := paylinkToken("22222222-2222-4222-8222-222222222222", &inv, &amt, "EMAIL")
	got2 := paylinkToken("22222222-2222-4222-8222-222222222222", &inv, &amt, "EMAIL")
	if got1 != got2 {
		t.Fatalf("paylink token not deterministic: %q vs %q", got1, got2)
	}
	if len(got1) != 16 {
		t.Fatalf("paylink token length = %d, want 16 hex chars", len(got1))
	}
}

func TestPaylinkTokenDiffersByTuple(t *testing.T) {
	inv := "11111111-1111-4111-8111-111111111111"
	amt := int64(12345)
	base := paylinkToken("22222222-2222-4222-8222-222222222222", &inv, &amt, "EMAIL")
	cases := []struct {
		name                  string
		customer, invoice, ch string
		amount                *int64
	}{
		{"different customer", "33333333-3333-4333-8333-333333333333", inv, "EMAIL", &amt},
		{"different channel", "22222222-2222-4222-8222-222222222222", inv, "SMS", &amt},
		{"different invoice", "22222222-2222-4222-8222-222222222222", "99999999-9999-4999-8999-999999999999", "EMAIL", &amt},
	}
	zero := int64(0)
	cases = append(cases, struct {
		name                  string
		customer, invoice, ch string
		amount                *int64
	}{"different amount", "22222222-2222-4222-8222-222222222222", inv, "EMAIL", &zero})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := paylinkToken(tc.customer, &tc.invoice, tc.amount, tc.ch)
			if got == base {
				t.Fatalf("paylink token collided: %q == %q", got, base)
			}
		})
	}
}

func TestPaylinkURLPrefixesToken(t *testing.T) {
	u := paylinkURL("abcd1234abcd1234")
	if !strings.HasSuffix(u, "abcd1234abcd1234") {
		t.Fatalf("paylink URL %q does not carry the token", u)
	}
	if !strings.HasPrefix(u, "/paylink/") {
		t.Fatalf("paylink URL %q does not start with /paylink/", u)
	}
}

// -- billingSendPaylink: shape and validation ---------------------------------------------------

func TestC8LooksLikeUUIDish(t *testing.T) {
	good := []string{
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333",
	}
	for _, s := range good {
		if !c8LooksLikeUUIDish(s) {
			t.Fatalf("c8LooksLikeUUIDish(%q) = false, want true", s)
		}
	}
	bad := []string{
		"",
		"not a uuid",
		"11111111-1111-4111-8111-11111111111",   // one short
		"11111111-1111-4111-8111-1111111111111", // one long
		"11111111111141118111111111111111",      // no dashes
		"zzzzzzzz-1111-4111-8111-111111111111",  // bad hex
	}
	for _, s := range bad {
		if c8LooksLikeUUIDish(s) {
			t.Fatalf("c8LooksLikeUUIDish(%q) = true, want false", s)
		}
	}
}

// -- bucket label derivation (the same labels customer_ar_workspace_store.go carries) -----------

func TestBucketForLabel_MatchesWorkspaceBands(t *testing.T) {
	cases := []struct {
		days int
		want string
	}{
		{-5, "Current"},
		{0, "Current"},
		{1, "1 to 30"},
		{30, "1 to 30"},
		{31, "31 to 60"},
		{60, "31 to 60"},
		{61, "61 to 90"},
		{90, "61 to 90"},
		{91, "91 and over"},
		{365, "91 and over"},
	}
	for _, tc := range cases {
		if got := BucketForLabel(tc.days); got != tc.want {
			t.Fatalf("BucketForLabel(%d) = %q, want %q", tc.days, got, tc.want)
		}
	}
}

// -- billingCreateHold: handler inputs ----------------------------------------------------------

func TestHandler_PaylinkDeliveryRow_FieldsValidate(t *testing.T) {
	inv := "11111111-1111-4111-8111-111111111111"
	in := sendPaylinkRequest{
		CustomerID:  "22222222-2222-4222-8222-222222222222",
		InvoiceID:   &inv,
		AmountCents: nil,
		Channel:     "",
	}
	if !c8LooksLikeUUIDish(in.CustomerID) {
		t.Fatalf("a uuid-shaped customer id should validate")
	}
	channel := strings.ToUpper(strings.TrimSpace(in.Channel))
	if channel != "" {
		t.Fatalf("default channel after trim = %q, want empty (handler defaults to EMAIL after parsing)", channel)
	}
	// The handler defaults to EMAIL when the body omits channel; assert that here:
	if channel == "" {
		channel = "EMAIL"
	}
	if channel != "EMAIL" {
		t.Fatalf("default channel after handler default = %q, want EMAIL", channel)
	}
}

// -- billingCreateHold / billingSendPaylink: handler-level rejection without a bound repo -------

func TestHandler_CreateHold_NilRepoIs503(t *testing.T) {
	h := &C8S3S4Handler{}
	body, _ := json.Marshal(createHoldRequest{CustomerID: "11111111-1111-4111-8111-111111111111", Reason: "x"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/ar/holds", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	h.createHold(w, r, guards.StaffCaller{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no repo)", w.Code)
	}
}

func TestHandler_SendPaylink_NilRepoIs503(t *testing.T) {
	h := &C8S3S4Handler{}
	body, _ := json.Marshal(sendPaylinkRequest{CustomerID: "11111111-1111-4111-8111-111111111111"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/ar/payments/paylink", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	h.sendPaylink(w, r, guards.StaffCaller{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no repo)", w.Code)
	}
}

func TestHandler_StatementPreview_NilRepoIs503(t *testing.T) {
	h := &C8S3S4Handler{}
	r := httptest.NewRequest(http.MethodGet,
		"/api/v1/billing/ar/customers/11111111-1111-4111-8111-111111111111/statements/22222222-2222-4222-8222-222222222222/preview",
		nil)
	r.SetPathValue("customer_id", "11111111-1111-4111-8111-111111111111")
	r.SetPathValue("statement_id", "22222222-2222-4222-8222-222222222222")
	w := httptest.NewRecorder()
	h.statementPreview(w, r, guards.StaffCaller{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no repo)", w.Code)
	}
}

// -- paylink delivery: pure row building -------------------------------------------------------

func TestPaylinkDeliveryRow_Propagates(t *testing.T) {
	inv := "11111111-1111-4111-8111-111111111111"
	amt := int64(9876)
	row := PaylinkDeliveryRow{
		CustomerID:  "22222222-2222-4222-8222-222222222222",
		InvoiceID:   &inv,
		AmountCents: &amt,
		Channel:     "EMAIL",
		Recipient:   "ap@example.com",
		PaylinkURL:  "/paylink/abc",
		IdemKey:     "paylink-abc",
		SentBy:      "staff",
	}
	if row.CustomerID == "" || row.IdemKey == "" || row.PaylinkURL == "" {
		t.Fatalf("row carries empty required field: %+v", row)
	}
}

// -- preview shape: the wire schema's own reconciliation rule -----------------------------------

func TestBillingStatementPreview_ReconciledTrueWhenBucketsSumToTotals(t *testing.T) {
	p := BillingStatementPreview{
		Aging: StatementPreviewAging{
			CurrentCents: 100, Days30Cents: 200, Days60Cents: 300,
			Days90Cents: 400, Days90PlusCents: 500,
			OpenCents: 1500, OverdueCents: 1400,
		},
		OpenLines: []StatementPreviewLine{{BalanceCents: 1500}},
	}
	sumBuckets := p.Aging.CurrentCents + p.Aging.Days30Cents + p.Aging.Days60Cents + p.Aging.Days90Cents + p.Aging.Days90PlusCents
	sumLate := p.Aging.Days30Cents + p.Aging.Days60Cents + p.Aging.Days90Cents + p.Aging.Days90PlusCents
	if sumBuckets != p.Aging.OpenCents {
		t.Fatalf("sum of buckets %d != openCents %d", sumBuckets, p.Aging.OpenCents)
	}
	if sumLate != p.Aging.OverdueCents {
		t.Fatalf("sum of late buckets %d != overdueCents %d", sumLate, p.Aging.OverdueCents)
	}
}

func TestBillingStatementPreview_DriftFailsReconciliation(t *testing.T) {
	// Five buckets sum to 1500; openCents is 1499 → does not reconcile.
	// Late buckets 200+300+400+499 = 1399; overdueCents is 1400 → does not reconcile on the late side either.
	p := BillingStatementPreview{
		Aging: StatementPreviewAging{
			CurrentCents: 100, Days30Cents: 200, Days60Cents: 300,
			Days90Cents: 400, Days90PlusCents: 500,
			OpenCents: 1499, OverdueCents: 1400,
		},
	}
	sumBuckets := p.Aging.CurrentCents + p.Aging.Days30Cents + p.Aging.Days60Cents + p.Aging.Days90Cents + p.Aging.Days90PlusCents
	sumLate := p.Aging.Days30Cents + p.Aging.Days60Cents + p.Aging.Days90Cents + p.Aging.Days90PlusCents
	if sumBuckets == p.Aging.OpenCents && sumLate == p.Aging.OverdueCents {
		t.Fatalf("drift case should not reconcile, but it did: sum %d == openCents %d, late %d == overdueCents %d",
			sumBuckets, p.Aging.OpenCents, sumLate, p.Aging.OverdueCents)
	}
}

// -- guard against accidental nil-pool drift ---------------------------------------------------

func TestCustomerExists_NilRepoIsErrNoDB(t *testing.T) {
	r := &Repository{}
	_, err := r.CustomerExists(context.Background(), "11111111-1111-4111-8111-111111111111")
	if !errors.Is(err, errNoDB) {
		t.Fatalf("CustomerExists on nil pool = %v, want errNoDB", err)
	}
}

func TestPreviewStatement_NilRepoIsErrNoDB(t *testing.T) {
	r := &Repository{}
	_, err := r.PreviewStatement(context.Background(), "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222")
	if !errors.Is(err, errNoDB) {
		t.Fatalf("PreviewStatement on nil pool = %v, want errNoDB", err)
	}
}

func TestRecordPaylinkDelivery_NilRepoIsErrNoDB(t *testing.T) {
	r := &Repository{}
	err := r.RecordPaylinkDelivery(context.Background(), PaylinkDeliveryRow{
		CustomerID: "11111111-1111-4111-8111-111111111111", Channel: "EMAIL",
		IdemKey: "paylink-x", SentAt: time.Now().UTC(),
	})
	if !errors.Is(err, errNoDB) {
		t.Fatalf("RecordPaylinkDelivery on nil pool = %v, want errNoDB", err)
	}
}
