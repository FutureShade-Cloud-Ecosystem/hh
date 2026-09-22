//go:build integration

// list_snapshot_integration_test.go, regression guard for the MINOR recon finding
// (repository.go List): the default-branch read and the locations read must be ONE consistent
// snapshot, not two independent round-trips. Before the fix, List did:
//
//	defaultID := DefaultBranchID(ctx)   // read #1 (own round-trip)
//	rows      := pool.Query(...)        // read #2 (separate round-trip)
//
// so a writer that ATOMICALLY (one transaction) inserts a new BRANCH and points
// system_settings.default_branch_id at it could commit BETWEEN List's two reads. A straddling List
// then reads the NEW default_branch_id (read #1, post-commit) but the OLD locations set (read #2 -
// or vice versa), so it flags is_default against a branch that is not in the list it returned: a
// torn snapshot (zero branches flagged, though a default is set and its branch is present in a
// consistent view). After the fix both reads run inside one ReadOnly/RepeatableRead transaction, so
// any concurrent commit is entirely before or entirely after List's snapshot, never straddled.
//
// The test hammers List concurrently with an atomic (insert-branch + set-default) / (remove) writer
// and asserts the snapshot invariant on every List result: whenever a default is set to a branch, a
// consistent snapshot either sees BOTH (branch present AND flagged) or NEITHER, never the torn
// "default set to branch X but X absent from the list" state the two-read version can produce.
//
//	Run: DATABASE_URL=postgres://hardscape:hardscape@localhost:5435/hardscapeos go test \
//	  -tags integration ./internal/branches/... -run TestIntegration_List_ConsistentSnapshot
package branches

import (
	"context"
	"sync"
	"testing"
)

func TestIntegration_List_ConsistentSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	// Save + restore the original default so the test is non-destructive.
	orig, err := repo.DefaultBranchID(ctx)
	if err != nil {
		t.Fatalf("read orig default: %v", err)
	}

	// A dedicated test branch we toggle IN/OUT of the BRANCH set atomically alongside the default
	// pointer. A BRANCH row must have parent_id IS NULL and branch_id = own id (0003 CHECK); the
	// "out" state re-parents it to a seeded branch as a ZONE so it drops out of List's
	// loc_type='BRANCH' filter, the CHECK ((loc_type='BRANCH') = (parent_id IS NULL)) is satisfied
	// in both states. We need a real seeded branch to hang the ZONE off.
	var seedBranchID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY code LIMIT 1`).Scan(&seedBranchID); err != nil {
		t.Fatalf("find seed branch: %v", err)
	}
	const testCode = "ZZS"                                                   // snapshot-test code, outside the seeded KGN/TRT set
	_, _ = pool.Exec(ctx, `DELETE FROM locations WHERE code = $1`, testCode) // clear any prior-run leftover
	// branch_id must reference an existing location (self-FK, NOT NULL). Point it at the seed
	// branch, the 0003 CHECK only couples loc_type↔parent_id, not branch_id, so this is valid for
	// both the BRANCH (parent_id NULL) and ZONE (parent_id = seed) states we toggle between.
	var testBranchID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, is_active)
		VALUES (gen_random_uuid(), $2::uuid, NULL, 'BRANCH', $1, 'Snapshot Test Branch', true)
		RETURNING id::text`, testCode, seedBranchID).Scan(&testBranchID); err != nil {
		t.Fatalf("insert test branch: %v", err)
	}

	setDefault := func(ctx context.Context, id string) error {
		_, err := pool.Exec(ctx,
			`INSERT INTO system_settings (key, value) VALUES ('default_branch_id', to_jsonb($1::text))
			 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, id)
		return err
	}

	t.Cleanup(func() {
		bg := context.Background()
		if orig != "" {
			_ = setDefault(bg, orig)
		} else {
			_, _ = pool.Exec(bg, `DELETE FROM system_settings WHERE key = 'default_branch_id'`)
		}
		_, _ = pool.Exec(bg, `DELETE FROM locations WHERE id = $1`, testBranchID)
	})

	// Point the default at the test branch to start from a coherent state.
	if err := setDefault(ctx, testBranchID); err != nil {
		t.Fatalf("seed default: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer: atomically toggle the test branch in/out of the BRANCH set AND its default pointer,
	// in ONE transaction each flip, so the two facts (branch present ⟺ default points at it) always
	// commit together. This is the interleaving that torn-reads a two-round-trip List.
	wg.Add(1)
	go func() {
		defer wg.Done()
		present := true
		for {
			select {
			case <-stop:
				return
			default:
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				return
			}
			if present {
				// drop the branch OUT of the BRANCH set (→ ZONE under the seed) and clear the
				// default together, in one commit.
				_, e1 := tx.Exec(ctx,
					`UPDATE locations SET loc_type = 'ZONE', parent_id = $2::uuid WHERE id = $1`,
					testBranchID, seedBranchID)
				_, e2 := tx.Exec(ctx,
					`INSERT INTO system_settings (key, value) VALUES ('default_branch_id', to_jsonb($1::text))
					 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, orig)
				if e1 != nil || e2 != nil {
					_ = tx.Rollback(ctx)
					continue
				}
			} else {
				// bring the branch BACK into the BRANCH set (parent_id NULL) and point the default
				// at it together, in one commit.
				_, e1 := tx.Exec(ctx,
					`UPDATE locations SET loc_type = 'BRANCH', parent_id = NULL WHERE id = $1`, testBranchID)
				_, e2 := tx.Exec(ctx,
					`INSERT INTO system_settings (key, value) VALUES ('default_branch_id', to_jsonb($1::text))
					 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, testBranchID)
				if e1 != nil || e2 != nil {
					_ = tx.Rollback(ctx)
					continue
				}
			}
			if err := tx.Commit(ctx); err != nil {
				continue
			}
			present = !present
		}
	}()

	// Reader: List repeatedly and assert the snapshot invariant. The torn state the two-read version
	// produces: default_branch_id points at the test branch, but the returned list does NOT contain
	// it flagged (because the two reads straddled a toggle). Under a single-snapshot List that state
	// is impossible.
	defer func() { close(stop); wg.Wait() }()
	for i := 0; i < 400; i++ {
		list, err := repo.List(ctx)
		if err != nil {
			t.Fatalf("List iter %d: %v", i, err)
		}
		// Invariant the atomic writer guarantees in any CONSISTENT snapshot: the test branch is a
		// BRANCH (present in the list) IF AND ONLY IF the default points at it. So whenever the test
		// branch appears in List's result it MUST be flagged is_default. The two-read version breaks
		// this: read #1 (default) and read #2 (locations) can straddle a toggle commit, yielding a
		// list that contains the test branch as a BRANCH while the default it read still pointed at
		// `orig`, the branch present but is_default=false. A single-snapshot List cannot tear that.
		var flagged int
		for _, b := range list {
			if b.IsDefault {
				flagged++
			}
			if b.ID == testBranchID && !b.IsDefault {
				t.Fatalf("iter %d: test branch is present in the list but not flagged is_default, "+
					"torn snapshot (the default read and the locations read straddled a toggle)", i)
			}
		}
		if flagged > 1 {
			t.Fatalf("iter %d: %d branches flagged is_default; a consistent snapshot has at most one", i, flagged)
		}
	}
}
