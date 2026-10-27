package invcount

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// - in-memory fakes (no DB) -

type fakeStore struct {
	sheet   Sheet
	lines   []Line
	reasons []ReasonCode
	recon   map[string]ReconRun // key: year|month|branch
	inserts int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		reasons: []ReasonCode{
			{Code: "DAMAGE", AccountCode: "5200", IsActive: true},
			{Code: "FOUND", AccountCode: "5200", IsActive: true},
			{Code: "SPOIL", AccountCode: "5900", IsActive: true},
		},
		recon: map[string]ReconRun{},
	}
}

func reconKey(y, m int, b string) string { return b + "|" + itoa(y) + "|" + itoa(m) }
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func (f *fakeStore) GetSheet(_ context.Context, _ string) (Sheet, error) { return f.sheet, nil }
func (f *fakeStore) ListSheets(_ context.Context, _, _ string) ([]Sheet, error) {
	return []Sheet{f.sheet}, nil
}
func (f *fakeStore) ListLines(_ context.Context, _ string) ([]Line, error) { return f.lines, nil }
func (f *fakeStore) ReasonCodes(_ context.Context) ([]ReasonCode, error)   { return f.reasons, nil }
func (f *fakeStore) CreateSheet(_ context.Context, branchID, scope, notes string) (Sheet, error) {
	f.sheet = Sheet{ID: "s1", SheetNo: "CS-1", BranchID: branchID, Scope: scope, Status: StatusOpen, Notes: notes}
	return f.sheet, nil
}
func (f *fakeStore) FreezeSheet(_ context.Context, _ string, snaps []Snapshot) error {
	f.sheet.Status = StatusFrozen
	for _, sn := range snaps {
		f.lines = append(f.lines, Line{ID: "l" + sn.ProductID, ProductID: sn.ProductID, LocationID: sn.LocationID, SnapshotQtyBase: sn.OnHandBase, UnitCostCents: sn.AvgCostCents})
	}
	return nil
}
func (f *fakeStore) SaveCounts(_ context.Context, _, countedBy string, lines []CountLineInput) error {
	f.sheet.Status = StatusCounted
	f.sheet.CountedBy = countedBy
	for _, in := range lines {
		found := false
		for i := range f.lines {
			if f.lines[i].ProductID == in.ProductID {
				c := in.CountedQtyBase
				f.lines[i].CountedQtyBase = &c
				f.lines[i].VarianceBase = in.CountedQtyBase - f.lines[i].SnapshotQtyBase
				f.lines[i].ReasonCode = in.ReasonCode
				found = true
			}
		}
		if !found {
			c := in.CountedQtyBase
			f.lines = append(f.lines, Line{ID: "l" + in.ProductID, ProductID: in.ProductID, LocationID: in.LocationID, CountedQtyBase: &c, VarianceBase: in.CountedQtyBase, ReasonCode: in.ReasonCode})
		}
	}
	return nil
}
func (f *fakeStore) ApproveSheet(_ context.Context, _, approvedBy string) error {
	f.sheet.ApprovedBy = approvedBy
	return nil
}
func (f *fakeStore) CancelSheet(_ context.Context, _ string) error {
	f.sheet.Status = StatusCancelled
	return nil
}
func (f *fakeStore) InTx(ctx context.Context, fn func(tx pgx.Tx) error) error { return fn(nil) }
func (f *fakeStore) LockSheetTx(_ context.Context, _ pgx.Tx, _ string) (Sheet, error) {
	return f.sheet, nil
}
func (f *fakeStore) LinesForPostTx(_ context.Context, _ pgx.Tx, _ string) ([]Line, error) {
	return f.lines, nil
}
func (f *fakeStore) UpdateLinePostedTx(_ context.Context, _ pgx.Tx, lineID string, unitCost, varVal int64, moveID string) error {
	for i := range f.lines {
		if f.lines[i].ID == lineID {
			f.lines[i].UnitCostCents = unitCost
			f.lines[i].VarianceValueCents = varVal
			f.lines[i].MoveID = moveID
		}
	}
	return nil
}
func (f *fakeStore) MarkSheetPostedTx(_ context.Context, _ pgx.Tx, _ string) error {
	f.sheet.Status = StatusPosted
	return nil
}
func (f *fakeStore) GetReconRun(_ context.Context, y, m int, b string) (ReconRun, bool, error) {
	r, ok := f.recon[reconKey(y, m, b)]
	return r, ok, nil
}
func (f *fakeStore) InsertReconRun(_ context.Context, in ReconRun) (ReconRun, error) {
	k := reconKey(in.FiscalYear, in.Month, in.BranchID)
	if r, ok := f.recon[k]; ok {
		return r, nil
	}
	in.ID = "recon-" + k
	f.recon[k] = in
	f.inserts++
	return in, nil
}
func (f *fakeStore) ListReconRuns(_ context.Context, _ string) ([]ReconRun, error) {
	out := []ReconRun{}
	for _, r := range f.recon {
		out = append(out, r)
	}
	return out, nil
}

type fakeStock struct {
	snaps    []Snapshot
	avg      int64
	moves    int
	lastQty  float64
	lastCost int64
}

func (s *fakeStock) SnapshotOnHand(_ context.Context, _ string) ([]Snapshot, error) {
	return s.snaps, nil
}
func (s *fakeStock) WeightedAvgCostCents(_ context.Context, _, _ string) (int64, error) {
	return s.avg, nil
}
func (s *fakeStock) WeightedAvgCostCentsTx(_ context.Context, _ pgx.Tx, _, _ string) (int64, error) {
	return s.avg, nil
}
func (s *fakeStock) PostCountAdjustmentTx(_ context.Context, _ pgx.Tx, _, _, _ string, qtyDelta float64, unitCostCents int64, _ string) (string, error) {
	s.moves++
	s.lastQty = qtyDelta
	s.lastCost = unitCostCents
	return "move-1", nil
}

type fakeGL struct {
	posted map[string]GLJournal
	count  int
}

func newFakeGL() *fakeGL { return &fakeGL{posted: map[string]GLJournal{}} }
func (g *fakeGL) PostJournal(_ context.Context, in GLJournal) (string, error) {
	if _, ok := g.posted[in.IdempotencyKey]; ok {
		return "j-" + in.IdempotencyKey, nil // dedup, no new journal
	}
	g.posted[in.IdempotencyKey] = in
	g.count++
	return "j-" + in.IdempotencyKey, nil
}

type fakeRule struct{ inv string }

func (r fakeRule) ResolveRule(_ context.Context, _, _, _ string) (string, error) { return r.inv, nil }

type fakeGLBal struct{ bal int64 }

func (b fakeGLBal) GLBalanceCents(_ context.Context, _ string, _, _ int, _ string) (int64, error) {
	return b.bal, nil
}

func (b fakeGLBal) GLBalanceThroughCents(_ context.Context, _ string, _, _ int, _ string) (int64, error) {
	return b.bal, nil
}

// - tests -

func TestValueVariance(t *testing.T) {
	cases := []struct {
		q    float64
		cost int64
		want int64
	}{
		{-3, 350, -1050},  // shrink of 3 @ $3.50
		{2, 800, 1600},    // found 2 @ $8.00
		{0, 500, 0},       // no variance
		{1.5, 333, 500},   // 1.5 × 333 = 499.5 → round 500
		{-2.5, 101, -253}, // -2.5 × 101 = -252.5 → round -253 (round half away)
	}
	for _, c := range cases {
		if got := valueVariance(c.q, c.cost); got != c.want {
			t.Errorf("valueVariance(%v,%d) = %d, want %d", c.q, c.cost, got, c.want)
		}
	}
}

func TestValidateMakerChecker(t *testing.T) {
	cases := []struct {
		name    string
		sheet   Sheet
		wantErr bool
		status  int
	}{
		{"posted replay ok", Sheet{Status: StatusPosted}, false, 0},
		{"not counted", Sheet{Status: StatusFrozen}, true, 409},
		{"no approver", Sheet{Status: StatusCounted, CountedBy: "m"}, true, 422},
		{"self-approve", Sheet{Status: StatusCounted, CountedBy: "m", ApprovedBy: "m"}, true, 422},
		{"valid four-eyes", Sheet{Status: StatusCounted, CountedBy: "m", ApprovedBy: "c"}, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateMakerChecker(c.sheet)
			if c.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr=%v", err, c.wantErr)
			}
			if c.wantErr {
				var ce clientError
				if !asClient(err, &ce) || ce.status != c.status {
					t.Fatalf("want status %d, got %v", c.status, err)
				}
			}
		})
	}
}

func TestBuildAdjustmentJournal_ReasonAccountMatrix(t *testing.T) {
	reasons := map[string]string{"DAMAGE": "5200", "FOUND": "5200", "SPOIL": "5900"}
	lines := []Line{
		{ProductID: "p1", ReasonCode: "DAMAGE", VarianceValueCents: -1050}, // loss: CR 1200, DR 5200
		{ProductID: "p2", ReasonCode: "FOUND", VarianceValueCents: 400},    // gain: DR 1200, CR 5200
		{ProductID: "p3", ReasonCode: "SPOIL", VarianceValueCents: -600},   // loss: CR 1200, DR 5900
		{ProductID: "p4", ReasonCode: "DAMAGE", VarianceValueCents: 0},     // skipped
	}
	j, err := buildAdjustmentJournal("s1", "CS-1", "br1", "1200", reasons, lines)
	if err != nil {
		t.Fatal(err)
	}
	// net inventory = -1050+400-600 = -1250 → CR 1200 1250
	// 5200 net = -(-1050) + -(400) = 1050-400 = 650 → DR 5200 650
	// 5900 net = -(-600) = 600 → DR 5900 600
	var dr, cr int64
	acct := map[string][2]int64{}
	for _, l := range j.Lines {
		dr += l.DebitCents
		cr += l.CreditCents
		acct[l.AccountCode] = [2]int64{l.DebitCents, l.CreditCents}
	}
	if dr != cr {
		t.Fatalf("journal not balanced: DR %d CR %d", dr, cr)
	}
	if acct["1200"] != [2]int64{0, 1250} {
		t.Errorf("1200 leg = %v, want CR 1250", acct["1200"])
	}
	if acct["5200"] != [2]int64{650, 0} {
		t.Errorf("5200 leg = %v, want DR 650", acct["5200"])
	}
	if acct["5900"] != [2]int64{600, 0} {
		t.Errorf("5900 leg = %v, want DR 600", acct["5900"])
	}
	if j.IdempotencyKey != "invcount.count:s1" {
		t.Errorf("idem key = %q", j.IdempotencyKey)
	}
	// first leg must be the inventory (1200) leg, deterministic ordering
	if j.Lines[0].AccountCode != "1200" {
		t.Errorf("inventory leg not first: %v", j.Lines)
	}
}

func TestBuildAdjustmentJournal_MissingReasonAccount(t *testing.T) {
	_, err := buildAdjustmentJournal("s1", "CS-1", "br1", "1200", map[string]string{}, []Line{
		{ProductID: "p1", ReasonCode: "DAMAGE", VarianceValueCents: -100},
	})
	if err == nil {
		t.Fatal("expected error for unmapped reason account")
	}
}

func TestPostCountedSheet_CrossModule_Idempotent(t *testing.T) {
	st := newFakeStore()
	st.sheet = Sheet{ID: "s1", SheetNo: "CS-1", BranchID: "br1", Status: StatusCounted, CountedBy: "maker", ApprovedBy: "checker"}
	st.lines = []Line{{ID: "l1", ProductID: "p1", LocationID: "loc1", SnapshotQtyBase: 10, VarianceBase: -3, ReasonCode: "DAMAGE"}}
	stock := &fakeStock{avg: 350}
	gp := newFakeGL()
	rr := fakeRule{inv: "1200"}

	// first post: writes 1 stock move + 1 GL journal, flips POSTED
	if err := postCountedSheet(context.Background(), st, stock, gp, rr, "s1"); err != nil {
		t.Fatalf("first post: %v", err)
	}
	if stock.moves != 1 {
		t.Fatalf("stock moves = %d, want 1", stock.moves)
	}
	if gp.count != 1 {
		t.Fatalf("gl journals = %d, want 1", gp.count)
	}
	if st.sheet.Status != StatusPosted {
		t.Fatalf("status = %q, want POSTED", st.sheet.Status)
	}
	// the move must carry the signed variance + weighted-avg cost
	if stock.lastQty != -3 || stock.lastCost != 350 {
		t.Fatalf("move qty/cost = %v/%d, want -3/350", stock.lastQty, stock.lastCost)
	}
	// the valued variance must be persisted on the line
	if st.lines[0].VarianceValueCents != -1050 {
		t.Fatalf("variance value = %d, want -1050", st.lines[0].VarianceValueCents)
	}

	// second post (replay): no new stock move, GL dedups → still 1 journal
	if err := postCountedSheet(context.Background(), st, stock, gp, rr, "s1"); err != nil {
		t.Fatalf("replay post: %v", err)
	}
	if stock.moves != 1 {
		t.Errorf("replay wrote extra stock moves: %d", stock.moves)
	}
	if gp.count != 1 {
		t.Errorf("replay posted a duplicate journal: %d", gp.count)
	}
}

func TestPostCountedSheet_MakerCheckerGate(t *testing.T) {
	st := newFakeStore()
	st.sheet = Sheet{ID: "s1", BranchID: "br1", Status: StatusCounted, CountedBy: "maker"} // no approver
	st.lines = []Line{{ID: "l1", ProductID: "p1", VarianceBase: -1, ReasonCode: "DAMAGE"}}
	err := postCountedSheet(context.Background(), st, &fakeStock{avg: 100}, newFakeGL(), fakeRule{inv: "1200"}, "s1")
	if err == nil {
		t.Fatal("expected maker-checker gate error")
	}
	var ce clientError
	if !asClient(err, &ce) || ce.status != 422 {
		t.Fatalf("want 422, got %v", err)
	}
	if st.sheet.Status == StatusPosted {
		t.Fatal("sheet posted despite failed gate")
	}
}

func TestReconcileMonth_LazyRunOnce(t *testing.T) {
	st := newFakeStore()
	stock := &fakeStock{snaps: []Snapshot{
		{ProductID: "p1", OnHandBase: 100, AvgCostCents: 350}, // 35000
		{ProductID: "p2", OnHandBase: 10, AvgCostCents: 8000}, // 80000
	}}
	glBal := fakeGLBal{bal: 115000} // ties exactly → TIED

	r1, err := reconcileMonth(context.Background(), st, stock, glBal, "u1", 2026, 7, "br1")
	if err != nil {
		t.Fatal(err)
	}
	if r1.SubledgerCents != 115000 {
		t.Errorf("subledger = %d, want 115000", r1.SubledgerCents)
	}
	if r1.Status != ReconTied || r1.DeltaCents != 0 {
		t.Errorf("status = %q delta = %d, want TIED/0", r1.Status, r1.DeltaCents)
	}
	if st.inserts != 1 {
		t.Fatalf("inserts = %d, want 1", st.inserts)
	}

	// second call for the same period: run-once → no new insert, same row
	r2, err := reconcileMonth(context.Background(), st, stock, glBal, "u1", 2026, 7, "br1")
	if err != nil {
		t.Fatal(err)
	}
	if st.inserts != 1 {
		t.Errorf("run-once violated: inserts = %d", st.inserts)
	}
	if r2.ID != r1.ID {
		t.Errorf("run-once returned a different row: %q vs %q", r2.ID, r1.ID)
	}
}

func TestReconcileMonth_OutOfTie(t *testing.T) {
	st := newFakeStore()
	stock := &fakeStock{snaps: []Snapshot{{ProductID: "p1", OnHandBase: 100, AvgCostCents: 350}}}
	glBal := fakeGLBal{bal: 30000} // subledger 35000 vs GL 30000 → delta 5000
	r, err := reconcileMonth(context.Background(), st, stock, glBal, "u1", 2026, 7, "br1")
	if err != nil {
		t.Fatal(err)
	}
	if r.DeltaCents != 5000 || r.Status != ReconOutOfTie {
		t.Errorf("delta = %d status = %q, want 5000/OUT_OF_TIE", r.DeltaCents, r.Status)
	}
}

// asClient is a tiny errors.As wrapper kept local so the test reads clearly.
func asClient(err error, target *clientError) bool {
	for err != nil {
		if ce, ok := err.(clientError); ok {
			*target = ce
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (s *fakeStock) SnapshotOnHandTx(ctx context.Context, _ pgx.Tx, branchID string) ([]Snapshot, error) {
	return s.SnapshotOnHand(ctx, branchID)
}
