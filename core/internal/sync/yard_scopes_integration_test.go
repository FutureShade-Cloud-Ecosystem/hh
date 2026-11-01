//go:build integration

// yard_scopes_integration_test.go: the yard-pick scopes of HH-3.5 #166
// against a real database: the per-scope contract battery, GATE-7 and
// the replay property for shortages (manual), weights (server_wins) and
// attachments (last_write_wins_by_field), the manual policy's
// never-overwrite and review marker legs, the weights seq race, the org
// and tenant isolation legs, and the shortage walkthrough scripted step
// by step. The stores under test are the owning modules' own
// (tasks.ShortageSyncStore, tasks.WeightSyncStore, attachments.SyncStore);
// the desk edits run through the real tasks service and the attachments
// module's own desk path.
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:55439/hh35sync166?sslmode=disable' \
//	  go test -tags=integration -count=1 ./internal/sync/...
//
// The database is a throwaway scratch, never a shared proof database.
package sync_test

import (
	"context"
	"fmt"
	"math/rand"
	stdsync "sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/attachments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/harness"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// yardFixture is the three yard scopes wired exactly as the composition
// root wires them: the owning modules' stores over the scratch pool and
// the engine with the real audit seam.
type yardFixture struct {
	pool       *pgxpool.Pool
	shortages  *tasks.ShortageSyncStore
	weights    *tasks.WeightSyncStore
	attach     *attachments.SyncStore
	engine     *sync.Engine
	svc        *tasks.Service
	filter     sync.ScopeFilter
	south      sync.ScopeFilter
	tenantFilt sync.ScopeFilter
}

// newYard builds the fixture on the scratch database, cleaning this
// lane's rows after the pilot's own cleanup discipline (the tasks rows
// and their cascades go through intPool's own cleanup).
func newYard(t *testing.T) *yardFixture {
	t.Helper()
	pool := intPool(t)
	auditSince := auditMark(t, pool)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx,
			`DELETE FROM attachment_link WHERE attachment_id IN (SELECT id FROM attachment WHERE org_id IN ($1::uuid, $2::uuid))`,
			testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = pool.Exec(ctx, `DELETE FROM attachment WHERE org_id IN ($1::uuid, $2::uuid)`,
			testkit.OrgNorthID, testkit.OrgSouthID)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_sequences WHERE scope IN ('shortages', 'weights', 'attachments')`)
		deleteOwnAudit(t, pool, auditSince, "sync.shortages", "sync.weights", "sync.attachments")
	})
	reader := intOrderReader{repo: order.NewRepository(pool)}
	linker := linkedUser(pool)
	shortages := tasks.NewShortageSyncStore(pool, linker)
	weights := tasks.NewWeightSyncStore(pool, reader, linker)
	attach := attachments.NewSyncStore(pool)
	engine := sync.NewEngine().WithAudit(intAudit{svc: audit.NewService(audit.NewRepository(pool), nil)}, nil)
	for name, store := range map[string]sync.Store{
		"shortages": shortages, "weights": weights, "attachments": attach,
	} {
		if err := engine.RegisterScope(name, store); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	return &yardFixture{
		pool:       pool,
		shortages:  shortages,
		weights:    weights,
		attach:     attach,
		engine:     engine,
		svc:        tasks.NewService(tasks.NewRepository(pool)).WithOrderReader(reader),
		filter:     sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales},
		south:      sync.ScopeFilter{OrgID: testkit.OrgSouthID, Actor: testkit.SubInsideSales},
		tenantFilt: sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: testkit.SubPortalAdminAlpha},
	}
}

// info resolves one scope's registry row or fails the test.
func info(t *testing.T, name string) sync.ScopeInfo {
	t.Helper()
	i, ok := sync.Lookup(name)
	if !ok {
		t.Fatalf("scope %s missing from the registry", name)
	}
	return i
}

// northPick queues one PICK task on the alpha picking order through the
// real service, the parent the shortage kits report against.
func (f *yardFixture) northPick(t *testing.T) tasks.Task {
	t.Helper()
	task, err := f.svc.CreateFromOrder(context.Background(), tasks.CreateInput{
		OrderID:   testkit.OrderAlphaPickID,
		Source:    tasks.SourceManual,
		CreatedBy: testkit.UserInsideSalesID,
		OrgID:     testkit.OrgNorthID,
	})
	if err != nil {
		t.Fatalf("desk create pick: %v", err)
	}
	return task
}

// shortageKit is the battery kit: creates report against the parent
// pick, updates carry the crew's correction, the desk clears the
// shortage through the real resolve path.
func shortageKit(t *testing.T, f *yardFixture, parent tasks.Task) harness.Kit {
	return harness.Kit{
		Scope:  info(t, "shortages"),
		Filter: f.filter,
		Store:  f.shortages,
		Create: func(id string) map[string]any {
			fields := map[string]any{
				"task_id":     parent.ID,
				"description": "short two bundles on line one",
				"qty_short":   "2",
				"uom":         "EA",
			}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "note": "recounted at the pallet"}
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			_, err := f.svc.ResolveShortage(ctx, tasks.ResolveShortageInput{
				TaskID:     fmt.Sprint(row["task_id"]),
				ShortageID: fmt.Sprint(row["id"]),
				ActorID:    testkit.UserInsideSalesID,
				Resolution: "substituted an equivalent bundle",
			})
			return err
		},
	}
}

// weightKit: creates are the counter's weight-request shape against the
// alpha order's line one; updates carry the yard's answer (the weight
// closes the task through the module's one-tap walk) or a stamp-only
// touch on a closed row; the desk moves the task through the real
// service transitions, falling back to a seq-drawing stamp on a terminal
// row exactly as the pilot's desk hook does.
func weightKit(t *testing.T, f *yardFixture) harness.Kit {
	statusOf := func(ctx context.Context, id string) (string, error) {
		var status string
		err := f.pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id = $1::uuid`, id).Scan(&status)
		return status, err
	}
	return harness.Kit{
		Scope:  info(t, "weights"),
		Filter: f.filter,
		Store:  f.weights,
		Create: func(id string) map[string]any {
			fields := map[string]any{"order_id": testkit.OrderAlphaPickID, "order_line_no": 1}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			if w, _ := row["weight_entered"].(string); w == "" {
				return map[string]any{"id": row["id"], "weight_entered": "2.5", "weight_uom": "TONNE"}
			}
			return map[string]any{"id": row["id"], "photo_url": "fx://scale/ticket-9"}
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			id := fmt.Sprint(row["id"])
			status, err := statusOf(ctx, id)
			if err != nil {
				return err
			}
			switch status {
			case tasks.StatusQueued, tasks.StatusPushed:
				_, err := f.svc.Claim(ctx, id, testkit.UserInsideSalesID)
				return err
			case tasks.StatusAccepted:
				_, err := f.svc.Accept(ctx, id, testkit.UserInsideSalesID)
				return err
			case tasks.StatusInProgress, tasks.StatusStaged:
				_, err := f.svc.Done(ctx, id, testkit.UserInsideSalesID, "")
				return err
			}
			// Terminal: the desk stamps a field and draws a seq from the
			// scope's own allocator, the same upsert every tasks write
			// path runs (the pilot's own terminal arm).
			var seq int64
			if err := f.pool.QueryRow(ctx,
				`INSERT INTO scope_sequences (scope) VALUES ('weights')
				 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
				 RETURNING next_value`).Scan(&seq); err != nil {
				return err
			}
			_, err = f.pool.Exec(ctx,
				`UPDATE tasks SET photo_url = 'FX Desk', updated_at = now(), seq = $2 WHERE id = $1::uuid`, id, seq)
			return err
		},
	}
}

// attachmentKit: creates register a photo whose bytes already landed in
// Appwrite Storage, linked to the alpha order as evidence; updates
// correct the metadata legs the merge policy owns; the desk corrects the
// legs the device does NOT declare (size and links), the write a stale
// device merge must keep.
func attachmentKit(t *testing.T, f *yardFixture) harness.Kit {
	return harness.Kit{
		Scope:  info(t, "attachments"),
		Filter: f.filter,
		Store:  f.attach,
		Create: func(id string) map[string]any {
			fields := map[string]any{
				"kind":             "photo",
				"appwrite_file_id": "fxfile-" + uuid.NewString()[:8],
				"bucket":           "hh-attachments",
				"mime":             "image/jpeg",
				"size_bytes":       float64(2048),
				"checksum":         "sha256:fixture",
				"links": []any{
					map[string]any{"owner_kind": "order", "owner_id": testkit.OrderAlphaPickID, "role": "evidence"},
				},
			}
			if id != "" {
				fields["id"] = id
			}
			return fields
		},
		Update: func(row map[string]any) map[string]any {
			return map[string]any{"id": row["id"], "mime": "image/heic", "checksum": "sha256:recounted"}
		},
		// The chain evolves the mime leg: this module's replay idempotency
		// rests on the identical-write no-op (the frozen schema gives it
		// no applied-key history), so a repeated identical update is
		// deliberately seq-less; a real device queue's sequential edits
		// each carry different values, and the chain hook is the
		// harness's own seam for exactly that shape.
		Chain: func(row map[string]any, n int) []map[string]any {
			out := make([]map[string]any, 0, n)
			for i := 0; i < n; i++ {
				out = append(out, map[string]any{
					"id":   row["id"],
					"mime": fmt.Sprintf("image/heic-%d", i),
				})
			}
			return out
		},
		Desk: func(ctx context.Context, row map[string]any) error {
			_, err := f.attach.UpdateMetadata(ctx, f.filter, fmt.Sprint(row["id"]), map[string]any{
				"size_bytes": float64(4096),
				"links": []any{
					map[string]any{"owner_kind": "order", "owner_id": testkit.OrderAlphaPickID, "role": "evidence"},
					map[string]any{"owner_kind": "quote", "owner_id": "3ef10000-0000-4000-8000-000000000075", "role": "pod"},
				},
			})
			return err
		},
	}
}

// TestShortagesBattery: the contract battery, GATE-7 and the replay
// property on the shortages scope over the tasks module's own tables.
func TestShortagesBattery(t *testing.T) {
	f := newYard(t)
	k := shortageKit(t, f, f.northPick(t))
	harness.Contract(t, f.engine, k)
	harness.Gate7(t, f.engine, k)
	rng := rand.New(rand.NewSource(1661))
	harness.ReplayProperty(t, f.engine, k, 8, rng.Intn)
}

// TestWeightsBattery: the same battery on the weights scope.
func TestWeightsBattery(t *testing.T) {
	f := newYard(t)
	k := weightKit(t, f)
	harness.Contract(t, f.engine, k)
	harness.Gate7(t, f.engine, k)
	rng := rand.New(rand.NewSource(1662))
	harness.ReplayProperty(t, f.engine, k, 8, rng.Intn)
}

// TestAttachmentsBattery: the same battery on the attachments scope.
func TestAttachmentsBattery(t *testing.T) {
	f := newYard(t)
	k := attachmentKit(t, f)
	harness.Contract(t, f.engine, k)
	harness.Gate7(t, f.engine, k)
	rng := rand.New(rand.NewSource(1663))
	harness.ReplayProperty(t, f.engine, k, 8, rng.Intn)
}

// TestShortagesManualNeverOverwritesAndMarker: the manual policy's own
// acceptance leg, beyond the battery: a crew's field edit on a shortage
// the desk already resolved never overwrites the server row, the
// envelope returns the server row for the client to show, and the review
// marker lands where the review surface reads it.
func TestShortagesManualNeverOverwritesAndMarker(t *testing.T) {
	f := newYard(t)
	ctx := context.Background()
	parent := f.northPick(t)

	created := pushOne(t, f, "shortages", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{
			"task_id": parent.ID, "description": "short one pallet of pavers",
			"qty_short": "1", "uom": "EA",
		},
	})
	if created.Status != "applied" || created.Seq == nil {
		t.Fatalf("create answered %+v", created)
	}
	shortageID := rowIDBySeq(t, f, "shortages", *created.Seq)
	row, ok, err := f.shortages.Load(ctx, f.filter, shortageID)
	if err != nil || !ok {
		t.Fatalf("load the created shortage: ok=%v err=%v", ok, err)
	}

	// The desk resolves the shortage while the device is offline.
	if _, err := f.svc.ResolveShortage(ctx, tasks.ResolveShortageInput{
		TaskID: parent.ID, ShortageID: shortageID,
		ActorID: testkit.UserInsideSalesID, Resolution: "back-ordered for the morning truck",
	}); err != nil {
		t.Fatalf("desk resolve: %v", err)
	}
	moved, _, _ := f.shortages.Load(ctx, f.filter, shortageID)
	if moved.Seq <= row.Seq || moved.Data["resolution"] == "" {
		t.Fatalf("the desk resolve did not move the row: %+v", moved)
	}

	// The crew's stale correction: qty recounted offline, based on the
	// seq the device last saw. Manual never overwrites.
	stale := row.Seq
	res := pushOne(t, f, "shortages", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "update", BaseSeq: &stale,
		Payload: map[string]any{"id": shortageID, "qty_short": "3"},
	})
	if res.Status != "conflict" || res.Conflict == nil {
		t.Fatalf("the stale correction answered %+v", res)
	}
	c := res.Conflict
	if c.Policy != string(sync.PolicyManual) || c.Applied == nil || *c.Applied {
		t.Fatalf("the envelope is not manual's: %+v", c)
	}
	if fmt.Sprint(c.ServerState["qty_short"]) != fmt.Sprint(moved.Data["qty_short"]) {
		t.Fatalf("the client was not told to show the server row's qty: %v", c.ServerState["qty_short"])
	}
	if fmt.Sprint(c.ServerState["resolution"]) == "" {
		t.Fatalf("the server row the client is told to show lost the desk's resolution: %+v", c.ServerState)
	}
	after, _, _ := f.shortages.Load(ctx, f.filter, shortageID)
	if after.Seq != moved.Seq || fmt.Sprint(after.Data["qty_short"]) != fmt.Sprint(moved.Data["qty_short"]) {
		t.Fatalf("the manual policy let the crew's stale field land: %+v", after.Data)
	}

	// A FRESH correction of the resolved shortage also refuses: the
	// counter settled it, and the module closes the report exactly as it
	// closes a second resolver. (The stale leg above conflicts before the
	// guard runs; this leg proves the guard itself.)
	fresh := pushOne(t, f, "shortages", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": shortageID, "qty_short": "3"},
	})
	if fresh.Status != "rejected" {
		t.Fatalf("a fresh correction of a resolved shortage answered %+v", fresh)
	}

	// The review marker: the server-side record that a client edit waits
	// on a review card, written through the audit seam.
	var markers int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE entity_type = 'sync.shortages' AND action = 'SYNC_CONFLICT_REVIEW' AND entity_id = $1::uuid`,
		shortageID).Scan(&markers); err != nil {
		t.Fatalf("marker count: %v", err)
	}
	if markers < 1 {
		t.Fatalf("no review marker landed for the conflicted shortage")
	}
	t.Logf("manual: the crew's stale qty correction never landed; the server row (qty %v, resolution %q) is what the client is told to show; %d review marker row(s) wait on the review surface",
		after.Data["qty_short"], after.Data["resolution"], markers)
}

// TestAttachmentsFieldMergePerField: the per-field merge story, named
// field by field beyond the battery: the desk's legs the device does not
// declare survive the merge; the device's declared legs land; the link
// set replaces as one field.
func TestAttachmentsFieldMergePerField(t *testing.T) {
	f := newYard(t)
	ctx := context.Background()

	created := pushOne(t, f, "attachments", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{
			"kind": "photo", "appwrite_file_id": "fxfile-merge", "bucket": "hh-attachments",
			"mime": "image/jpeg", "size_bytes": float64(2048), "checksum": "sha256:first",
		},
	})
	row, ok, err := f.attach.Load(ctx, f.filter, rowIDBySeq(t, f, "attachments", *created.Seq))
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}

	// The desk corrects the size and attaches the file to a quote as
	// pod; the device, offline, re-saved with a different mime and
	// checksum based on the older seq.
	if _, err := f.attach.UpdateMetadata(ctx, f.filter, row.ID, map[string]any{
		"size_bytes": float64(4096),
		"links": []any{
			map[string]any{"owner_kind": "order", "owner_id": testkit.OrderAlphaPickID, "role": "evidence"},
			map[string]any{"owner_kind": "quote", "owner_id": "3ef10000-0000-4000-8000-000000000075", "role": "pod"},
		},
	}); err != nil {
		t.Fatalf("desk update: %v", err)
	}
	moved, _, _ := f.attach.Load(ctx, f.filter, row.ID)
	if moved.Seq <= row.Seq {
		t.Fatalf("the desk edit did not bump the seq")
	}

	stale := row.Seq
	res := pushOne(t, f, "attachments", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "update", BaseSeq: &stale,
		Payload: map[string]any{"id": row.ID, "mime": "image/heic", "checksum": "sha256:second"},
	})
	if res.Status != "applied" || res.Conflict == nil || res.Conflict.Policy != string(sync.PolicyLWWByField) {
		t.Fatalf("the stale merge answered %+v %+v", res, res.Conflict)
	}
	after, _, _ := f.attach.Load(ctx, f.filter, row.ID)

	// The device's declared fields landed, per field.
	if after.Data["mime"] != "image/heic" {
		t.Fatalf("the merge lost the device's mime: %v", after.Data["mime"])
	}
	if after.Data["checksum"] != "sha256:second" {
		t.Fatalf("the merge lost the device's checksum: %v", after.Data["checksum"])
	}
	// The desk's undeclared legs survived, per field.
	if fmt.Sprint(after.Data["size_bytes"]) != fmt.Sprint(moved.Data["size_bytes"]) {
		t.Fatalf("the merge overwrote the desk's size: %v want %v", after.Data["size_bytes"], moved.Data["size_bytes"])
	}
	if fmt.Sprint(after.Data["links"]) != fmt.Sprint(moved.Data["links"]) {
		t.Fatalf("the merge overwrote the desk's links: %v want %v", after.Data["links"], moved.Data["links"])
	}
	if after.Seq <= moved.Seq {
		t.Fatalf("the merge did not draw a fresh seq: %d after %d", after.Seq, moved.Seq)
	}

	// The link set is one field: a declared set replaces the row's set.
	repl := pushOne(t, f, "attachments", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": row.ID, "links": []any{
			map[string]any{"owner_kind": "order", "owner_id": testkit.OrderAlphaPickID, "role": "evidence"},
		}},
	})
	if repl.Status != "applied" {
		t.Fatalf("the link replace answered %+v", repl)
	}
	final, _, _ := f.attach.Load(ctx, f.filter, row.ID)
	links, _ := final.Data["links"].([]any)
	if len(links) != 1 {
		t.Fatalf("the declared link set did not replace the row's set: %v", final.Data["links"])
	}
	movedLinks, _ := moved.Data["links"].([]any)
	t.Logf("lww: mime and checksum merged from the device, size %v and %d link(s) kept from the desk, fresh seq %d",
		after.Data["size_bytes"], len(movedLinks), after.Seq)
}

// TestWeightsSeqMonotonicUnderConcurrentPushes: the race-clean leg. The
// scope_sequences allocator hands out strictly distinct seqs under
// concurrent creates across goroutines, and concurrent updates on ONE
// row serialize on the row lock with every push answered applied and the
// row's final seq the maximum drawn for it.
func TestWeightsSeqMonotonicUnderConcurrentPushes(t *testing.T) {
	f := newYard(t)
	ctx := context.Background()
	scope := info(t, "weights")

	const workers, perWorker = 8, 5
	var mu stdsync.Mutex
	seen := map[int64]bool{}
	errs := []error{}
	record := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	}
	var wg stdsync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				resp, err := f.engine.Push(ctx, scope, f.filter, []apigen.SyncPushOperation{{
					ClientId: uuid.NewString(), Op: "create",
					Payload: map[string]any{
						"order_id": testkit.OrderAlphaPickID, "order_line_no": 1, "id": uuid.NewString(),
					},
				}})
				if err != nil {
					record(err)
					return
				}
				res := resp.Results[0]
				if res.Status != "applied" || res.Seq == nil {
					record(fmt.Errorf("concurrent create answered %+v", res))
					return
				}
				mu.Lock()
				if seen[*res.Seq] {
					mu.Unlock()
					record(fmt.Errorf("the allocator handed out seq %d twice", *res.Seq))
					return
				}
				seen[*res.Seq] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent creates: %v", errs[0])
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("%d distinct seqs across %d concurrent creates", len(seen), workers*perWorker)
	}
	t.Logf("race: %d concurrent creates drew %d distinct monotonic seqs", workers*perWorker, len(seen))

	// Concurrent updates on one row: every push applies, the final seq is
	// the maximum drawn, and no duplicate was handed out.
	created := pushOne(t, f, "weights", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"order_id": testkit.OrderAlphaPickID, "order_line_no": 1, "id": uuid.NewString()},
	})
	row, ok, err := f.weights.Load(ctx, f.filter, rowIDBySeq(t, f, "weights", *created.Seq))
	if err != nil || !ok {
		t.Fatalf("load the raced row: ok=%v err=%v", ok, err)
	}
	updSeen := map[int64]bool{}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := f.engine.Push(ctx, scope, f.filter, []apigen.SyncPushOperation{{
				ClientId: uuid.NewString(), Op: "update",
				Payload: map[string]any{"id": row.ID, "photo_url": "fx://scale/race"},
			}})
			if err != nil {
				record(err)
				return
			}
			res := resp.Results[0]
			if res.Status != "applied" || res.Seq == nil {
				record(fmt.Errorf("concurrent update answered %+v", res))
				return
			}
			mu.Lock()
			if updSeen[*res.Seq] {
				mu.Unlock()
				record(fmt.Errorf("the allocator handed out update seq %d twice", *res.Seq))
				return
			}
			updSeen[*res.Seq] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent updates: %v", errs[0])
	}
	if len(updSeen) != 4 {
		t.Fatalf("%d distinct seqs across 4 concurrent updates on one row", len(updSeen))
	}
	finalRow, _, _ := f.weights.Load(ctx, f.filter, row.ID)
	maxSeq := int64(0)
	for s := range updSeen {
		if s > maxSeq {
			maxSeq = s
		}
	}
	if finalRow.Seq != maxSeq {
		t.Fatalf("the row's final seq %d is not the maximum drawn %d", finalRow.Seq, maxSeq)
	}
	t.Logf("race: 4 concurrent updates on one row serialized on the row lock; final seq %d is the maximum drawn", finalRow.Seq)
}

// TestYardScopesOrgAndTenantIsolation: the yard tables' scoping legs.
// Rows of the north org never pull through the south org's filter and
// the mirror; a tenant filter answers nothing through all three scopes
// (the adapters fail closed; the route's 403 is the untagged suite's
// leg); and a shortage create naming another org's pick is the not-found
// refusal that leaks no existence.
func TestYardScopesOrgAndTenantIsolation(t *testing.T) {
	f := newYard(t)
	ctx := context.Background()
	parent := f.northPick(t)

	// One row per scope in the north.
	shortage := pushOne(t, f, "shortages", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{
			"task_id": parent.ID, "description": "isolation leg", "qty_short": "1", "uom": "EA",
		},
	})
	weight := pushOne(t, f, "weights", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"order_id": testkit.OrderAlphaPickID, "order_line_no": 1, "id": uuid.NewString()},
	})
	attach := pushOne(t, f, "attachments", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"kind": "photo", "appwrite_file_id": "fxfile-iso", "bucket": "hh-attachments"},
	})
	northIDs := map[string]string{
		"shortages":   rowIDBySeq(t, f, "shortages", *shortage.Seq),
		"weights":     rowIDBySeq(t, f, "weights", *weight.Seq),
		"attachments": rowIDBySeq(t, f, "attachments", *attach.Seq),
	}

	// A second org attachment: the south org's own row, invisible north.
	if _, err := f.attach.Create(ctx, f.south, uuid.NewString(), map[string]any{
		"kind": "document", "appwrite_file_id": "fxfile-south", "bucket": "hh-attachments",
	}); err != nil {
		t.Fatalf("south attachment create: %v", err)
	}

	for scope, id := range northIDs {
		northPull, err := f.engine.Pull(ctx, info(t, scope), f.filter, 0, 200)
		if err != nil {
			t.Fatalf("%s north pull: %v", scope, err)
		}
		found := false
		for _, it := range northPull.Items {
			if it.Id == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("the north pull on %s missed its own row", scope)
		}
		southPull, err := f.engine.Pull(ctx, info(t, scope), f.south, 0, 200)
		if err != nil {
			t.Fatalf("%s south pull: %v", scope, err)
		}
		for _, it := range southPull.Items {
			if it.Id == id {
				t.Fatalf("the south pull on %s saw the north org's row", scope)
			}
		}
		tenantPull, err := f.engine.Pull(ctx, info(t, scope), f.tenantFilt, 0, 200)
		if err != nil {
			t.Fatalf("%s tenant pull: %v", scope, err)
		}
		if len(tenantPull.Items) != 0 || len(tenantPull.Tombstones) != 0 {
			t.Fatalf("a tenant filter read org rows through %s: %+v", scope, tenantPull)
		}
	}

	// The south org sees exactly its own attachment.
	southPull, err := f.engine.Pull(ctx, info(t, "attachments"), f.south, 0, 200)
	if err != nil {
		t.Fatalf("south attachments pull: %v", err)
	}
	if len(southPull.Items) != 1 {
		t.Fatalf("the south org's attachments pull: %d rows, want its own 1", len(southPull.Items))
	}

	// A shortage create naming the north org's pick through the south
	// filter is not-found: the parent is not the caller's own fact.
	res := pushOne(t, f, "shortages", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{
			"task_id": parent.ID, "description": "cross org", "qty_short": "1", "uom": "EA",
		},
	}, withSouth())
	if res.Status != "rejected" {
		t.Fatalf("the cross-org shortage create answered %+v", res)
	}
	t.Logf("isolation: the cross-org shortage create refused (%s)", *res.Error)
}

// TestShortageWalkthrough: the operator walkthrough, scripted against
// the seeded database exactly as the acceptance words it: a shortage
// reported offline in the yard, a server-side correction made at the
// desk, the replay returning the server row with the review marker, and
// the marker visible to the review surface as the contract shapes it.
// Each step logs its outcome so the PR body carries the transcript.
func TestShortageWalkthrough(t *testing.T) {
	f := newYard(t)
	ctx := context.Background()

	// Step 1: the desk queues a pick on the alpha order.
	pick := f.northPick(t)
	t.Logf("walkthrough 1: the desk queued pick task %s on order %s, status %s", pick.ID, testkit.OrderAlphaPickID, pick.Status)

	// Step 2: the yard device pulls the shortages feed before leaving
	// signal: empty, high-water mark 0.
	pull0, err := f.engine.Pull(ctx, info(t, "shortages"), f.filter, 0, 50)
	if err != nil {
		t.Fatalf("device pull: %v", err)
	}
	if len(pull0.Items) != 0 || pull0.LastSeq != 0 {
		t.Fatalf("the initial shortages pull was not empty: %+v", pull0)
	}
	t.Logf("walkthrough 2: the device pulled the shortages feed: 0 rows, high-water mark %d", pull0.LastSeq)

	// Step 3: offline in the yard, the crew finds the shortage and
	// reports it; the outbox holds the create (and, a moment later, the
	// recount as a correction). Signal returns far enough to land the
	// create first.
	createKey := uuid.NewString()
	createOp := apigen.SyncPushOperation{
		ClientId: createKey, Op: "create",
		Payload: map[string]any{
			"task_id": pick.ID, "description": "short four bundles on line one",
			"qty_short": "4", "uom": "EA", "note": "pallet 12 half empty",
		},
	}
	first := yardPush(t, f, "shortages", f.filter, []apigen.SyncPushOperation{createOp})
	if first[0].Status != "applied" || first[0].Seq == nil {
		t.Fatalf("walkthrough 3: the create answered %+v", first[0])
	}
	baseSeq := *first[0].Seq
	shortageID := rowIDBySeq(t, f, "shortages", baseSeq)
	t.Logf("walkthrough 3: the offline shortage report landed on replay, row %s seq %d", shortageID, baseSeq)

	// Step 4: the desk clears the shortage while the device is still
	// offline (the server-side correction).
	if _, err := f.svc.ResolveShortage(ctx, tasks.ResolveShortageInput{
		TaskID: pick.ID, ShortageID: shortageID,
		ActorID:    testkit.UserInsideSalesID,
		Resolution: "substituted the thin-cut bundle from branch B",
	}); err != nil {
		t.Fatalf("walkthrough 4: desk resolve: %v", err)
	}
	deskRow, _, _ := f.shortages.Load(ctx, f.filter, shortageID)
	t.Logf("walkthrough 4: the desk resolved the shortage (resolution %q), row seq now %d",
		deskRow.Data["resolution"], deskRow.Seq)

	// Step 5: the device replays the rest of its outbox: the crew's
	// recount (qty 3, based on the seq it last saw). Manual never
	// overwrites: the replay answers conflict, applied false, the server
	// row is what the client is told to show, and the review marker is
	// written.
	recountKey := uuid.NewString()
	recountOp := apigen.SyncPushOperation{
		ClientId: recountKey, Op: "update", BaseSeq: &baseSeq,
		Payload: map[string]any{"id": shortageID, "qty_short": "3"},
	}
	second := yardPush(t, f, "shortages", f.filter, []apigen.SyncPushOperation{recountOp})
	res := second[0]
	if res.Status != "conflict" || res.Conflict == nil {
		t.Fatalf("walkthrough 5: the stale recount answered %+v", res)
	}
	c := res.Conflict
	if c.Policy != string(sync.PolicyManual) || c.Applied == nil || *c.Applied {
		t.Fatalf("walkthrough 5: the envelope is not manual's: %+v", c)
	}
	if fmt.Sprint(c.ServerState["qty_short"]) != "4.0000" || fmt.Sprint(c.ServerState["resolution"]) == "" {
		t.Fatalf("walkthrough 5: the client was not told to show the desk's row: %+v", c.ServerState)
	}
	t.Logf("walkthrough 5: the stale recount answered conflict (manual, applied false); the client is told to show the server row (qty %v, resolution %q)",
		c.ServerState["qty_short"], c.ServerState["resolution"])

	// Step 6: the marker visible to the yard manager's review surface:
	// the audit row the contract's conflict envelope describes, carrying
	// the scope, the waiting client id and the entity.
	var action, clientID, entityID, outcome string
	err = f.pool.QueryRow(ctx,
		`SELECT action, changes->>'client_id', entity_id::text, changes->>'status'
		 FROM audit_log
		 WHERE entity_type = 'sync.shortages' AND action = 'SYNC_CONFLICT_REVIEW'
		 ORDER BY created_at DESC LIMIT 1`).
		Scan(&action, &clientID, &entityID, &outcome)
	if err != nil {
		t.Fatalf("walkthrough 6: the review marker row: %v", err)
	}
	if clientID != recountKey || entityID != shortageID || outcome != "conflict" {
		t.Fatalf("walkthrough 6: the marker row does not name the waiting edit: %q %q %q", clientID, entityID, outcome)
	}
	t.Logf("walkthrough 6: the review marker is on the review surface: action %s, client id %s, entity %s, outcome %s",
		action, clientID, entityID, outcome)

	// Step 7: a second replay of the whole outbox changes nothing: the
	// create dedups applied, the recount re-answers the same conflict,
	// and the server state is unchanged down to the seqs.
	before := yardSnap(t, f, "shortages")
	again := yardPush(t, f, "shortages", f.filter, []apigen.SyncPushOperation{createOp, recountOp})
	if again[0].Status != "applied" || again[1].Status != "conflict" {
		t.Fatalf("walkthrough 7: the second replay answered %+v %+v", again[0], again[1])
	}
	after := yardSnap(t, f, "shortages")
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("walkthrough 7: the second replay changed the server state: %v then %v", before, after)
	}
	t.Logf("walkthrough 7: the second replay changed nothing (dedup applied and the same conflict, high-water mark stands at %d)", after["last_seq"])
}

// TestWeightsModuleInvariants: the weights adapter enforces the module's
// own rules through sync: a second weight on a closed task refuses (the
// RecordWeight parity), a payload declaring both a weight and a status
// move refuses whole, and a field outside the writable set refuses.
func TestWeightsModuleInvariants(t *testing.T) {
	f := newYard(t)
	created := pushOne(t, f, "weights", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"order_id": testkit.OrderAlphaPickID, "order_line_no": 1, "id": uuid.NewString()},
	})
	id := rowIDBySeq(t, f, "weights", *created.Seq)
	// On the OPEN task, a weight and a status move together refuse whole
	// (this leg runs before the row closes, so the refusal it proves is
	// the both-declared guard itself, not the closed-task one).
	res := pushOne(t, f, "weights", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": id, "weight_entered": "2.4", "weight_uom": "TONNE", "status": "ACCEPTED"},
	})
	if res.Status != "rejected" {
		t.Fatalf("a weight plus a status move on the open task answered %+v", res)
	}
	// The yard answers: the weight closes the task.
	weighed := pushOne(t, f, "weights", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": id, "weight_entered": "2.5", "weight_uom": "TONNE"},
	})
	if weighed.Status != "applied" {
		t.Fatalf("the weight write answered %+v", weighed)
	}
	// A second weight on the closed row refuses.
	res = pushOne(t, f, "weights", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": id, "weight_entered": "2.6", "weight_uom": "TONNE"},
	})
	if res.Status != "rejected" {
		t.Fatalf("a second weight on the closed task answered %+v", res)
	}
	t.Logf("invariant: a second weight on the closed task refused (%s)", *res.Error)
	// A field outside the writable set refuses whole.
	res = pushOne(t, f, "weights", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": id, "priority": "URGENT"},
	})
	if res.Status != "rejected" {
		t.Fatalf("an unwritable field answered %+v", res)
	}
}

// TestWeightsSeqCounterPartition: the two tasks-table feeds draw from
// their own counters, the partition the type-aware allocator keeps (a
// NEED_WEIGHT row's seq never draws the pick_tasks counter, and the
// mirror), so each feed stays monotonic on the column it orders by.
func TestWeightsSeqCounterPartition(t *testing.T) {
	f := newYard(t)
	ctx := context.Background()
	counter := func(scope string) int64 {
		var n int64
		if err := f.pool.QueryRow(ctx,
			`SELECT COALESCE((SELECT next_value FROM scope_sequences WHERE scope = $1), 0)`, scope).Scan(&n); err != nil {
			t.Fatalf("counter %s: %v", scope, err)
		}
		return n
	}
	pickBefore, weightsBefore := counter("pick_tasks"), counter("weights")
	created := pushOne(t, f, "weights", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"order_id": testkit.OrderAlphaPickID, "order_line_no": 1, "id": uuid.NewString()},
	})
	if got := counter("weights"); got <= weightsBefore {
		t.Fatalf("the weights write did not draw the weights counter: %d then %d", weightsBefore, got)
	}
	if got := counter("pick_tasks"); got != pickBefore {
		t.Fatalf("the weights write drew the pick_tasks counter: %d then %d", pickBefore, got)
	}
	// A repository write on the SAME NEED_WEIGHT row (the service's own
	// claim path) draws the weights counter too: the type-aware
	// allocator maps the row's type, in every write path, not only the
	// sync adapter's.
	weightsRow := rowIDBySeq(t, f, "weights", *created.Seq)
	weightsAfterCreate := counter("weights")
	if _, err := f.svc.Claim(ctx, weightsRow, testkit.UserInsideSalesID); err != nil {
		t.Fatalf("claim the weights row: %v", err)
	}
	if got := counter("weights"); got <= weightsAfterCreate {
		t.Fatalf("the claim on the NEED_WEIGHT row did not draw the weights counter: %d then %d", weightsAfterCreate, got)
	}
	if got := counter("pick_tasks"); got != pickBefore {
		t.Fatalf("a repository write on a NEED_WEIGHT row drew the pick_tasks counter: %d then %d", pickBefore, got)
	}
	// And the mirror: PICK writes draw the pick counter only.
	weightsAfterPicks := counter("weights")
	f.northPick(t)
	f.northPick(t)
	if got := counter("pick_tasks"); got <= pickBefore {
		t.Fatalf("the pick writes did not draw the pick_tasks counter: %d then %d", pickBefore, got)
	}
	if counter("weights") != weightsAfterPicks {
		t.Fatalf("a pick write drew the weights counter")
	}
	t.Logf("partition: the sync create and the repository claim on the NEED_WEIGHT row drew the weights counter only; the pick writes drew the pick_tasks counter only")
}

// TestYardScopesAuditRows: every push operation writes its per-operation
// audit row through the real audit service, one scope each.
func TestYardScopesAuditRows(t *testing.T) {
	f := newYard(t)
	ctx := context.Background()
	parent := f.northPick(t)
	pushOne(t, f, "shortages", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"task_id": parent.ID, "description": "audit leg", "qty_short": "1", "uom": "EA"},
	})
	pushOne(t, f, "weights", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"order_id": testkit.OrderAlphaPickID, "order_line_no": 1, "id": uuid.NewString()},
	})
	pushOne(t, f, "attachments", apigen.SyncPushOperation{
		ClientId: uuid.NewString(), Op: "create",
		Payload: map[string]any{"kind": "photo", "appwrite_file_id": "fxfile-audit", "bucket": "hh-attachments"},
	})
	for _, scope := range []string{"shortages", "weights", "attachments"} {
		var n int
		if err := f.pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE entity_type = $1 AND action = 'SYNC_PUSH'`,
			"sync."+scope).Scan(&n); err != nil {
			t.Fatalf("audit count %s: %v", scope, err)
		}
		if n < 1 {
			t.Fatalf("no SYNC_PUSH audit rows landed for %s", scope)
		}
		t.Logf("audit: %d SYNC_PUSH row(s) on %s", n, scope)
	}
}

// The small helpers the yard legs use.

// pushOne pushes one operation through the fixture's engine under the
// north filter (or the override) and returns its single result.
func pushOne(t *testing.T, f *yardFixture, scope string, op apigen.SyncPushOperation, opts ...pushOption) apigen.SyncPushResult {
	t.Helper()
	filter := f.filter
	for _, o := range opts {
		filter = o(filter)
	}
	results := yardPush(t, f, scope, filter, []apigen.SyncPushOperation{op})
	return results[0]
}

// pushOption overrides the push filter.
type pushOption func(sync.ScopeFilter) sync.ScopeFilter

// withSouth pushes under the south org's filter.
func withSouth() pushOption {
	return func(f sync.ScopeFilter) sync.ScopeFilter {
		f.OrgID = testkit.OrgSouthID
		return f
	}
}

// pushOps pushes a whole outbox and refuses a failed request.
func yardPush(t *testing.T, f *yardFixture, scope string, filter sync.ScopeFilter, ops []apigen.SyncPushOperation) []apigen.SyncPushResult {
	t.Helper()
	resp, err := f.engine.Push(context.Background(), info(t, scope), filter, ops)
	if err != nil {
		t.Fatalf("push %s: %v", scope, err)
	}
	if len(resp.Results) != len(ops) {
		t.Fatalf("push %s: %d results for %d ops", scope, len(resp.Results), len(ops))
	}
	return resp.Results
}

// rowIDBySeq resolves the row id carrying one seq in one scope's feed
// (the battery's own resolution discipline).
func rowIDBySeq(t *testing.T, f *yardFixture, scope string, seq int64) string {
	t.Helper()
	resp, err := f.engine.Pull(context.Background(), info(t, scope), f.filter, seq-1, 1)
	if err != nil {
		t.Fatalf("rowOf %s@%d: %v", scope, seq, err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Seq != seq {
		t.Fatalf("rowOf %s@%d: %+v", scope, seq, resp.Items)
	}
	return resp.Items[0].Id
}

// yardSnap snapshots one scope's feed state for the walkthrough's
// replay compare.
func yardSnap(t *testing.T, f *yardFixture, scope string) map[string]any {
	t.Helper()
	resp, err := f.engine.Pull(context.Background(), info(t, scope), f.filter, 0, 200)
	if err != nil {
		t.Fatalf("snap %s: %v", scope, err)
	}
	state := map[string]any{"last_seq": resp.LastSeq}
	for _, it := range resp.Items {
		state[it.Id] = fmt.Sprintf("%d|%v", it.Seq, it.Data["qty_short"])
	}
	for _, tb := range resp.Tombstones {
		state[tb.Id] = fmt.Sprintf("tombstone|%d", tb.Seq)
	}
	return state
}
