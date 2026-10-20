//go:build integration

package assist

// spend_integration_test.go, slice 2 against a real migrated Postgres (migration 0417). What only a
// database proves: the append only triggers, the CHECK and UNIQUE constraints, the SQL fold, the
// half open month, the sweep end to end, and the price row in force. Fixture rows sit in years far
// from any real month, one month per test, so no test sees another's rows.

import (
	"context"
	"testing"
	"time"
)

// itYear is drawn once per run, so a rerun on the same database (append only rows cannot be cleaned
// up) never sees an earlier run's rows.
var itYear = 3000 + int(time.Now().UnixNano()%5000)

func spendMonth(_ int, month time.Month) (time.Time, time.Time, time.Time) {
	at := time.Date(itYear, month, 10, 12, 0, 0, 0, time.UTC)
	from, to := MonthWindow(at)
	return from, to, at
}

func srow(kind SpendKind, ex string, call int, aud Audience, staff, portal string, at time.Time) SpendRow {
	return SpendRow{Kind: kind, ExchangeID: ex, CallNo: call, Audience: aud, StaffUserID: staff, PortalUserID: portal,
		RoleCode: "SALES_MANAGER", Alias: "it-alias", Model: "it-model", At: at}
}

func TestIntegration_SpendTablesAreAppendOnly(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	_, _, at := spendMonth(2032, 1)
	if err := repo.Reserve(ctx, srow(SpendReservation, newUUID(), 1, AudienceStaff, user, "", at)); err != nil {
		t.Fatal(err)
	}
	alias := "it-append-" + newUUID()
	if _, err := pool.Exec(ctx, `INSERT INTO assist_model_prices (alias, model, price_in_micro_per_m, price_out_micro_per_m, price_cached_micro_per_m, effective_seq)
		VALUES ($1, 'm', 1, 1, 1, 1)`, alias); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE assist_spend SET tokens_in = 1 WHERE staff_user_id = '` + user + `'`,
		`DELETE FROM assist_spend WHERE staff_user_id = '` + user + `'`,
		`TRUNCATE assist_spend`,
		`UPDATE assist_model_prices SET price_in_micro_per_m = 9 WHERE alias = '` + alias + `'`,
		`DELETE FROM assist_model_prices WHERE alias = '` + alias + `'`,
		`TRUNCATE assist_model_prices`,
	} {
		if _, err := pool.Exec(ctx, stmt); err == nil {
			t.Errorf("%q was accepted: the table is not append only", stmt)
		}
	}
}

func TestIntegration_SpendConstraintsMirrorValidate(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	_, _, at := spendMonth(2032, 2)
	ex := newUUID()
	insert := func(kind, aud, staff, portal, settledBy string, call int) error {
		_, err := pool.Exec(ctx, `INSERT INTO assist_spend (kind, exchange_id, call_no, audience, staff_user_id, portal_user_id, role_code, alias, model, settled_by, at)
			VALUES ($1, $2::uuid, $3, $4, NULLIF($5,'')::uuid, NULLIF($6,'')::uuid, 'R', 'a', 'm', $7, $8)`, kind, ex, call, aud, staff, portal, settledBy, at)
		return err
	}
	bad := map[string]error{
		"staff without a user":        insert("reservation", "staff", "", "", "", 1),
		"staff with both":             insert("reservation", "staff", user, newUUID(), "", 2),
		"pro with a staff user":       insert("reservation", "pro", user, "", "", 3),
		"embed with a user":           insert("reservation", "embed", user, "", "", 4),
		"unknown audience":            insert("reservation", "web", user, "", "", 5),
		"unknown kind":                insert("refund", "staff", user, "", "", 6),
		"call number zero":            insert("reservation", "staff", user, "", "", 0),
		"sweep mark on a reservation": insert("reservation", "staff", user, "", "sweep", 7),
		"unknown settled_by":          insert("settlement", "staff", user, "", "operator", 8),
	}
	for name, err := range bad {
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	for name, err := range map[string]error{
		"staff":            insert("reservation", "staff", user, "", "", 11),
		"messages":         insert("reservation", "messages", user, "", "", 12),
		"pro":              insert("reservation", "pro", "", newUUID(), "", 13),
		"embed":            insert("reservation", "embed", "", "", "", 14),
		"sweep settlement": insert("settlement", "staff", user, "", "sweep", 15),
	} {
		if err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
	// A call is settled at most once.
	if err := insert("settlement", "staff", user, "", "", 11); err != nil {
		t.Fatal(err)
	}
	if err := insert("settlement", "staff", user, "", "", 11); err == nil {
		t.Error("a second settlement of one call was accepted")
	}
}

func TestIntegration_FoldCountsOpenAtEstimateSettledAsActualAndVoidAsNothing(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	from, to, at := spendMonth(2032, 3)

	mk := func(kind SpendKind, ex string, in, out int, cost int64, when time.Time) SpendRow {
		r := srow(kind, ex, 1, AudienceStaff, user, "", when)
		r.TokensIn, r.TokensOut, r.CostMicroCents = in, out, cost
		return r
	}
	open, settled, voided := newUUID(), newUUID(), newUUID()
	for _, r := range []SpendRow{
		mk(SpendReservation, open, 100, 1024, 50, at),
		mk(SpendReservation, settled, 100, 1024, 50, at),
		mk(SpendSettlement, settled, 30, 7, 9, at),
		mk(SpendReservation, voided, 100, 1024, 50, at),
		mk(SpendVoid, voided, 0, 0, 0, at),
	} {
		var err error
		if r.Kind == SpendReservation {
			err = repo.Reserve(ctx, r)
		} else if r.Kind == SpendSettlement {
			err = repo.Settle(ctx, r)
		} else {
			err = repo.Void(ctx, r)
		}
		if err != nil {
			t.Fatalf("%s: %v", r.Kind, err)
		}
	}
	got, err := repo.UserMonth(ctx, user, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if got.Exchanges != 2 || got.TokensIn != 130 || got.TokensOut != 1031 {
		t.Fatalf("user month = %+v, want 2 exchanges, 130 in, 1031 out (open estimate plus settled actual, void nothing)", got)
	}
	dealer, err := repo.DealerMonthMicroCents(ctx, from, to)
	if err != nil || dealer != 59 {
		t.Fatalf("dealer month = %d (%v), want 59", dealer, err)
	}
	all, err := repo.UsersMonth(ctx, from, to)
	if err != nil || all[user].TokensIn != 130 {
		t.Fatalf("users month = %+v (%v)", all[user], err)
	}
}

func TestIntegration_TheMonthIsHalfOpenAndACallNeverStraddlesIt(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	from, to, _ := spendMonth(2032, 4)
	last := to.Add(-time.Nanosecond)
	r := srow(SpendReservation, newUUID(), 1, AudienceStaff, user, "", last)
	r.TokensIn = 5
	if err := repo.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	next := srow(SpendReservation, newUUID(), 1, AudienceStaff, user, "", to)
	next.TokensIn = 7
	if err := repo.Reserve(ctx, next); err != nil {
		t.Fatal(err)
	}
	a, _ := repo.UserMonth(ctx, user, from, to)
	if a.TokensIn != 5 {
		t.Fatalf("April = %+v, want only the row inside the window", a)
	}
	// A settlement written the next month carries the reservation's at and so stays in April.
	s := r
	s.Kind, s.TokensIn = SpendSettlement, 4
	if err := repo.Settle(ctx, s); err != nil {
		t.Fatal(err)
	}
	a, _ = repo.UserMonth(ctx, user, from, to)
	if a.TokensIn != 4 {
		t.Fatalf("April after settlement = %+v, want 4 (replaced, not added)", a)
	}
}

func TestIntegration_CapSeesEveryAudience(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	from, to, at := spendMonth(2032, 5)
	for i, r := range []SpendRow{
		srow(SpendSettlement, newUUID(), 1, AudienceStaff, user, "", at),
		srow(SpendSettlement, newUUID(), 1, AudiencePro, "", newUUID(), at),
		srow(SpendSettlement, newUUID(), 1, AudienceEmbed, "", "", at),
		srow(SpendSettlement, newUUID(), 1, AudienceMessages, user, "", at),
	} {
		r.CostMicroCents = 100
		if err := repo.Settle(ctx, r); err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
	}
	if got, _ := repo.DealerMonthMicroCents(ctx, from, to); got != 400 {
		t.Fatalf("dealer month = %d, want 400 across the four audiences", got)
	}
}

func TestIntegration_SweepSettlesAtTheEstimateAndKeepsTheSpend(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	from, to, at := spendMonth(2032, 6)

	lost, answered := newUUID(), newUUID()
	res := srow(SpendReservation, lost, 1, AudienceStaff, user, "", at)
	res.TokensIn, res.TokensOut, res.CostMicroCents = 40, 1024, 77
	if err := repo.Reserve(ctx, res); err != nil {
		t.Fatal(err)
	}
	ans := srow(SpendReservation, answered, 1, AudienceStaff, user, "", at)
	if err := repo.Reserve(ctx, ans); err != nil {
		t.Fatal(err)
	}
	ans.Kind = SpendSettlement
	if err := repo.Settle(ctx, ans); err != nil {
		t.Fatal(err)
	}
	before, _ := repo.DealerMonthMicroCents(ctx, from, to)

	n, err := SweepReservations(ctx, repo, at.Add(time.Hour), 15*time.Minute)
	if err != nil || n < 1 {
		t.Fatalf("swept %d (%v), want at least the lost call", n, err)
	}
	after, _ := repo.DealerMonthMicroCents(ctx, from, to)
	if after != before {
		t.Fatalf("dealer spend moved from %d to %d across the sweep", before, after)
	}
	rows, err := repo.Settled(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	var swept *SpendRow
	for i := range rows {
		if rows[i].ExchangeID == lost {
			swept = &rows[i]
		}
	}
	if swept == nil || swept.SettledBy != SettledBySweep || swept.TokensOut != 1024 || swept.CostMicroCents != 77 || !swept.At.Equal(at) {
		t.Fatalf("lost call's settlement = %+v", swept)
	}
	if n2, _ := SweepReservations(ctx, repo, at.Add(time.Hour), 15*time.Minute); n2 != 0 {
		t.Fatalf("second sweep settled %d, want 0", n2)
	}
	// Younger than the grace: left alone.
	young := srow(SpendReservation, newUUID(), 1, AudienceStaff, user, "", at)
	if err := repo.Reserve(ctx, young); err != nil {
		t.Fatal(err)
	}
	if n3, _ := SweepReservations(ctx, repo, at.Add(time.Minute), 15*time.Minute); n3 != 0 {
		t.Fatalf("sweep took a reservation inside the grace: %d", n3)
	}
}

func TestIntegration_PriceInForceIsTheHighestSeq(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	alias := "it-price-" + newUUID()
	for seq, in := range map[int]int{1: 10, 3: 30, 2: 20} {
		if _, err := pool.Exec(ctx, `INSERT INTO assist_model_prices (alias, model, price_in_micro_per_m, price_out_micro_per_m, price_cached_micro_per_m, effective_seq)
			VALUES ($1, 'm', $2, 1, 1, $3)`, alias, in, seq); err != nil {
			t.Fatal(err)
		}
	}
	p, ok, err := repo.PriceFor(ctx, alias, "m")
	if err != nil || !ok || p.InMicroPerM != 30 || p.Seq != 3 || p.ID == "" {
		t.Fatalf("price = %+v ok=%v err=%v, want seq 3", p, ok, err)
	}
	if _, ok, _ := repo.PriceFor(ctx, alias, "other"); ok {
		t.Fatal("a price was found for a model that has none")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assist_model_prices (alias, model, price_in_micro_per_m, price_out_micro_per_m, price_cached_micro_per_m, effective_seq)
		VALUES ($1, 'm', 1, 1, 1, 3)`, alias); err == nil {
		t.Error("a duplicate (alias, model, seq) was accepted")
	}
}

// The whole path on real tables: complete reserves, calls, settles at the row in force, and the
// export reads the same sums.
func TestIntegration_CompleteAndExportOnRealTables(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	alias, model := "it-e2e-"+newUUID(), "claude-it"
	if _, err := pool.Exec(ctx, `INSERT INTO assist_model_prices (alias, model, price_in_micro_per_m, price_out_micro_per_m, price_cached_micro_per_m, effective_seq)
		VALUES ($1, $2, 3000000, 15000000, 300000, 1)`, alias, model); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(itYear, 7, 15, 9, 0, 0, 0, time.UTC)
	p := &namedProvider{name: alias, model: model, reply: Reply{Text: "ok", TokensIn: 1000, TokensOut: 200, TokensCached: 50}}
	svc := NewService(NewRegistry(seededReaders().deps(), nil), p, &fakeLedger{}, newFakeLimits(), StaticSettings{}, nil).
		WithClock(func() time.Time { return clock }).
		WithMetering(repo, repo).
		WithDealerCap(1 << 60)
	c := manager()
	c.UserID = user
	if _, err := svc.complete(ctx, c, CompletionRequest{Messages: []Message{{Role: RoleUser, Text: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	from, to := MonthWindow(clock)
	rows, err := repo.Settled(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	exp := BuildBillingExport("hh-it", "x-07", rows)
	// 1000*3 + 200*15 + 50*0.3 = 3000+3000+15 micro cents
	if exp.Summary.Requests != 1 || exp.Summary.CostMicroCents != 6015 || exp.Summary.TokensCached != 50 {
		t.Fatalf("summary = %+v", exp.Summary)
	}
	got, _ := repo.UserMonth(ctx, user, from, to)
	if got.TokensIn != 1000 || got.TokensOut != 200 {
		t.Fatalf("user month = %+v", got)
	}
}

type namedProvider struct {
	name, model string
	reply       Reply
}

func (p *namedProvider) Name() string  { return p.name }
func (p *namedProvider) Model() string { return p.model }
func (p *namedProvider) Complete(context.Context, CompletionRequest) (Reply, error) {
	return p.reply, nil
}

func TestIntegration_TheSweepJobHasItsScheduleRow(t *testing.T) {
	pool := testPool(t)
	var kind string
	var secs *int
	var enabled bool
	err := pool.QueryRow(context.Background(), `SELECT kind, interval_secs, enabled FROM job_schedules WHERE name = $1`, SpendSweepJobName).Scan(&kind, &secs, &enabled)
	if err != nil || kind != "INTERVAL" || secs == nil || *secs != 300 || !enabled {
		t.Fatalf("schedule row = %s %v %v (%v)", kind, secs, enabled, err)
	}
}

func TestIntegration_ProCapsTableExistsAndIsUnused(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO assist_pro_caps (scope, subject_id, monthly_cap_micro_cents) VALUES ('bogus', gen_random_uuid(), 1)`); err == nil {
		t.Error("an unknown pro cap scope was accepted")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assist_pro_caps (scope, subject_id, monthly_cap_micro_cents) VALUES ('account', gen_random_uuid(), -1)`); err == nil {
		t.Error("a negative pro cap was accepted")
	}
}
