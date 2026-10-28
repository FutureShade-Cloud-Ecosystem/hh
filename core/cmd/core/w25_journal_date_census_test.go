package main

// w25_journal_date_census_test.go is the source check that EVERY journal inventory posts carries
// a document date (W2-5 fix round 8, S5's P2-2 and P3-1).
//
// A journal with no document date is dated at the GL's clock, the moment it is posted, which is a
// different instant from the movement it books: the stock ledger's move carries the movement's
// instant and the journal carries the drain's. A month end that falls between the two finds the
// two sides of one movement on different sides of itself and refuses the month by the whole
// movement.
//
// The two legs here:
//   - the census over the real inventory package: every journal literal names a date;
//   - the red: a planted poster in a scratch tree that names no date is caught, so the census is
//     not vacuous.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestW25_JournalDateCensus_EveryInventoryJournalNamesADate is the census over the real package.
func TestW25_JournalDateCensus_EveryInventoryJournalNamesADate(t *testing.T) {
	coreRoot := filepath.Clean(filepath.Join("..", ".."))
	invDir := filepath.Join(coreRoot, "internal", "inventory")
	result, err := w25JournalDateCensus(invDir)
	if err != nil {
		t.Fatalf("scanning %s: %v", invDir, err)
	}
	if result.Literals == 0 {
		t.Fatalf("the census saw no journal literal at all in %s: a scan that finds nothing has proved nothing", invDir)
	}
	for _, u := range result.Undated {
		t.Errorf("an inventory journal names no document date: %s", w25UndatedJournalError(u))
	}
	t.Logf("the census read %d journal literal(s) in %s: every one names a document date", result.Literals, invDir)
}

// TestW25_JournalDateCensus_APosterWithoutADateIsCaught is the red: a planted poster in a scratch
// tree that names no document date is found by the scan. On a census that did not watch journal
// dates the scan would find nothing and this leg would go red.
func TestW25_JournalDateCensus_APosterWithoutADateIsCaught(t *testing.T) {
	coreRoot := t.TempDir()
	probeDir := filepath.Join(coreRoot, "internal", "inventory")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatalf("mkdir probe: %v", err)
	}
	src := `package inventory

import "time"

// sneakPoster posts a journal the way this package does, without naming a date.
func (s *Service) sneakPoster() error {
	_ = s.gl.PostJournal(ctx, GLJournal{
		Source:         "ADJUSTMENT",
		SourceRefID:    "probe",
		IdempotencyKey: "probe:no-date",
		Memo:           "a journal with no date",
		BranchID:       "",
		Lines:          nil,
	})
	return nil
}

var _ = time.Now
`
	if err := os.WriteFile(filepath.Join(probeDir, "probe.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	result, err := w25JournalDateCensus(probeDir)
	if err != nil {
		t.Fatalf("scanning the probe tree: %v", err)
	}
	if result.Literals != 1 {
		t.Fatalf("the census saw %d journal literal(s) in the probe tree, want 1: the scan does not see the planted poster", result.Literals)
	}
	if len(result.Undated) != 1 {
		t.Fatalf("the census found %d journal(s) naming no date in the probe tree, want 1: %v", len(result.Undated), result.Undated)
	}
	if !strings.Contains(result.Undated[0].Func, "sneakPoster") {
		t.Fatalf("the census named %q, want the planted poster's own function", result.Undated[0].Func)
	}
	t.Logf("the census caught the planted poster with no date: %s", w25UndatedJournalError(result.Undated[0]))
}
