package authz

// rolegate_t5018_report_test.go, who may read the year's T5018 report. The annotation read is the
// generated census entry, so the contract is what is proven, not a literal beside it. The report
// is a payables read: it names money-desk at read, the same door as the payables board beside it,
// and every role the matrix gives money-desk at read or above reaches the handler while a role
// with no money-desk cell is refused with the shared RBAC body.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// TestT5018Report_IsAMoneyDeskRead: the report and its CSV export are money desk reads, so every
// role the matrix gives money-desk at read or above reaches the handler, and a role with no
// money-desk cell is refused. The CSV is the same report in its file form and must not be a wider
// door than the screen: the test reads both annotations and holds them to the same door.
func TestT5018Report_IsAMoneyDeskRead(t *testing.T) {
	f := newFixture(t)
	cfg := mustConfig(t)
	for _, op := range []string{"purchasingT5018Report", "purchasingT5018ReportCSV"} {
		access := procurementAccess(t, op)
		if len(access.Surfaces) != 1 || access.Surfaces[0] != "money-desk" || access.Level != roleconfig.LevelRead {
			t.Fatalf("%s must name money-desk at read, got %+v", op, access)
		}
		for _, role := range roleconfig.Roles() {
			code, reached := runGatedAs(t, f, access, role)
			reach, err := cfg.Holds(role, access.Surfaces[0], roleconfig.LevelRead)
			if err != nil {
				t.Fatalf("%s: %s: %v", op, role, err)
			}
			if reach && (code != 200 || !reached) {
				t.Errorf("%s: %s holds money-desk at read or above, must reach the handler, got %d reached=%v", op, role, code, reached)
			}
			if !reach && (code != 403 || reached) {
				t.Errorf("%s: %s holds no money-desk cell, must be refused, got %d reached=%v", op, role, code, reached)
			}
		}
	}
}
