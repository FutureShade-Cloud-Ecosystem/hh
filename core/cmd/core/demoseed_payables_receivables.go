// demoseed_payables_receivables.go, the receivables world's addition (item 64, Payables and
// Receivables): bounced payments for the queue to show, one per branch.
//
// The persona works the 18 invoices the book already seeds across the aging buckets (I01 to I18). What
// the book lacks is a payment that came back, so "marks a payment bounced and works it in the queue"
// had nothing to open. B01 is the cheque for the whole of I08 (a Kingston contractor's invoice, one to
// thirty days past due), returned NSF: the payment reads BOUNCED, the NSF record carries the reason and
// the policy fee, and no allocation remains, so I08 stands open for the full amount, which is exactly
// what the real bounce leaves behind (till/MarkNSF: flip BOUNCED, reverse the allocations, reopen the
// invoices). B02 is the same story at the Point Loma branch against I10, so the Payables and Receivables
// landing has a bounced cheque from each yard.
//
// The queue (GET /api/v1/ar/bounced) is payments joined to payment_nsf where the payment is BOUNCED, so
// both rows are needed or the payment is invisible. No credit hold is seeded: the dealer's NSF policy
// decides whether a bounce holds the account, and a tester who marks a second bounce sees that policy
// act.
//
// STABLE-KEYED, NOT DAY-KEYED, like every document: written once, recognised on a second apply. NUMBERED
// IN THE SEED'S OWN STYLE: DEMO-PAY-B01, and the cheque's reference DEMO-CHQ-B01, which the queue's exact
// reference search finds.
package main

// demoBouncedPayments is the receivables world's bounced payments. Invoice names the book invoice the
// cheque was tendered against; its customer, branch and amount are that invoice's.
var demoBouncedPayments = []struct {
	Key       string
	Invoice   string
	AgeDay    int // days since the cheque was received
	BouncedAt int // days since the bank returned it
	Reason    string
	FeeCents  int64
}{
	{"B01", "I08", 12, 9, "Cheque returned by the bank, not sufficient funds", 4_500},
	{"B02", "I10", 8, 5, "Cheque returned by the bank, account frozen", 4_500},
}

func (b *builder) bouncedPayments() {
	for _, s := range demoBouncedPayments {
		inv := b.invs[s.Invoice]
		total := inv.Subtotal + taxOn(inv.Subtotal)
		payKey := "bounced-payment:" + s.Key
		received, bounced := b.agoDays(s.AgeDay), b.agoDays(s.BouncedAt)
		b.add(insert("payments", "(id)",
			"id", demoID(payKey),
			"payment_no", demoPrefix+"PAY-"+s.Key,
			"customer_id", demoID("customer:"+inv.Cust),
			"method", "CHEQUE",
			"amount_cents", total,
			"status", "BOUNCED",
			"received_at", received,
			"gateway_ref", demoPrefix+"CHQ-"+s.Key,
			"branch_id", b.branchID(inv.Branch),
			"metadata", demoMetadata,
			"created_at", received, "updated_at", bounced,
		))
		b.add(insert("payment_nsf", "(payment_id)",
			"id", demoID(payKey+":nsf"),
			"payment_id", demoID(payKey),
			"customer_id", demoID("customer:"+inv.Cust),
			"reason", s.Reason,
			"fee_cents", s.FeeCents,
			"fee_taxable", false,
			"created_at", bounced,
		))
	}
}
