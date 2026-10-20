//go:build integration

package parity

// Red team case 9 of plan r3 section 7, on the receivables views (slice 7a): a manager without the payment trail
// asks for a payment's history (the allocations), a payment method, a payment amount by name, by aggregate, by
// filter, by order, by group and by null test; each is refused with notice.code = refused and the payment_trail
// class word, or answers with no row at all, and none returns a value. The same manager with the trail gets the
// figures. Case 9 as the plan words it assumed every payment column is trail only; the customer workspace shows a
// payment's amount and method to any staff, so the view follows the trail routes (the stricter reading) and the
// report asks which screen is the rule.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

func TestRedTeam9_AManagerWithoutThePaymentTrailCannotGetAPaymentByAnySpec(t *testing.T) {
	r := newARRig(t)
	q := &assist.ReadQuerier{Reader: r.env.Readers.Staff, Now: time.Now}
	ctx := testsupport.Ctx(t)
	var without, with arCaller
	for _, c := range r.cs {
		switch c.name {
		case "manager at both without trail":
			without = c
		case "global with trail":
			with = c
		}
	}
	off, _ := r.claimsFor(t, without)
	on, _ := r.claimsFor(t, with)
	if off.PaymentTrail || !on.PaymentTrail {
		t.Fatalf("the callers are not what the test needs: off=%v on=%v", off.PaymentTrail, on.PaymentTrail)
	}
	for _, v := range assist.CatalogueFor(off) {
		for _, col := range []string{"method", "status", "amount_cents", "unapplied_cents", "allocated_at"} {
			if _, ok := v.Column(col); ok && (v.Name == "ai.payments" || v.Name == "ai.payment_allocations") {
				t.Fatalf("the catalogue of a caller without the trail carries %s of %s", col, v.Name)
			}
		}
		if v.Name == "ai.payment_allocations" {
			if _, ok := v.Column("amount_cents"); ok {
				t.Fatal("the allocation amount is in a catalogue without the trail")
			}
		}
	}
	for name, spec := range map[string]string{
		"the method named":       `{"view":"ai.payments","select":["payment_no","method"]}`,
		"the amount named":       `{"view":"ai.payments","select":["payment_no","amount_cents"]}`,
		"the status named":       `{"view":"ai.payments","select":["payment_no","status"]}`,
		"the unapplied named":    `{"view":"ai.payments","select":["payment_no","unapplied_cents"]}`,
		"a sum of the amount":    `{"view":"ai.payments","aggregate":[{"fn":"sum","col":"amount_cents"}]}`,
		"an average by branch":   `{"view":"ai.payments","group_by":["branch_label"],"aggregate":[{"fn":"avg","col":"amount_cents"}]}`,
		"a count of the method":  `{"view":"ai.payments","aggregate":[{"fn":"count","col":"method"}]}`,
		"a group by the method":  `{"view":"ai.payments","group_by":["method"],"aggregate":[{"fn":"count"}]}`,
		"a filter on the amount": `{"view":"ai.payments","select":["payment_no"],"where":[{"col":"amount_cents","op":">","value":0}]}`,
		"a filter on the method": `{"view":"ai.payments","select":["payment_no"],"where":[{"col":"method","op":"in","value":["CASH","CARD"]}]}`,
		"an order on the amount": `{"view":"ai.payments","select":["payment_no"],"order_by":[{"col":"amount_cents","dir":"desc"}]}`,
		"a null test":            `{"view":"ai.payments","select":["payment_no"],"where":[{"col":"amount_cents","op":"is_null","value":false}]}`,
		"the allocation amount":  `{"view":"ai.payment_allocations","select":["payment_no","amount_cents"]}`,
		"a sum of allocations":   `{"view":"ai.payment_allocations","aggregate":[{"fn":"sum","col":"amount_cents"}]}`,
		"the allocation date":    `{"view":"ai.payment_allocations","select":["payment_no","allocated_at"]}`,
	} {
		res, err := q.Query(ctx, off, spec)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if res.Notice == nil || res.Notice.Code != assist.NoticeRefused || res.Notice.Class != "payment_trail" || !res.Empty || len(res.Items) != 0 {
			t.Errorf("%s: want a refusal of class payment_trail with no rows, got %+v", name, res)
		}
		if s := fmt.Sprintf("%+v", res); strings.Contains(s, "amount_cents") || strings.Contains(s, "method") {
			t.Errorf("%s: the refusal names the column: %s", name, s)
		}
	}
	// A payment's history, asked for by the plain columns: the allocations view holds no row for this asker.
	hist, err := q.Query(ctx, off, `{"view":"ai.payment_allocations","select":["payment_no","invoice_no"],"limit":100}`)
	if err != nil || len(hist.Items) != 0 {
		t.Errorf("a payment's history for a caller without the trail: %v, %d rows", err, len(hist.Items))
	}
	// The plain read of the payments answers, with the identifying cells and none of the payment class.
	list := `{"view":"ai.payments","where":[{"col":"payment_no","op":"contains","value":"S7A-` + r.w.tag + `"}],"limit":100}`
	res, err := q.Query(ctx, off, list)
	if err != nil || len(res.Items) == 0 {
		t.Fatalf("a plain payment list without the trail: %v, %d rows, %+v", err, len(res.Items), res.Notice)
	}
	for _, it := range res.Items {
		for _, label := range []string{"Method", "Payment status", "Amount", "Unapplied"} {
			if _, has := fieldValue(it, label); has {
				t.Fatalf("a payment carries %q for a caller without the trail: %+v", label, it)
			}
		}
	}
	// The same asker with the trail: the figures, the aggregate, the filter, the order, the group and the history.
	res, err = q.Query(ctx, on, `{"view":"ai.payments","select":["payment_no","method","amount_cents"],"where":[{"col":"payment_no","op":"contains","value":"S7A-`+r.w.tag+`"}],"order_by":[{"col":"amount_cents","dir":"desc"}],"limit":100}`)
	if err != nil || res.Notice != nil && res.Notice.Code == assist.NoticeRefused || len(res.Items) < 7 {
		t.Fatalf("a caller with the trail: %v %d rows %+v", err, len(res.Items), res.Notice)
	}
	for _, it := range res.Items {
		if _, has := fieldValue(it, "Amount"); !has {
			t.Errorf("a payment carries no amount for a caller with the trail: %+v", it)
		}
	}
	if first, _ := fieldValue(res.Items[0], "Amount"); first != "$70.00" {
		t.Errorf("ordered by amount the largest planted payment should lead: %q)", first)
	}
	agg, err := q.Query(ctx, on, `{"view":"ai.payments","group_by":["method"],"aggregate":[{"fn":"sum","col":"amount_cents"}],"where":[{"col":"payment_no","op":"contains","value":"S7A-`+r.w.tag+`"}]}`)
	if err != nil || agg.Notice != nil && agg.Notice.Code == assist.NoticeRefused || len(agg.Items) < 4 {
		t.Errorf("a sum by method for a caller with the trail: %v %d groups %+v", err, len(agg.Items), agg.Notice)
	}
	filt, err := q.Query(ctx, on, `{"view":"ai.payments","select":["payment_no","amount_cents"],"where":[{"col":"amount_cents","op":">","value":4000},{"col":"payment_no","op":"contains","value":"S7A-`+r.w.tag+`"}]}`)
	if err != nil || len(filt.Items) != 2 {
		t.Errorf("a filter on the amount for a caller with the trail: %v, %d rows, want the two payments over $40", err, len(filt.Items))
	}
	// An aggregate ordered by its own figure is ordered as a number, not as text.
	top, err := q.Query(ctx, on, `{"view":"ai.payments","group_by":["method"],"aggregate":[{"fn":"sum","col":"amount_cents"}],"where":[{"col":"payment_no","op":"contains","value":"S7A-`+r.w.tag+`"}],"order_by":[{"col":"sum_amount_cents","dir":"desc"}]}`)
	if err != nil || len(top.Items) < 4 {
		t.Fatalf("a sum by method ordered by the sum: %v, %d groups", err, len(top.Items))
	}
	if lead, _ := fieldValue(top.Items[0], "Total amount"); lead != "$85.00" {
		t.Errorf("the largest method total should lead (EFT, $85.00): %q (%+v)", lead, top.Items)
	}
	h2, err := q.Query(ctx, on, `{"view":"ai.payment_allocations","select":["payment_no","invoice_no","amount_cents"],"where":[{"col":"payment_no","op":"contains","value":"S7A-`+r.w.tag+`"}],"limit":100}`)
	if err != nil || len(h2.Items) != 4 {
		t.Errorf("a payment's history for a caller with the trail: %v, %d rows (want the 4 planted allocations)", err, len(h2.Items))
	}
	// Through the service with a scripted provider: the model asks for the method by name; the answer carries the
	// refusal.
	reg := assist.NewRegistry(assist.Deps{Query: q}, nil)
	svc := assist.NewService(reg, &attackProvider{replies: []assist.Reply{
		spec(`{"view":"ai.payments","select":["payment_no","method"]}`), {Text: "I cannot show that."},
	}}, noLedger{}, noLimits{}, assist.StaticSettings{}, nil)
	ans, err := svc.Ask(ctx, off, "how did the customer pay?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ans.Notice == nil || ans.Notice.Code != assist.NoticeRefused || ans.Notice.Class != "payment_trail" {
		t.Errorf("the answer notice = %+v, want refused / payment_trail; answer %+v", ans.Notice, ans)
	}
}
