//go:build demo

package main

// demoworld_controller_cursor_test.go, the approvals page's positional cursor through the real registered
// route (PR 300 fix round 1): the row a cursor names may be decided between two reads, and the next page
// still answers the rows after it, none skipped and none repeated, with a concurrent insert beside it.

import (
	"net/http"
	"testing"
)

func TestDemoWorld_ControllerApprovalsCursor(t *testing.T) {
	w := cwNewGuardWorld(t)
	// Three fresh requests sit on top of the queue (newest first).
	for i := 0; i < 3; i++ {
		w.park(t)
	}
	type page struct {
		Items      []cwApproval `json:"items"`
		NextCursor *string      `json:"next_cursor"`
	}
	read := func(query string) page {
		t.Helper()
		rsp := w.controller.call("GET", "/api/v1/staff/approvals?status=PENDING&"+query, nil)
		rsp.ok(t, http.StatusOK)
		var p page
		rsp.decode(t, &p)
		return p
	}
	// The whole order before anything moves: the rows a keyset page two must answer.
	all := read("limit=200")
	if len(all.Items) < 5 {
		t.Fatalf("the queue holds %d rows, want at least 5", len(all.Items))
	}
	first := read("limit=1")
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("page one: %d items, cursor %v", len(first.Items), first.NextCursor)
	}
	if first.Items[0].ID != all.Items[0].ID {
		t.Fatalf("page one holds %s, the full list starts with %s", first.Items[0].ID, all.Items[0].ID)
	}
	// Between the two reads: the cursor row is decided (it leaves the PENDING list) and another request
	// is filed (it sorts above the cursor).
	w.decide(first.Items[0].ID, false).ok(t, http.StatusOK)
	w.park(t)
	second := read("limit=2&cursor=" + *first.NextCursor)
	want := []string{all.Items[1].ID, all.Items[2].ID}
	if len(second.Items) != 2 || second.Items[0].ID != want[0] || second.Items[1].ID != want[1] {
		var got []string
		for _, it := range second.Items {
			got = append(got, it.ID)
		}
		t.Errorf("page two after the decide holds %v, want %v (none skipped, none repeated)", got, want)
	}
}
