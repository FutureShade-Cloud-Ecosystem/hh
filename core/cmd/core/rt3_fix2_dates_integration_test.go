//go:build integration

package main

// rt3_fix2_dates_integration_test.go, the RT-3 fix round 2, item 3: a manager door to set or correct a lien
// job's dates (publication of the certificate, substantial performance, last supply), so the conservative
// preservation rule can see a publication that happens after the job was opened.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

// Publication set after creation moves the due day later: a job whose period had run out on its
// performance date is not due once a later publication is recorded, and a correction back makes it due.
func TestRT3Fix2_APublicationSetAfterCreationMovesTheDueDayLater(t *testing.T) {
	l := rt2Setup(t, "f2e")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(70)
	path := "/api/v1/lien/jobs/" + l.lien + "/preservation-dates"
	today := rt3Today().Format("2006-01-02")

	if code, body := l.call("PUT", path, `{"csp_published_on":"not a date"}`); code != http.StatusUnprocessableEntity {
		t.Errorf("a bad date: want 422, got %d %s", code, body)
	}
	if code, body := l.call("PUT", "/api/v1/lien/jobs/00000000-0000-4000-8000-000000000000/preservation-dates", `{"csp_published_on":"`+today+`"}`); code != http.StatusNotFound {
		t.Errorf("an unknown job: want 404, got %d %s", code, body)
	}
	if code, body := l.call("PUT", path, `{}`); code != http.StatusUnprocessableEntity {
		t.Errorf("a body naming no date: want 422, got %d %s", code, body)
	}

	code, body := l.call("PUT", path, `{"csp_published_on":"`+today+`"}`)
	if code != http.StatusOK || !strings.Contains(body, today) {
		t.Fatalf("set the publication: want 200 naming the date, got %d %s", code, body)
	}
	eq(t, "the change is recorded", l.auditCount("HOLDBACK_LIEN_DATES_SET"), 1)
	if _, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("f2e-1")); rt3Refusal(err) != billing.RefusedNotDue {
		t.Fatalf("a publication today starts the period today: want a not due refusal, got %v", err)
	}
	eq(t, "nothing moved by the refusal", l.holdbackOf(doc.InvoiceID), int64(10_000))

	// A correction: the publication was keyed on the wrong job; clearing it returns the performance date's rule.
	if code, body := l.call("PUT", path, `{"csp_published_on":null}`); code != http.StatusOK {
		t.Fatalf("clear the publication: want 200, got %d %s", code, body)
	}
	eq(t, "the correction is recorded", l.auditCount("HOLDBACK_LIEN_DATES_SET"), 2)

	// Last supply and substantial performance can be corrected too.
	if code, body := l.call("PUT", path, `{"last_supply_date":"`+today+`"}`); code != http.StatusOK {
		t.Fatalf("set the last supply: want 200, got %d %s", code, body)
	}
	if _, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("f2e-2")); rt3Refusal(err) != billing.RefusedNotDue {
		t.Fatalf("a last supply today starts the period today: want a not due refusal, got %v", err)
	}
	if code, body := l.call("PUT", path, `{"last_supply_date":null}`); code != http.StatusOK {
		t.Fatalf("clear the last supply: want 200, got %d %s", code, body)
	}
	if _, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("f2e-3")); err != nil {
		t.Fatalf("with the dates corrected back the period has run out: %v", err)
	}
}
