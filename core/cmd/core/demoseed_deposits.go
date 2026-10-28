// demoseed_deposits.go, the deposit world (item 9.9 of the row 1047 deposits work): two sales a
// tester can walk at the Trenton till, each rung with a deposit as line one and the rest owed.
//
//	D01  a credit customer (C02): a will call sale on the ACCOUNT tender with a card deposit taken,
//	     the rest riding the account on its terms. Not held: a credit customer owes on terms, which
//	     is what terms are. The Pay balance surface finds it by order number or by the customer.
//	D02  the Cash On Demand account (C10): a COD sale with a cash deposit taken, the rest due later.
//	     Held from picking until the last dollar lands (order/pick_hold.go reads the invoice balance
//	     against the zero day terms), "In process: deposit taken" on its card because a payment has
//	     landed and money remains.
//
// WHAT EACH IS, as rows: the order (CONFIRMED, will call, its job reference), its line, the invoice
// (PARTIALLY_PAID, the balance the rest), the deposit payment (RECEIVED, the method the sale named)
// with its allocation, and the pos_sale row naming the shape. Exactly the trio a counter sale leaves
// behind (demoseed_build.go counterSale), with the one payment a deposit is instead of the whole
// total. Every figure comes from the builder's own price rules and the flat HST, so a seeded balance
// is the number the Pay balance find computes from the same rows.
//
// NUMBERED IN THE SEED'S OWN STYLE: DEMO-TRT-D01 and DEMO-TRT-D02, the way the order book's O04 reads
// DEMO-TRT-O04 and a counter sale's S03 reads DEMO-TRT-S03. A tester types DEMO-TRT-D0 (or the
// customer's name) into Pay balance and finds both.
//
// STABLE-KEYED, NOT DAY-KEYED: a deposit order is a document, written once. Like every stable row
// the applier recognises it on a second apply and writes nothing, so a tester who takes the balance
// leaves the order paid and it stays paid; apply never updates a row, so the unpaid states do not
// come back (the same posture, and the same words for the testers' page, the quote world states).
package main

import (
	"fmt"
)

type depositSaleSpec struct {
	Key        string
	Branch     int
	Cust       string
	AgeDay     int
	SaleType   string // COLLECT_LATER (a will call sale on account) | COD (pay before release)
	Tender     string // the pos_sale tender: ACCOUNT for the credit shape, the deposit method for COD
	Method     string // the deposit's method: CASH | CARD
	Deposit    int64  // cents, always short of the order total
	Job        string
	Prod       string
	Qty        string
	QtyF       float64
	Fulfilment string
}

var demoDepositSales = []depositSaleSpec{
	{Key: "D01", Branch: 1, Cust: "C02", AgeDay: 1, SaleType: "COLLECT_LATER", Tender: "ACCOUNT", Method: "CARD",
		Deposit: 50_000, Job: "Marina Road patio", Prod: "P02", Qty: "200.0000", QtyF: 200, Fulfilment: "WILL_CALL"},
	{Key: "D02", Branch: 1, Cust: "C10", AgeDay: 1, SaleType: "COD", Tender: "CASH", Method: "CASH",
		Deposit: 2_000, Job: "Patio base material", Prod: "P08", Qty: "4.0000", QtyF: 4, Fulfilment: "WILL_CALL"},
}

func (b *builder) depositSales() {
	for _, s := range demoDepositSales {
		p := b.prod[s.Prod]
		unit := b.unitPrice(s.Cust, s.Prod)
		subtotal := int64(s.QtyF * float64(unit))
		tax := taxOn(subtotal)
		total := subtotal + tax
		balance := total - s.Deposit
		at := b.agoDays(s.AgeDay)
		orderKey, invKey, payKey := "deposit-order:"+s.Key, "deposit-invoice:"+s.Key, "deposit-payment:"+s.Key
		no := fmt.Sprintf("%s%s-%s", demoPrefix, b.branchCode(s.Branch), s.Key)

		meta := demoMetadata
		if s.SaleType == "COD" {
			// What the ring stamps on a pay before release sale, so a tester reading the order sees the
			// same posture and the deposit as a display fact (service.go, the meta block).
			meta = fmt.Sprintf(`{"demo_seed":"%s","cod":true,"cod_posture":"AWAITING_PAYMENT","deposit_cents":%d,"deposit_method":"%s"}`,
				demoMarker, s.Deposit, s.Method)
		}
		due := b.day(-s.AgeDay)
		if s.SaleType != "COD" {
			due = b.day(-s.AgeDay + b.cust[s.Cust].TermsDays)
		}

		b.addDeposit(insert("orders", "(id)",
			"id", demoID(orderKey),
			"order_no", no,
			"customer_id", demoID("customer:"+s.Cust),
			"branch_id", b.branchID(s.Branch),
			"status", "CONFIRMED", "fulfillment", s.Fulfilment,
			"requested_date", b.day(2),
			"subtotal_cents", subtotal, "tax_cents", tax, "total_cents", total,
			"job_reference", s.Job,
			"metadata", meta,
			"created_at", at, "updated_at", at,
		))
		b.addDeposit(insert("order_lines", "(id)",
			"id", demoID(orderKey+":1"),
			"order_id", demoID(orderKey), "line_no", 1,
			"product_id", demoID("product:"+s.Prod), "description", p.Name,
			"qty_entered", s.Qty, "uom_entered", p.UOM, "qty_base", s.Qty,
			"fulfilled_base", "0",
			"unit_price_cents", unit, "price_uom", p.UOM, "line_total_cents", subtotal,
			"metadata", demoMetadata,
			"created_at", at, "updated_at", at,
		))
		b.addDeposit(insert("invoices", "(id)",
			"id", demoID(invKey),
			"invoice_no", demoPrefix+"INV-"+s.Key,
			"customer_id", demoID("customer:"+s.Cust),
			"order_id", demoID(orderKey),
			"branch_id", b.branchID(s.Branch),
			"org_id", driverOrgStamp(),
			"status", "PARTIALLY_PAID",
			"issue_date", b.day(-s.AgeDay), "due_date", due,
			"subtotal_cents", subtotal, "tax_cents", tax, "total_cents", total,
			"balance_cents", balance, "finalized_at", at,
			"metadata", demoMetadata,
			"created_at", at, "updated_at", at,
		))
		b.addDeposit(insert("invoice_lines", "(id)",
			"id", demoID(invKey+":1"),
			"invoice_id", demoID(invKey), "line_no", 1,
			"product_id", demoID("product:"+s.Prod), "description", p.Name,
			"qty_entered", s.Qty, "uom_entered", p.UOM, "qty_base", s.Qty,
			"unit_price_cents", unit, "price_uom", p.UOM, "line_total_cents", subtotal,
			"created_at", at,
		))
		b.addDeposit(insert("payments", "(id)",
			"id", demoID(payKey),
			"payment_no", demoPrefix+"PAY-"+s.Key,
			"customer_id", demoID("customer:"+s.Cust),
			"method", s.Method,
			"amount_cents", s.Deposit,
			"status", "RECEIVED",
			"received_at", at,
			"branch_id", b.branchID(s.Branch),
			"metadata", demoMetadata,
			"created_at", at, "updated_at", at,
		))
		b.addDeposit(insert("payment_allocations", "(payment_id, invoice_id, kind)",
			"payment_id", demoID(payKey),
			"invoice_id", demoID(invKey),
			"amount_cents", s.Deposit,
			"created_at", at,
		))
		b.addDeposit(insert("pos_sale", "(id)",
			"id", demoID("possale:"+s.Key),
			"idempotency_key", demoID("possale-idem:"+s.Key),
			"order_id", demoID(orderKey),
			"invoice_id", demoID(invKey),
			"branch_id", b.branchID(s.Branch),
			"sale_type", s.SaleType,
			"cash_sale", s.Method == "CASH" && s.Tender == "CASH",
			"tender", s.Tender,
			"status", "COMMITTED",
			"payment_id", demoID(payKey),
			"created_at", at,
		))
		b.sum.Orders["CONFIRMED"]++
		b.sum.Aging["current"]++
		b.sum.DepositOrders = append(b.sum.DepositOrders,
			fmt.Sprintf("%s  %s  %s  deposit by %s, rest %s", no, s.Cust, s.SaleType, s.Method, map[bool]string{true: "due later, held from picking", false: "on account"}[s.SaleType == "COD"]))
	}
}

// addDeposit adds a row and records its id, the rows the world's proofs find by.
func (b *builder) addDeposit(r row) {
	b.add(r)
	b.sum.DepositRowIDs = append(b.sum.DepositRowIDs, fmt.Sprint(r.vals[0]))
}
