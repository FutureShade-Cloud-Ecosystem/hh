//go:build integration

// Integration cover for the Wave 2 yard flow (migration 0095) against a real migrated Postgres.
// Run with: DATABASE_URL=... go test -tags integration -count=1 ./internal/tasks/
//
// What only a database can prove, and is therefore proved here rather than against the fake:
//   - the widened status CHECK actually admits STAGED (a Go-side constant is not a schema);
//   - the stage stamp, the task_events audit row and the StageOutbox notice commit TOGETHER, and a
//     failing enqueue takes the whole stage down rather than leaving a silent gap;
//   - the shortage counts on the task read back from the SQL sub-selects, not from a Go counter;
//   - the concurrent double-resolve loses on `resolved_at IS NULL`, in the database, under contention.
//
// FIXTURES RESTORE WHAT THEY TOUCH. Every test allocates its OWN synthetic order id (tasks.order_id
// carries no FK by design, 0073) and deletes every row it created, tasks, task_shortages and its own
// outbox rows, on cleanup. No seeded row is read or written, so these tests are order-independent
// and can run against a database another suite is also using.

package tasks

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// recordingStageOutbox is a StageOutbox that writes a real row through the SAME transaction it is
// handed. It inserts straight into sms_outbox (0097) with raw SQL rather than importing the SMS
// module, so this test proves TRANSACTION MEMBERSHIP, the thing only a database can show, without
// coupling the tasks suite to another package.
type recordingStageOutbox struct {
	notices []StagedNotice
	err     error // when set, the enqueue fails and the whole stage must roll back
}

func (o *recordingStageOutbox) EnqueueStagedTx(ctx context.Context, tx pgx.Tx, n StagedNotice) error {
	if o.err != nil {
		return o.err
	}
	o.notices = append(o.notices, n)
	_, err := tx.Exec(ctx,
		`INSERT INTO sms_outbox (topic, payload) VALUES ('sms.dispatch.notice', jsonb_build_object(
			'task_id', $1::text, 'order_id', $2::text, 'stage_ref', $3::text))`,
		n.TaskID, n.OrderID, n.StageRef)
	return err
}

// trentonBranch is the seeded Trenton branch uuid. It is used as a plain value (branch_id carries no
// FK either) so the test needs nothing from the branches fixture beyond a realistic-looking id.
const trentonBranch = "5d1b3f6e-0000-4000-9000-000000000001"

func yardPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("no database: %v", err)
		return nil
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("database unreachable: %v", err)
		return nil
	}
	t.Cleanup(pool.Close)
	return pool
}

// newYardFixture builds a service over the real repository bound to a per-test synthetic order, and
// registers the cleanup that removes every row the test can create.
func newYardFixture(t *testing.T) (*Service, *fakeNotifier, *pgxpool.Pool, string, *recordingStageOutbox) {
	t.Helper()
	pool := yardPool(t)
	ctx := context.Background()

	var orderID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&orderID); err != nil {
		t.Fatalf("mint order id: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM task_shortages WHERE task_id IN (SELECT id FROM tasks WHERE order_id = $1::uuid)`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM task_events   WHERE task_id IN (SELECT id FROM tasks WHERE order_id = $1::uuid)`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM tasks         WHERE order_id = $1::uuid`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM sms_outbox WHERE payload->>'order_id' = $1`, orderID)
	})

	notifier := &fakeNotifier{}
	outbox := &recordingStageOutbox{}
	svc := NewService(NewRepository(pool).WithStageOutbox(outbox)).
		WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{
			orderID: {ID: orderID, BranchID: trentonBranch, Fulfillment: "PICKUP", Status: "CONFIRMED"},
		}}).
		WithChatNotifier(notifier).
		WithPickSource(NewStubPickSource())
	return svc, notifier, pool, orderID, outbox
}

// pickToInProgress drives a real DB-backed task to IN_PROGRESS.
func pickToInProgress(t *testing.T, svc *Service, orderID string) Task {
	t.Helper()
	ctx := context.Background()
	task, err := svc.CreateFromOrder(ctx, CreateInput{OrderID: orderID, Source: SourceManual})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Claim(ctx, task.ID, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	got, err := svc.Accept(ctx, task.ID, "")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	return got
}

func TestIntegrationStageWritesStampAuditAndOutboxInOneTx(t *testing.T) {
	svc, notifier, pool, orderID, outbox := newYardFixture(t)
	ctx := context.Background()
	task := pickToInProgress(t, svc, orderID)

	staged, err := svc.Stage(ctx, StageInput{TaskID: task.ID, Location: "Bay 3", Ref: "STG-TEST1"})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if staged.Status != StatusStaged || staged.StagedAt == nil {
		t.Fatalf("staged task = %+v", staged)
	}

	// The row really is STAGED in the database, i.e. the 0095 CHECK admits it.
	var dbStatus, dbLoc, dbRef string
	if err := pool.QueryRow(ctx,
		`SELECT status, COALESCE(stage_location,''), COALESCE(stage_ref,'') FROM tasks WHERE id = $1`,
		task.ID).Scan(&dbStatus, &dbLoc, &dbRef); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if dbStatus != "STAGED" || dbLoc != "Bay 3" || dbRef != "STG-TEST1" {
		t.Fatalf("db row = (%s, %s, %s)", dbStatus, dbLoc, dbRef)
	}

	// The audit edge exists, with the true pre-image.
	var from, to, note string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(from_status,''), to_status, note FROM task_events
		 WHERE task_id = $1 AND to_status = 'STAGED'`, task.ID).Scan(&from, &to, &note); err != nil {
		t.Fatalf("stage audit row missing: %v", err)
	}
	if from != "IN_PROGRESS" {
		t.Fatalf("audit from_status = %q", from)
	}
	if !strings.Contains(note, "Bay 3") {
		t.Fatalf("audit note = %q", note)
	}

	// And the out-of-band notice the StageOutbox seam wrote THROUGH THE STAGE TRANSACTION.
	if len(outbox.notices) != 1 || outbox.notices[0].StageRef != "STG-TEST1" {
		t.Fatalf("stage notices = %+v", outbox.notices)
	}
	var payloadOrder, payloadRef, status string
	if err := pool.QueryRow(ctx,
		`SELECT payload->>'order_id', payload->>'stage_ref', status FROM sms_outbox
		 WHERE payload->>'task_id' = $1`, task.ID).
		Scan(&payloadOrder, &payloadRef, &status); err != nil {
		t.Fatalf("staged notice row missing: %v", err)
	}
	if payloadOrder != orderID || payloadRef != "STG-TEST1" || status != "PENDING" {
		t.Fatalf("notice payload = (%s, %s, %s)", payloadOrder, payloadRef, status)
	}

	if got := lastPost(t, notifier); got.Audience != AudienceCounter {
		t.Fatalf("stage EVENT audience = %q", got.Audience)
	}
}

func TestIntegrationStageIsRejectedFromAnIllegalStatus(t *testing.T) {
	svc, _, pool, orderID, _ := newYardFixture(t)
	ctx := context.Background()
	task, err := svc.CreateFromOrder(ctx, CreateInput{OrderID: orderID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Stage(ctx, StageInput{TaskID: task.ID, Location: "Bay 3"}); err == nil {
		t.Fatal("stage from QUEUED succeeded")
	}
	// Nothing was written: no stamp, no audit edge, no notice.
	var events, notices int
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT COUNT(*) FROM task_events WHERE task_id = $1 AND to_status = 'STAGED'),
		        (SELECT COUNT(*) FROM sms_outbox WHERE payload->>'task_id' = $1::text)`,
		task.ID).Scan(&events, &notices); err != nil {
		t.Fatalf("count: %v", err)
	}
	if events != 0 || notices != 0 {
		t.Fatalf("rejected stage left rows behind: %d events, %d notices", events, notices)
	}
}

func TestIntegrationStageRollsBackWhenTheNoticeCannotBeQueued(t *testing.T) {
	// The notice is not "best effort", it rides the stage transaction. If it cannot be queued, the
	// pick must NOT read STAGED, because the counter would then be waiting on a text that will never
	// arrive with no record that anything went wrong.
	svc, _, pool, orderID, outbox := newYardFixture(t)
	ctx := context.Background()
	task := pickToInProgress(t, svc, orderID)

	boom := errors.New("notice queue unavailable")
	outbox.err = boom

	if _, err := svc.Stage(ctx, StageInput{TaskID: task.ID, Location: "Bay 9"}); !errors.Is(err, boom) {
		t.Fatalf("stage err = %v, want the enqueue failure surfaced", err)
	}
	var status string
	var stagedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, staged_at FROM tasks WHERE id = $1`, task.ID).
		Scan(&status, &stagedAt); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != StatusInProgress || stagedAt != nil {
		t.Fatalf("task = (%s, %v), the stage must have rolled back entirely", status, stagedAt)
	}
	var events int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM task_events WHERE task_id = $1 AND to_status = 'STAGED'`, task.ID).Scan(&events); err != nil {
		t.Fatalf("count: %v", err)
	}
	if events != 0 {
		t.Fatalf("rolled-back stage left %d audit rows", events)
	}
}

func TestIntegrationShortageCountsComeFromTheDatabase(t *testing.T) {
	svc, _, pool, orderID, _ := newYardFixture(t)
	ctx := context.Background()
	task := pickToInProgress(t, svc, orderID)

	first, err := svc.FlagShortage(ctx, ShortageInput{
		TaskID: task.ID, Description: "Holland paver charcoal", QtyShort: "2.5", UOM: "PALLET", Note: "yard is out",
	})
	if err != nil {
		t.Fatalf("flag: %v", err)
	}
	if _, err := svc.FlagShortage(ctx, ShortageInput{
		TaskID: task.ID, Description: "HPB sand", QtyShort: "1", UOM: "TONNE",
	}); err != nil {
		t.Fatalf("flag 2: %v", err)
	}

	detail, err := svc.PickDetail(ctx, task.ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.Task.OpenShortages != 2 || detail.Task.TotalShortages != 2 {
		t.Fatalf("counts = (%d open, %d total), want (2, 2)", detail.Task.OpenShortages, detail.Task.TotalShortages)
	}
	if detail.Ticket == nil || detail.Ticket.Source != "STUB" {
		t.Fatalf("detail ticket = %+v, want a STUB-labelled fixture ticket", detail.Ticket)
	}
	if len(detail.Shortages) != 2 {
		t.Fatalf("detail shortages = %d", len(detail.Shortages))
	}
	// NUMERIC(19,4) round-trips as the string the counter reads out.
	if detail.Shortages[0].QtyShort != "2.5000" {
		t.Fatalf("qty_short round-trip = %q", detail.Shortages[0].QtyShort)
	}

	if _, err := svc.ResolveShortage(ctx, ResolveShortageInput{
		TaskID: task.ID, ShortageID: first.ID, Resolution: "back-ordered for Thursday",
	}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	after, err := svc.PickDetail(ctx, task.ID)
	if err != nil {
		t.Fatalf("detail 2: %v", err)
	}
	if after.Task.OpenShortages != 1 || after.Task.TotalShortages != 2 {
		t.Fatalf("counts after resolve = (%d, %d), want (1, 2)", after.Task.OpenShortages, after.Task.TotalShortages)
	}

	// The resolution is a stamp on the original report, not a replacement of it.
	var desc, resolution string
	if err := pool.QueryRow(ctx,
		`SELECT description, resolution FROM task_shortages WHERE id = $1`, first.ID).Scan(&desc, &resolution); err != nil {
		t.Fatalf("read shortage: %v", err)
	}
	if desc != "Holland paver charcoal" || resolution != "back-ordered for Thursday" {
		t.Fatalf("row = (%q, %q)", desc, resolution)
	}
}

func TestIntegrationConcurrentResolveHasExactlyOneWinner(t *testing.T) {
	svc, _, _, orderID, _ := newYardFixture(t)
	ctx := context.Background()
	task := pickToInProgress(t, svc, orderID)
	sh, err := svc.FlagShortage(ctx, ShortageInput{TaskID: task.ID, Description: "edge restraint", QtyShort: "12", UOM: "EA"})
	if err != nil {
		t.Fatalf("flag: %v", err)
	}

	const racers = 6
	var wg sync.WaitGroup
	results := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = svc.ResolveShortage(context.Background(), ResolveShortageInput{
				TaskID: task.ID, ShortageID: sh.ID, Resolution: "cleared by racer",
			})
		}(i)
	}
	close(start)
	wg.Wait()

	won := 0
	for _, err := range results {
		if err == nil {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d/%d concurrent resolves succeeded, want exactly 1 (the first resolver's name must stand)", won, racers)
	}
}

func TestIntegrationStaleSweepReadsOnlyOldQueuedPicks(t *testing.T) {
	svc, notifier, pool, orderID, _ := newYardFixture(t)
	ctx := context.Background()

	fresh, err := svc.CreateFromOrder(ctx, CreateInput{OrderID: orderID})
	if err != nil {
		t.Fatalf("create fresh: %v", err)
	}
	old, err := svc.CreateFromOrder(ctx, CreateInput{OrderID: orderID})
	if err != nil {
		t.Fatalf("create old: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET created_at = now() - interval '30 hours' WHERE id = $1`, old.ID); err != nil {
		t.Fatalf("age task: %v", err)
	}

	stale, err := NewRepository(pool).StalePickTasks(ctx, time.Now().Add(-12*time.Hour), 100)
	if err != nil {
		t.Fatalf("stale read: %v", err)
	}
	sawOld, sawFresh := false, false
	for _, s := range stale {
		if s.ID == old.ID {
			sawOld = true
		}
		if s.ID == fresh.ID {
			sawFresh = true
		}
	}
	if !sawOld {
		t.Fatal("aged QUEUED pick was not swept")
	}
	if sawFresh {
		t.Fatal("a task created seconds ago was reported stale")
	}

	// The sweep only speaks; both tasks are still exactly where they were.
	before := len(notifier.posts)
	if _, _, err := svc.SweepStalePicks(ctx, 12*time.Hour, 500); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(notifier.posts) <= before {
		t.Fatal("sweep posted nothing for an aged pick")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id = $1`, old.ID).Scan(&status); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != StatusQueued {
		t.Fatalf("sweep moved a task to %q, it must never mutate", status)
	}
}
