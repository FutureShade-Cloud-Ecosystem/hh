//go:build integration

package purchasing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// postUSDBillAt creates a USD PO, receives it, creates a USD vendor bill at fxRate,
// posts it through the service seam, and registers cleanup for the PO, bill, and payment journals.
func (rig *apAgingTestRig) postUSDBillAt(t *testing.T, tag, billDate, dueDate string, usdCents int64, fxRate float64, postedAt time.Time) BillDetail {
	t.Helper()
	return rig.postUSDBillQtyAt(t, tag, billDate, dueDate, usdCents, 1, fxRate, postedAt)
}

// postUSDBillQtyAt is postUSDBillAt for a line of qty units at unitUSDCents each.
func (rig *apAgingTestRig) postUSDBillQtyAt(t *testing.T, tag, billDate, dueDate string, usdCents int64, qty float64, fxRate float64, postedAt time.Time) BillDetail {
	t.Helper()
	if d, err := time.Parse("2006-01-02", billDate); err == nil {
		rig.ensurePeriod(t, d.Year(), int(d.Month()))
	}
	now := time.Now().In(rig.zone)
	rig.ensurePeriod(t, now.Year(), int(now.Month()))

	prod := rig.newProduct(t, tag)
	po, err := rig.svc.CreatePO(rig.ctx, CreatePORequest{
		VendorID: rig.vendor,
		BranchID: branchTRT,
		Currency: "USD",
		FXRate:   fxRate,
		Lines:    []AddLineParams{{ProductID: prod, QtyOrderedBase: qty, UOM: "EA", UnitCostCents: usdCents}},
	})
	if err != nil {
		t.Fatalf("CreatePO(%s): %v", tag, err)
	}
	rig.cleanupPO(t, po.ID)
	if _, err := rig.svc.Transition(rig.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("Transition PO(%s): %v", tag, err)
	}
	lineID := po.Lines[0].ID
	if _, err := rig.svc.Receive(rig.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: qty}},
	}); err != nil {
		t.Fatalf("Receive PO(%s): %v", tag, err)
	}

	created, err := rig.svc.CreateBill(rig.ctx, CreateBillRequest{
		BillNo:   billNo(tag),
		POID:     po.ID,
		BillDate: billDate,
		DueDate:  dueDate,
		Currency: "USD",
		FXRate:   fxRate,
		Lines: []BillLineParams{
			{
				POLineID:      lineID,
				QtyBilledBase: qty,
				UnitCostCents: usdCents,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateBill(%s): %v", tag, err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source = 'PAYMENT' AND source_ref_id = $1::uuid)`, created.ID)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE source = 'PAYMENT' AND source_ref_id = $1::uuid`, created.ID)
	})

	posted, err := rig.svc.PostBill(rig.ctx, created.ID, PostBillRequest{
		AcceptVariance: true,
		Note:           "accept USD bill for W3-FX1 test",
	})
	if err != nil {
		t.Fatalf("PostBill(%s): %v", tag, err)
	}
	if !postedAt.IsZero() && posted.GLJournalID != "" {
		if _, err := rig.pool.Exec(rig.ctx, `UPDATE gl_journals SET posted_at = $1 WHERE id = $2`, postedAt, posted.GLJournalID); err != nil {
			t.Fatalf("stamp bill journal posted_at: %v", err)
		}
	}
	return posted
}

// paymentJournalLegs reads the posted payment journal's legs by account code
// as map[code][2]int64{debitCents, creditCents}.
func (rig *apAgingTestRig) paymentJournalLegs(t *testing.T, idempotencyKey string) map[string][2]int64 {
	t.Helper()
	rows, err := rig.pool.Query(rig.ctx, `
		SELECT a.code::text, COALESCE(l.debit_cents, 0), COALESCE(l.credit_cents, 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1
		   AND j.status = 'POSTED'`,
		glPaymentKeyPrefix+idempotencyKey,
	)
	if err != nil {
		t.Fatalf("query payment journal legs (%s): %v", idempotencyKey, err)
	}
	defer rows.Close()

	legs := make(map[string][2]int64)
	var sumDr, sumCr int64
	for rows.Next() {
		var code string
		var dr, cr int64
		if err := rows.Scan(&code, &dr, &cr); err != nil {
			t.Fatalf("scan payment journal leg: %v", err)
		}
		cur := legs[code]
		cur[0] += dr
		cur[1] += cr
		legs[code] = cur
		sumDr += dr
		sumCr += cr
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("payment journal legs rows: %v", err)
	}
	if len(legs) == 0 {
		t.Fatalf("no POSTED payment journal found for key %q", idempotencyKey)
	}
	if sumDr != sumCr {
		t.Fatalf("payment journal %q is unbalanced: debit=%d credit=%d", idempotencyKey, sumDr, sumCr)
	}
	return legs
}

// billNetAP2100Cents returns the net credit balance on GL 2100 for a single bill
// (its posting journal's 2100 credit minus all its payment journals' 2100 debits).
func (rig *apAgingTestRig) billNetAP2100Cents(t *testing.T, billID string) int64 {
	t.Helper()
	var netCredit int64
	err := rig.pool.QueryRow(rig.ctx, `
		SELECT COALESCE(SUM(COALESCE(l.credit_cents, 0) - COALESCE(l.debit_cents, 0)), 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.status = 'POSTED'
		   AND j.source_ref_id = $1::uuid
		   AND a.code = '2100'`,
		billID,
	).Scan(&netCredit)
	if err != nil {
		t.Fatalf("billNetAP2100Cents(%s): %v", billID, err)
	}
	return netCredit
}

// billOpenInAgingCents returns the OpenCents of billID in APAging as of asOf (0 if absent).
func (rig *apAgingTestRig) billOpenInAgingCents(t *testing.T, billID string, asOf time.Time) int64 {
	t.Helper()
	page, err := rig.svc.APAging(rig.ctx, asOf, BranchReach{BranchIDs: []string{branchTRT}})
	if err != nil {
		t.Fatalf("APAging: %v", err)
	}
	for _, row := range page.Items {
		for _, item := range row.OpenItems {
			if item.ID == billID {
				return item.OpenCents
			}
		}
	}
	return 0
}

func TestIntegration_W3FX1_RealisedFXGainOrLossAtPayment(t *testing.T) {
	rig := newAPAgingRig(t)
	loc := rig.zone
	nowLocal := time.Now().In(loc)
	billPostInstant := nowLocal
	payInstant := nowLocal.Add(time.Minute)
	asOf := nowLocal.Add(2 * time.Hour)
	rig.ensurePeriod(t, payInstant.Year(), int(payInstant.Month()))
	rig.ensurePeriod(t, asOf.Year(), int(asOf.Month()))
	billDate := nowLocal.Format("2006-01-02")
	dueDate := nowLocal.AddDate(0, 0, 30).Format("2006-01-02")

	t.Run("1_loss_at_1.38", func(t *testing.T) {
		// USD 1,000.00 bill at 1.35 -> CAD 1,350.00 (135,000 cents) in 2100.
		bill := rig.postUSDBillAt(t, "w3fx1-loss", billDate, dueDate, 100_000, 1.35, billPostInstant)
		if bill.TotalCents != 135_000 {
			t.Fatalf("posted USD bill TotalCents = %d, want 135000", bill.TotalCents)
		}
		if got := rig.billNetAP2100Cents(t, bill.ID); got != 135_000 {
			t.Fatalf("initial GL 2100 credit = %d, want 135000", got)
		}

		key := "w3fx1-loss-138-" + bill.ID
		if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
			AmountCents:    100_000,
			FXRate:         1.38,
			PaidAt:         payInstant,
			Note:           "USD wire at 1.38",
			IdempotencyKey: key,
		}); err != nil {
			t.Fatalf("W3-FX1 red: PayBill(USD 1000.00 at 1.38): %v", err)
		}

		legs := rig.paymentJournalLegs(t, key)
		if legs["2100"] != ([2]int64{135_000, 0}) {
			t.Fatalf("W3-FX1 red: payment journal 2100 = %v, want DR 135000 / CR 0", legs["2100"])
		}
		if legs["1000"] != ([2]int64{0, 138_000}) {
			t.Fatalf("W3-FX1 red: payment journal 1000 = %v, want DR 0 / CR 138000", legs["1000"])
		}
		if legs["6310"] != ([2]int64{3_000, 0}) {
			t.Fatalf("W3-FX1 red: payment journal 6310 (FX loss) = %v, want DR 3000 / CR 0 (all legs: %v)", legs["6310"], legs)
		}

		if got := rig.billNetAP2100Cents(t, bill.ID); got != 0 {
			t.Fatalf("W3-FX1 red: GL 2100 net credit after full payment = %d, want 0", got)
		}
		if open := rig.billOpenInAgingCents(t, bill.ID, asOf); open != 0 {
			t.Fatalf("W3-FX1 red: APAging open cents after full payment = %d, want 0", open)
		}
	})

	t.Run("2_gain_at_1.32", func(t *testing.T) {
		// USD 1,000.00 bill at 1.35 -> CAD 1,350.00 (135,000 cents) in 2100.
		bill := rig.postUSDBillAt(t, "w3fx1-gain", billDate, dueDate, 100_000, 1.35, billPostInstant)
		if bill.TotalCents != 135_000 {
			t.Fatalf("posted USD bill TotalCents = %d, want 135000", bill.TotalCents)
		}

		key := "w3fx1-gain-132-" + bill.ID
		if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
			AmountCents:    100_000,
			FXRate:         1.32,
			PaidAt:         payInstant,
			Note:           "USD wire at 1.32",
			IdempotencyKey: key,
		}); err != nil {
			t.Fatalf("W3-FX1 red: PayBill(USD 1000.00 at 1.32): %v", err)
		}

		legs := rig.paymentJournalLegs(t, key)
		if legs["2100"] != ([2]int64{135_000, 0}) {
			t.Fatalf("W3-FX1 red: payment journal 2100 = %v, want DR 135000 / CR 0", legs["2100"])
		}
		if legs["1000"] != ([2]int64{0, 132_000}) {
			t.Fatalf("W3-FX1 red: payment journal 1000 = %v, want DR 0 / CR 132000", legs["1000"])
		}
		if legs["6310"] != ([2]int64{0, 3_000}) {
			t.Fatalf("W3-FX1 red: payment journal 6310 (FX gain) = %v, want DR 0 / CR 3000 (all legs: %v)", legs["6310"], legs)
		}

		if got := rig.billNetAP2100Cents(t, bill.ID); got != 0 {
			t.Fatalf("W3-FX1 red: GL 2100 net credit after full payment = %d, want 0", got)
		}
		if open := rig.billOpenInAgingCents(t, bill.ID, asOf); open != 0 {
			t.Fatalf("W3-FX1 red: APAging open cents after full payment = %d, want 0", open)
		}
	})

	t.Run("3_part_payments_pro_rata_half_to_even_and_remainder", func(t *testing.T) {
		// USD 1,000.00 (100,000 USD cents) at 1.35 -> CAD 1,350.00 (135,000 CAD cents).
		bill := rig.postUSDBillAt(t, "w3fx1-part", billDate, dueDate, 100_000, 1.35, billPostInstant)

		steps := []struct {
			name       string
			usdCents   int64
			fxRate     float64
			paidAt     time.Time
			wantAPDr   int64
			wantCashCr int64
			wantFXDr   int64
			wantFXCr   int64
			wantRemCAD int64
		}{
			{
				// 135000 * 30 / 100000 = 40.5 -> half-to-even rounds to 40 (even).
				// Cash: 30 * 1.40 = 42 -> FX loss = 2.
				name:       "p1-even-down",
				usdCents:   30,
				fxRate:     1.40,
				paidAt:     payInstant,
				wantAPDr:   40,
				wantCashCr: 42,
				wantFXDr:   2,
				wantFXCr:   0,
				wantRemCAD: 134_960,
			},
			{
				// 135000 * 50 / 100000 = 67.5 -> half-to-even rounds to 68 (even).
				// Cash: 50 * 1.30 = 65 -> FX gain = 3.
				name:       "p2-even-up",
				usdCents:   50,
				fxRate:     1.30,
				paidAt:     payInstant.Add(time.Minute),
				wantAPDr:   68,
				wantCashCr: 65,
				wantFXDr:   0,
				wantFXCr:   3,
				wantRemCAD: 134_892,
			},
			{
				// 135000 * 33333 / 100000 = 44999.55 -> rounds to 45000.
				// Cash: 33333 * 1.38 = 45999.54 -> 46000 -> FX loss = 1000.
				name:       "p3-third-a",
				usdCents:   33_333,
				fxRate:     1.38,
				paidAt:     payInstant.Add(2 * time.Minute),
				wantAPDr:   45_000,
				wantCashCr: 46_000,
				wantFXDr:   1_000,
				wantFXCr:   0,
				wantRemCAD: 89_892,
			},
			{
				// 135000 * 33333 / 100000 = 44999.55 -> rounds to 45000.
				// Cash: 33333 * 1.32 = 43999.56 -> 44000 -> FX gain = 1000.
				name:       "p4-third-b",
				usdCents:   33_333,
				fxRate:     1.32,
				paidAt:     payInstant.Add(3 * time.Minute),
				wantAPDr:   45_000,
				wantCashCr: 44_000,
				wantFXDr:   0,
				wantFXCr:   1_000,
				wantRemCAD: 44_892,
			},
			{
				// Final payment of remaining 33,254 USD cents (30+50+33333+33333+33254 = 100000).
				// Pro rata alone would be 135000 * 33254 / 100000 = 44892.90 -> 44893 (over-clearing 2100 by 1 cent).
				// Last payment clears exact remainder: 135000 - (40+68+45000+45000) = 44892.
				// Paid at the bill's own rate 1.35, so the cash is the CAD the bill carries for it and
				// the cent of pro rata rounding posts no FX.
				name:       "p5-final-remainder",
				usdCents:   33_254,
				fxRate:     1.35,
				paidAt:     payInstant.Add(4 * time.Minute),
				wantAPDr:   44_892,
				wantCashCr: 44_892,
				wantFXDr:   0,
				wantFXCr:   0,
				wantRemCAD: 0,
			},
		}

		for _, st := range steps {
			key := "w3fx1-part-" + st.name + "-" + bill.ID
			if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
				AmountCents:    st.usdCents,
				FXRate:         st.fxRate,
				PaidAt:         st.paidAt,
				Note:           st.name,
				IdempotencyKey: key,
			}); err != nil {
				t.Fatalf("W3-FX1 red (%s): PayBill(%d USD cents at %v): %v", st.name, st.usdCents, st.fxRate, err)
			}

			legs := rig.paymentJournalLegs(t, key)
			if legs["2100"] != ([2]int64{st.wantAPDr, 0}) {
				t.Fatalf("W3-FX1 red (%s): 2100 = %v, want DR %d / CR 0", st.name, legs["2100"], st.wantAPDr)
			}
			if legs["1000"] != ([2]int64{0, st.wantCashCr}) {
				t.Fatalf("W3-FX1 red (%s): 1000 = %v, want DR 0 / CR %d", st.name, legs["1000"], st.wantCashCr)
			}
			if legs["6310"] != ([2]int64{st.wantFXDr, st.wantFXCr}) {
				t.Fatalf("W3-FX1 red (%s): 6310 = %v, want DR %d / CR %d", st.name, legs["6310"], st.wantFXDr, st.wantFXCr)
			}

			glRem := rig.billNetAP2100Cents(t, bill.ID)
			if glRem != st.wantRemCAD {
				t.Fatalf("W3-FX1 red (%s): GL 2100 remaining credit = %d, want %d", st.name, glRem, st.wantRemCAD)
			}
			agingOpen := rig.billOpenInAgingCents(t, bill.ID, asOf)
			if agingOpen != st.wantRemCAD {
				t.Fatalf("W3-FX1 red (%s): APAging open cents = %d, want %d (must tie to GL 2100 = %d)", st.name, agingOpen, st.wantRemCAD, glRem)
			}
		}

		// Any further payment on the fully settled USD bill must be refused as exceeding open balance 0.
		if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
			AmountCents:    1,
			FXRate:         1.35,
			PaidAt:         payInstant.Add(5 * time.Minute),
			IdempotencyKey: "w3fx1-part-overpay-" + bill.ID,
		}); err == nil {
			t.Fatalf("W3-FX1 red: payment after full USD settlement must be refused")
		}
	})

	t.Run("4_usd_bill_without_rate_refused", func(t *testing.T) {
		bill := rig.postUSDBillAt(t, "w3fx1-norate", billDate, dueDate, 100_000, 1.35, billPostInstant)

		for _, badRate := range []float64{0, -1.35} {
			_, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
				AmountCents:    100_000,
				FXRate:         badRate,
				PaidAt:         payInstant,
				Note:           "missing rate",
				IdempotencyKey: "w3fx1-norate-" + bill.ID,
			})
			if err == nil {
				t.Fatalf("W3-FX1 red: USD bill paid with fx_rate=%v must be refused", badRate)
			}
			var ce clientError
			if !errors.As(err, &ce) || ce.status != http.StatusBadRequest {
				t.Fatalf("W3-FX1 red: USD bill paid with fx_rate=%v error = %v, want 400 BadRequest", badRate, err)
			}
		}

		var instCount int
		if err := rig.pool.QueryRow(rig.ctx, `SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = $1 AND status = 'PAID'`, bill.ID).Scan(&instCount); err != nil {
			t.Fatalf("count paid instalments: %v", err)
		}
		if instCount != 0 {
			t.Fatalf("refused USD payment wrote %d PAID instalments, want 0", instCount)
		}

		// And a CAD bill still needs no fx_rate.
		_, cadBill := rig.createReceivedPOAndPostBill(t, "w3fx1-cad", 50_000, billDate, dueDate)
		t.Cleanup(func() {
			bg := context.Background()
			_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source = 'PAYMENT' AND source_ref_id = $1::uuid)`, cadBill.ID)
			_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE source = 'PAYMENT' AND source_ref_id = $1::uuid`, cadBill.ID)
		})
		if _, err := rig.svc.PayBill(rig.ctx, cadBill.ID, PayBillRequest{
			AmountCents:    50_000,
			PaidAt:         payInstant,
			Note:           "CAD payment needs no fx_rate",
			IdempotencyKey: "w3fx1-cad-" + cadBill.ID,
		}); err != nil {
			t.Fatalf("CAD bill payment without fx_rate: %v", err)
		}

		// Also verify the HTTP route refuses a USD bill payment without fx_rate (400) and accepts one with fx_rate (200).
		rig.svc.withCaller(identity.StaffContextFromContext)
		t.Cleanup(func() { rig.svc.withCaller(ctxCaller(rig.user)) })
		sg := testkit.NewStaffGuards(t)
		sg.SetAuthority(erpidentity.StaffAuthority{
			RoleID:   "BRANCH_MANAGER",
			Tier:     erpidentity.TierManager,
			Scope:    erpidentity.ScopeBranch,
			Branches: []string{branchTRT},
		})
		mux := http.NewServeMux()
		NewHandler(rig.svc, slog.Default()).WithUserLinker(func(context.Context, string) (string, bool) {
			return rig.user, true
		}).RegisterRoutes(mux, sg.Guards)

		reqNoRate := httptest.NewRequest(http.MethodPost, "/api/v1/purchasing/bills/"+bill.ID+"/payments",
			bytes.NewReader([]byte(`{"amount_cents":100000,"note":"no rate"}`)))
		reqNoRate.Header.Set("Content-Type", "application/json")
		reqNoRate.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
		reqNoRate.Header.Set("X-Org", testkit.OrgNorthSlug)
		reqNoRate.Header.Set("Idempotency-Key", "w3fx1-http-norate-"+bill.ID)
		recNoRate := httptest.NewRecorder()
		mux.ServeHTTP(recNoRate, reqNoRate)
		if recNoRate.Code != http.StatusBadRequest {
			t.Fatalf("HTTP POST /payments on USD bill without fx_rate status = %d, want 400 (body: %s)", recNoRate.Code, recNoRate.Body.String())
		}

		bodyWithRate, _ := json.Marshal(map[string]any{
			"amount_cents": 100_000,
			"fx_rate":      1.38,
			"paid_at":      payInstant.Format(time.RFC3339),
			"note":         "with rate",
		})
		reqWithRate := httptest.NewRequest(http.MethodPost, "/api/v1/purchasing/bills/"+bill.ID+"/payments",
			bytes.NewReader(bodyWithRate))
		reqWithRate.Header.Set("Content-Type", "application/json")
		reqWithRate.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
		reqWithRate.Header.Set("X-Org", testkit.OrgNorthSlug)
		reqWithRate.Header.Set("Idempotency-Key", "w3fx1-http-withrate-"+bill.ID)
		recWithRate := httptest.NewRecorder()
		mux.ServeHTTP(recWithRate, reqWithRate)
		if recWithRate.Code != http.StatusOK {
			t.Fatalf("HTTP POST /payments on USD bill with fx_rate=1.38 status = %d, want 200 (body: %s)", recWithRate.Code, recWithRate.Body.String())
		}
	})
}
