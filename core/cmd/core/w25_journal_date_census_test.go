package main

// w25_journal_date_census_test.go is the source check that EVERY journal a stock side of the
// books posts carries a document date (W2-5 fix round 8, S5's P2-2 and P3-1; round 10, S5's
// P2-7 and the census's scope).
//
// A journal with no document date is dated at the GL's clock, the moment it is posted, which is
// a different instant from the movement it books: the stock ledger's move carries the movement's
// instant and the journal carries the drain's. A month end that falls between the two finds the
// two sides of one movement on different sides of itself and refuses the month by the whole
// movement.
//
// THE SCOPE (round 10): the census watches EVERY package that posts a stock side journal, not
// only internal/inventory. The set lives in the census file (w25StockJournalPackages), so a
// package that posts such a journal and is not on the set is a hole in the check.
//
// The three legs here:
//   - the census over the real stock journal packages: every journal literal names a date, and
//     every package on the set holds at least one literal (a package that held none would be a
//     package the scan no longer sees);
//   - the red: a planted poster in a scratch tree that names no date is caught, so the scan is
//     not vacuous;
//   - the red for the scope: a planted poster in a scratch tree's invcount package is caught
//     too, so the scan watches that package and not only the inventory one.
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestW25_JournalDateCensus_EveryStockJournalNamesADate is the census over the real packages.
func TestW25_JournalDateCensus_EveryStockJournalNamesADate(t *testing.T) {
	coreRoot := filepath.Clean(filepath.Join("..", ".."))
	packages, err := w25StockJournalDateCensus(coreRoot)
	if err != nil {
		t.Fatalf("scanning the stock journal packages: %v", err)
	}
	if len(packages) != len(w25StockJournalPackages) {
		t.Fatalf("the census read %d package(s), want %d", len(packages), len(w25StockJournalPackages))
	}
	total := 0
	for _, p := range packages {
		if p.Literals == 0 {
			t.Errorf("the census saw no journal literal at all in internal/%s: a scan that finds nothing has proved nothing", p.Package)
		}
		total += p.Literals
		for _, u := range p.Undated {
			t.Errorf("a journal in internal/%s names no document date: %s", p.Package, w25UndatedJournalError(u))
		}
	}
	t.Logf("the census read %d package(s) and %d journal literal(s): every one names a document date", len(packages), total)
}

// w25CensusScratchPackage writes one scratch package into a scratch core root, so the census can
// be run over a tree of its own instead of the real one.
func w25CensusScratchPackage(t *testing.T, coreRoot, pkg, src string) {
	t.Helper()
	dir := filepath.Join(coreRoot, "internal", pkg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir the scratch package %s: %v", pkg, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write the scratch package %s: %v", pkg, err)
	}
}

// TestW25_JournalDateCensus_APosterWithoutADateIsCaught is the red: a planted poster in a
// scratch tree that names no document date is found by the scan. On a census that did not watch
// journal dates the scan would find nothing and this leg would go red.
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

// TestW25_JournalDateCensus_AnUndatedInvcountJournalIsCaught is the red for the scope (round
// 10): a planted poster in a scratch tree's invcount package is caught by the scan over the
// package set. On a census that watched only the inventory package the planted journal went
// unnamed and this leg went red.
func TestW25_JournalDateCensus_AnUndatedInvcountJournalIsCaught(t *testing.T) {
	coreRoot := t.TempDir()
	dated := `package %s

import "time"

// datedPoster posts a journal the way this package does, naming its date.
func (s *Service) datedPoster() error {
	_ = s.gl.PostJournal(ctx, GLJournal{
		Source:         "ADJUSTMENT",
		SourceRefID:    "probe",
		IdempotencyKey: "probe:dated",
		Memo:           "a journal with a date",
		BranchID:       "",
		DocumentDate:   time.Now(),
		Lines:          nil,
	})
	return nil
}

var _ = time.Now
`
	undated := `package invcount

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
	for _, pkg := range w25StockJournalPackages {
		if pkg == "invcount" {
			continue
		}
		w25CensusScratchPackage(t, coreRoot, pkg, strings.Replace(dated, "package %s", "package "+pkg, 1))
	}
	w25CensusScratchPackage(t, coreRoot, "invcount", undated)

	packages, err := w25StockJournalDateCensus(coreRoot)
	if err != nil {
		t.Fatalf("scanning the scratch tree: %v", err)
	}
	caught := false
	for _, p := range packages {
		if p.Package != "invcount" {
			continue
		}
		for _, u := range p.Undated {
			caught = true
			if !strings.Contains(u.Func, "sneakPoster") {
				t.Errorf("the census named %q in internal/invcount, want the planted poster's own function", u.Func)
			}
			t.Logf("the census caught the planted poster with no date in internal/invcount: %s", w25UndatedJournalError(u))
		}
	}
	if !caught {
		t.Fatalf("the census did not name the planted undated journal in internal/invcount: the scan does not watch that package")
	}
}
