package harness

import (
	"context"
	"fmt"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// FreshCase is the freshness probe of one server write: a device that has
// already synced the scope must see the write on its next pull from its own
// cursor. The check lives here, in one place, so every module item proves
// the property the same way: through the real write path and the real
// scope store, never through a fabricated row.
type FreshCase struct {
	Engine *sync.Engine
	Scope  sync.ScopeInfo
	// Filter is the caller the device pulls as.
	Filter sync.ScopeFilter
	// Write is the REAL module write path. It returns the id the scope
	// serves the changed row under.
	Write func(ctx context.Context) (entityID string, err error)
	// Want checks the changed served column on the row's data. A nil Want
	// means the write must arrive as a tombstone.
	Want func(data map[string]any) error
}

// pullAll follows next_cursor to the end from since and returns every item
// and tombstone seq by id, plus the item data and the final cursor. The
// cursor of a device that has synced is the last page's last_seq.
func pullAll(ctx context.Context, c FreshCase, since int64) (items map[string]map[string]any, itemSeq, tombSeq map[string]int64, cursor int64, err error) {
	items = map[string]map[string]any{}
	itemSeq = map[string]int64{}
	tombSeq = map[string]int64{}
	cursor = since
	for {
		resp, perr := c.Engine.Pull(ctx, c.Scope, c.Filter, cursor, 200)
		if perr != nil {
			return nil, nil, nil, cursor, perr
		}
		for _, it := range resp.Items {
			items[it.Id] = it.Data
			itemSeq[it.Id] = it.Seq
		}
		for _, tb := range resp.Tombstones {
			tombSeq[tb.Id] = tb.Seq
		}
		if resp.NextCursor == nil {
			if resp.LastSeq > cursor {
				cursor = resp.LastSeq
			}
			return items, itemSeq, tombSeq, cursor, nil
		}
		var next int64
		if _, serr := fmt.Sscan(*resp.NextCursor, &next); serr != nil {
			return nil, nil, nil, cursor, fmt.Errorf("next_cursor %q is not a seq: %w", *resp.NextCursor, serr)
		}
		cursor = next
	}
}

// Fresh runs one freshness case: pull from 0 to the end (the device's
// cursor), run Write, pull from that cursor, and require the entity in the
// items with Want holding (or in the tombstones when Want is nil) at a seq
// past the old cursor.
func Fresh(t testing.TB, c FreshCase) {
	t.Helper()
	ctx := context.Background()
	scope := c.Scope.Name

	_, oldItemSeq, oldTombSeq, cursor, err := pullAll(ctx, c, 0)
	if err != nil {
		t.Fatalf("fresh %s: first pull from 0: %v", scope, err)
	}
	id, err := c.Write(ctx)
	if err != nil {
		t.Fatalf("fresh %s: the write failed: %v", scope, err)
	}
	items, itemSeq, tombSeq, _, err := pullAll(ctx, c, cursor)
	if err != nil {
		t.Fatalf("fresh %s: pull from cursor %d: %v", scope, cursor, err)
	}

	before := "absent"
	if s, ok := oldItemSeq[id]; ok {
		before = fmt.Sprint(s)
	} else if s, ok := oldTombSeq[id]; ok {
		before = fmt.Sprintf("%d (tombstone)", s)
	}

	data, inItems := items[id]
	_, inTombs := tombSeq[id]

	if c.Want == nil {
		if !inTombs {
			t.Errorf("fresh %s: pull from the device's cursor %d returns the tombstone for %s: false (seq before the write: %s, came back as an item: %v)",
				scope, cursor, id, before, inItems)
			return
		}
		if tombSeq[id] <= cursor {
			t.Errorf("fresh %s: tombstone for %s has seq %d, not past the device's cursor %d (seq before the write: %s)",
				scope, id, tombSeq[id], cursor, before)
		}
		return
	}

	if !inItems {
		t.Errorf("fresh %s: pull from the device's cursor %d returns it: false (entity %s, seq before the write: %s, came back as a tombstone: %v)",
			scope, cursor, id, before, inTombs)
		return
	}
	if itemSeq[id] <= cursor {
		t.Errorf("fresh %s: entity %s has seq %d, not past the device's cursor %d (seq before the write: %s)",
			scope, id, itemSeq[id], cursor, before)
	}
	if werr := c.Want(data); werr != nil {
		t.Errorf("fresh %s: pull from the device's cursor %d returns it with the changed value: false (entity %s, seq before %s, after %d): %v",
			scope, cursor, id, before, itemSeq[id], werr)
	}
}

// FreshArm is one filter a FreshFor case runs under, with a label that
// the failure message carries.
type FreshArm struct {
	Name   string
	Filter sync.ScopeFilter
	// Write overrides the case's Write for this arm when non nil, for a
	// fence change where each arm needs its own setup. Most arms leave it.
	Write func(ctx context.Context) (entityID string, err error)
}

// FreshFor runs the same case under several filters, each as its own sub
// test. Two uses: the caller arms of one scope (invoices: staff by org,
// tenant by customer), and a fence change, where the entity must reach a
// filter that did not admit it before the write (arm Write supplies the
// move, and the arm's first pull proves the old state).
func FreshFor(t *testing.T, c FreshCase, arms ...FreshArm) {
	t.Helper()
	for _, a := range arms {
		a := a
		t.Run(a.Name, func(t *testing.T) {
			ac := c
			ac.Filter = a.Filter
			if a.Write != nil {
				ac.Write = a.Write
			}
			Fresh(t, ac)
		})
	}
}
