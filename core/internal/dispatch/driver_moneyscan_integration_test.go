//go:build integration

// driver_moneyscan_integration_test.go, THE INSTRUMENT THE DRIVER BRIEF ASKS FOR, built.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run DriverMoneyScan ./internal/dispatch/
//
// WHAT WAS ALREADY PINNED, AND WHAT WAS NOT, the same audit train 15 ran, one payload over.
//
//   - THE TYPE, TestStopPayload_NeverCarriesMoney marshals a Stop literal with both money pointers
//     set and proves neither key survives. It is a good test and it is not this one: it marshals a
//     struct the test itself built, never a struct a database filled in, so it cannot see a leak that
//     arrives on a field the literal does not set.
//   - THE BOARD'S WIRING, TestUnion15LoadValue_TheGateIsWIREDIntoTheBoardAndNotMerelyDefined scans a
//     real payload with real digits from a real row, and it scans Service.LoadBoard. The driver
//     surface never calls LoadBoard. It calls MyRuns, and nothing in this package had ever marshalled
//     a MyRuns result.
//   - "cost" and "margin", greped for nowhere on any staff dispatch payload. The one test in the
//     module that looks for them is TestWillCall_PublicPayloadLeaksNothing, a different audience
//     (the unauthenticated will-call page) asserting on decoded map KEYS rather than bytes.
//
// So: the claim recorded in NOTES-UNION-TRAIN13.md, "on a load worth $3,531.25 the driver's stop
// object has no value_cents and no freight_cents KEY, and 353125 appears nowhere in the body", was
// true, was photographed, and was pinned by nothing that would fail if it stopped being true. This
// file is that claim as a test.
//
// WHY THE DIGITS ARE ASSERTED AS WELL AS THE KEYS. A key scan alone passes against a payload that
// renamed the field ("load_worth", "amount"); a digit scan alone passes against a row whose value
// happens to be zero. Both, over a row seeded with figures nothing else in the fixture set could
// produce, is what makes the absence a measurement rather than a hope.
//
// FIXTURES RESTORE WHAT THEY TOUCH: seedOrder's cleanup removes the order and its stops, and the
// driver and run register their own.

package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// driverScanLines satisfies OrderLineReader with lines carrying digits nothing else in the fixture
// produces, so the positive control below, the driver DOES receive the material, is a measured
// claim and not an assumption. It deliberately returns lines that look like a real order: a decimal
// quantity as a string, a UOM, a description with a number in it.
type driverScanLines struct{ orderID string }

func (l driverScanLines) OrderPickLines(_ context.Context, orderID string) ([]PickFeedLine, error) {
	if orderID != l.orderID {
		return nil, nil
	}
	return []PickFeedLine{
		{LineNo: 1, ProductID: "p-1", SKU: "SCAN-PAV-771", Description: "Brown pavers 60mm", QtyOrdered: "48.000", UOM: "EA", Location: "A-12"},
		{LineNo: 2, ProductID: "p-2", SKU: "SCAN-AGG-772", Description: "Screened sand", QtyOrdered: "2.500", UOM: "TONNE", Location: "YARD-3"},
	}, nil
}

func TestDriverMoneyScan_TheDaySentToADriverCarriesNoMoneyBytesAtAll(t *testing.T) {
	h := newHarness(t)
	mgrCtx := globalCtx(t, h.pool)
	date := today()
	userID := seededUser(t, h.pool)

	// - the row, with figures nothing else could have produced -
	orderID := seedOrder(t, h.pool, branchTRT, "DELIVERY", "CONFIRMED")
	h.svc = h.svc.WithOrderLines(driverScanLines{orderID: orderID})

	driverID, err := h.svc.CreateDriver(mgrCtx, Driver{BranchID: branchTRT, Name: "Money Scan Driver " + uniqueSuffix(), UserID: userID})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = h.pool.Exec(c, `UPDATE delivery_runs SET driver_id = NULL WHERE driver_id = $1`, driverID)
		_, _ = h.pool.Exec(c, `DELETE FROM dispatch_drivers WHERE id = $1`, driverID)
	})

	run, err := h.svc.CreateRun(mgrCtx, CreateRunInput{
		BranchID: branchTRT, ScheduledDate: date,
		TruckID: firstTruck(t, h.pool, branchTRT), DriverID: driverID,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = h.pool.Exec(c, `UPDATE delivery_stops SET run_id = NULL WHERE run_id = $1`, run.ID)
		_, _ = h.pool.Exec(c, `DELETE FROM delivery_runs WHERE id = $1`, run.ID)
	})

	// A priced, weighed stop ON THAT RUN. The cents go in through raw SQL for the same reason train 15
	// does it: the service has no way to set them, which is itself the point, they are a fact of the
	// row, and the question is whether a read hands them out.
	if _, err := h.pool.Exec(context.Background(), `
INSERT INTO delivery_stops (branch_id, order_id, run_id, scheduled_date, status, stop_no,
                            value_cents, freight_cents, weight_kg,
                            address_line, city, postal_code, time_window, contact_name)
VALUES ($1, $2, $3, $4::date, 'READY', 1, 353125, 9500, 1240.5,
        '118 Depot Road', 'Trenton', 'K8V 1A1', '08:00-11:00', 'Site foreman')`,
		branchTRT, orderID, run.ID, date); err != nil {
		t.Fatalf("seed stop: %v", err)
	}

	// - the positive control: the figures really are in the database -
	//
	// Without this, a driver's day carrying no money proves only that the fixture was empty. The
	// manager's load board is the read that is ALLOWED the figures, so it is the read that shows they
	// exist to be leaked.
	mgrJSON, err := json.Marshal(mustLoadBoard(t, h, mgrCtx, date))
	if err != nil {
		t.Fatalf("marshal manager board: %v", err)
	}
	for _, want := range []string{"353125", "9500", "value_cents", "freight_cents"} {
		if !strings.Contains(string(mgrJSON), want) {
			t.Fatalf("the manager's board must carry %q for this test to mean anything, the fixture "+
				"did not land. Got %s", want, mgrJSON)
		}
	}

	// - the driver's own day, over the wire shape the surface actually receives -
	mine, err := h.svc.MyRuns(union15DriverCtx(t, h), date)
	if err != nil {
		t.Fatalf("my runs: %v", err)
	}
	// handler.myRuns writes exactly this envelope; scanning the envelope rather than the slice means
	// a field added at the envelope level is scanned too.
	drvJSON, err := json.Marshal(map[string]any{"items": mine})
	if err != nil {
		t.Fatalf("marshal driver day: %v", err)
	}
	body := string(drvJSON)

	// THE BYTE SCAN. Two categories: the WORDS money hides behind whatever it is called, and the
	// DIGITS of this particular load. A rename defeats the first list; a zero row defeats the second;
	// neither defeats both.
	// null is not byte-absence. A money pointer that lost its `json:"-"` and gained an `,omitempty`
	// would be caught above; one that lost the tag entirely marshals as null, and that is the shape
	// train 14 measured on LoadStop.
	assertDriverPayloadJSON(t, drvJSON)

	// - and the gate removed the MONEY, not the WORK -
	//
	// Every one of these is a fact the driver needs and none of them is a figure. Asserting them is
	// what stops this test from being satisfiable by returning an empty day.
	for _, want := range []string{
		"118 Depot Road", "Trenton", "K8V 1A1", // where
		"08:00-11:00",    // when
		"Site foreman",   // who
		`"weight_kg":`,   // how heavy, weight is NOT money and must survive
		"SCAN-PAV-771",   // what comes off the truck
		"Brown pavers",   //
		`"qty":"48.000"`, // how much of it, in the line's own unit
		`"uom":"EA"`,     //
		`"stop_lines"`,   //
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("a driver's day must still carry %q, the gate hides figures, not the delivery. "+
				"Got %s", want, body)
		}
	}
}

// mustLoadBoard is the manager-side control read, factored out only so the assertion above reads as
// one line.
func mustLoadBoard(t *testing.T, h *harness, ctx context.Context, date string) any {
	t.Helper()
	view, err := h.svc.LoadBoard(ctx, branchTRT, date)
	if err != nil {
		t.Fatalf("manager load board: %v", err)
	}
	if !view.ValueVisible {
		t.Fatal("the fixture is wrong: a logistics manager must be allowed the load value")
	}
	return view
}
