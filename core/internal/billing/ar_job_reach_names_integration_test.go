//go:build integration

// ar_job_reach_names_integration_test.go, W3 0.62 item 2 (P3-1): a job homed outside the caller's reach is
// named neither on the statement by job nor on the aging by job, though one of its invoices sits in reach:
// the money stays on the lines the caller sees.
package billing

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestIntegration_W3P062_JobHomedOutOfReachIsNotNamed: item 2 (P3-1), statement by job and aging by job.
func TestIntegration_W3P062_JobHomedOutOfReachIsNotNamed(t *testing.T) {
	w := newJobReachWorld(t)
	mux := jobReachMux(t)
	ctx := reachCtx(trentonBranch)

	rec := jobReachGet(mux, ctx, "/api/v1/ar/customers/"+w.customer+"/statement-by-job")
	var st JobStatement
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &st) != nil {
		t.Fatalf("statement by job: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), w.away) {
		t.Fatalf("the statement by job carries the id of the job homed at Kingston: %s", rec.Body.String())
	}
	seenAway, seenSplit := false, false
	for _, sec := range st.Sections {
		switch {
		case sec.JobID == "" && !sec.AccountLevel:
			if sec.JobNo != "" || sec.JobName != "" {
				t.Fatalf("an unidentified section is also unnamed: %+v", sec)
			}
			seenAway = true
			if sec.SubtotalCents != 5000 || len(sec.Lines) != 1 || sec.Lines[0].JobID != "" {
				t.Fatalf("the money stays on the visible lines, and the line carries no job id: %+v", sec)
			}
		case sec.JobID == w.away:
			seenAway = true
			if sec.JobNo != "" || sec.JobName != "" {
				t.Fatalf("the job homed at Kingston is named to a Trenton manager: %+v", sec)
			}
			if sec.SubtotalCents != 5000 || len(sec.Lines) != 1 {
				t.Fatalf("the money stays on the visible lines: %+v", sec)
			}
		case sec.JobID == w.split:
			seenSplit = true
			if sec.JobNo == "" || sec.JobName == "" || sec.SubtotalCents != 30000 {
				t.Fatalf("a job homed in reach keeps its name: %+v", sec)
			}
		}
	}
	if !seenAway || !seenSplit {
		t.Fatalf("both jobs have lines in reach: away %v split %v", seenAway, seenSplit)
	}

	rec = jobReachGet(mux, ctx, "/api/v1/ar/aging-by-job")
	if strings.Contains(rec.Body.String(), w.away) {
		t.Fatalf("the aging by job carries the id of the job homed at Kingston")
	}
	var aging struct {
		Items []AgingJobRow `json:"items"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &aging) != nil {
		t.Fatalf("aging by job: %d %s", rec.Code, rec.Body.String())
	}
	got := map[string]AgingJobRow{}
	for _, r := range aging.Items {
		if r.CustomerID == w.customer {
			got[r.JobID] = r
		}
	}
	var away AgingJobRow
	ok := false
	for _, r := range aging.Items {
		if r.CustomerID == w.customer && !r.AccountLevel && r.JobID == "" {
			away, ok = r, true
		}
	}
	if !ok || away.TotalCents != 5000 {
		t.Fatalf("the away job's row keeps its money: %+v (found %v)", away, ok)
	}
	if away.JobNo != "" || away.JobName != "" {
		t.Fatalf("the aging by job names a Kingston job to a Trenton manager: %+v", away)
	}
	if sp := got[w.split]; sp.JobNo == "" || sp.JobName == "" || sp.TotalCents != 30000 {
		t.Fatalf("a job homed in reach keeps its name on the aging: %+v", sp)
	}
}
