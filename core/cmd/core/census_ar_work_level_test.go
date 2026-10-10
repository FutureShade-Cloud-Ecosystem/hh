package main

// census_ar_work_level_test.go pins rulings 77 (a) and 87 in the census: the receivables acts
// Payables and Receivables works are served at money desk WORK; a payment reverse stays at manage.
// The reason is the roadmap's: that role holds the money desk at work, so a route at manage was shut
// to the very acts the role exists for. Ruling 87 then holds the payment trail (a payment's history
// and an invoice's payments) to WORK on the same surfaces: the persona and the managers above it keep
// them, while the money desk readers with no counter till lose them. A change to any of these levels
// is a ruling, not a tidy up.

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

func TestCensus_ReceivablesWorkLevelRoutes(t *testing.T) {
	want := []struct {
		method, path, level string
		surfaces            string
	}{
		{"POST", "/api/v1/ar/payments/{id}/nsf", "work", "money-desk"},
		{"POST", "/api/v1/ar/statement-runs", "work", "money-desk"},
		{"POST", "/api/v1/ar/register/statements", "work", "money-desk"},
		{"POST", "/api/v1/ar/register/reminders", "work", "money-desk"},
		{"POST", "/api/v1/ar/reminders", "work", "money-desk"},
		{"GET", "/api/v1/ar/payments/{id}/history", "work", "counter-till,money-desk"},
		{"GET", "/api/v1/ar/invoices/{id}/payments", "work", "counter-till,money-desk"},
		{"POST", "/api/v1/ar/payments/{id}/reverse", "manage", "money-desk"},
	}
	for _, w := range want {
		found := false
		for _, r := range apigen.Routes {
			if r.Method != w.method || r.Path != w.path {
				continue
			}
			found = true
			if r.Level != w.level || strings.Join(r.Surfaces, ",") != w.surfaces {
				t.Errorf("%s %s is served at %s on %v, want %s on %s", w.method, w.path, r.Level, r.Surfaces, w.level, w.surfaces)
			}
		}
		if !found {
			t.Errorf("%s %s is not in the census", w.method, w.path)
		}
	}
}
