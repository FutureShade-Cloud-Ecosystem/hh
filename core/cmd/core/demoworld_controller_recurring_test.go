//go:build demo

package main

// demoworld_controller_recurring_test.go, ruling 108 (2) through the real routes.

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Ruling 108 (2): until recurring runs can file, a recurring run's DRAFT over the threshold is refused at the
// GL approve route with words naming its template; under the threshold it posts as before.
func TestDemoWorld_ControllerRecurringRunRefusedOverTheThreshold(t *testing.T) {
	w := cwNewGuardWorld(t)
	ctx := context.Background()
	run := func(name string, cents int64) string {
		t.Helper()
		var tpl string
		if err := w.pool.QueryRow(ctx, `INSERT INTO gl_recurring_templates (name, memo, lines, schedule)
			VALUES ($1, '', jsonb_build_array(
			  jsonb_build_object('account_code','1300','debit_cents',$2::bigint),
			  jsonb_build_object('account_code','1000','credit_cents',$2::bigint)), 'MONTHLY') RETURNING id::text`, name, cents).Scan(&tpl); err != nil {
			t.Fatal(err)
		}
		rsp := w.controller.call("POST", "/api/v1/gl/recurring/"+tpl+"/run", nil)
		rsp.ok(t, http.StatusOK, http.StatusCreated)
		var j cwJournal
		rsp.decode(t, &j)
		// The run's maker is the caller; hand the draft to another maker so the four eyes check passes and the
		// leg reaches the approval state check.
		if _, err := w.pool.Exec(ctx, `UPDATE gl_journals SET created_by = $2::uuid WHERE id = $1::uuid`, j.ID, qwManagerUserID); err != nil {
			t.Fatal(err)
		}
		return j.ID
	}

	over := run("Quarterly rent accrual", 750_000)
	rsp := w.glApprove(over)
	rsp.ok(t, http.StatusConflict)
	if !strings.Contains(string(rsp.body), "Quarterly rent accrual") || !strings.Contains(string(rsp.body), "recurring") {
		t.Errorf("the refusal does not name the recurring template: %s", rsp.body)
	}
	if got := w.journalStatus(t, over); got != "DRAFT" {
		t.Errorf("the journal reads %s, want DRAFT", got)
	}

	under := run("Small monthly accrual", 100_000)
	w.glApprove(under).ok(t, http.StatusOK)
	if got := w.journalStatus(t, under); got != "POSTED" {
		t.Errorf("the under threshold journal reads %s, want POSTED", got)
	}
}
