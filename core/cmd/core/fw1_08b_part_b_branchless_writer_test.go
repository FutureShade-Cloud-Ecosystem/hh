package main

// fw1_08b_part_b_branchless_writer_test.go is item 2's census of the DIRECT writers of
// gl_journal_lines: every SQL statement in the core tree that inserts a journal line must name
// branch_id, because migration 0446's CHECK (gl_journal_lines_branch_id_not_null) refuses a line
// with no branch. The census names each site it finds, file and line, so a writer that can still
// leave a NULL branch is on the record and not only in one test's failure.
//
// The census is a static scan, so it is cheap and runs in the untagged suite. It reads the Go
// tree only: the migration files' own setup entries (0056 and 0221) are 0445's business, they
// are frozen, and 0446's pre check refuses a box where they are still unstamped.
//
// The Go writers that reach a journal through gl's own service (CreateJournal, ReverseJournal,
// RunRecurring, CloseYear, PostExternal, PostExternalTx, ParkExternalJournalTx) are not listed
// here: they are the census of fw1_branch_census_red.go, and each of them stamps the branch
// before it inserts a line.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fw1DirectJournalLineInsert matches an INSERT INTO gl_journal_lines and the open paren of its
// column list. The column list is where a branch is named, so the match stops there.
var fw1DirectJournalLineInsert = regexp.MustCompile(`(?i)insert\s+into\s+gl_journal_lines\s*\(`)

// fw1DirectWriterSite is one direct writer of gl_journal_lines that names no branch.
type fw1DirectWriterSite struct {
	file    string
	line    int
	columns string
}

// fw1PartBBranchlessWriterAllowance names the direct writers that write a branchless line on
// purpose, with the reason each is allowed to. A file in this table is allowed to write a
// branchless journal line.
var fw1PartBBranchlessWriterAllowance = map[string]string{
	"core/cmd/core/fw1_08b_part_b_integration_test.go": "migration 0446's own probe: the plant runs on a box that does not carry the CHECK yet, so the pre check has a branchless journal to refuse in words",
}

// TestFW1_08b_PartB_NoDirectJournalLineWriterLeavesABranchlessLine is the red first leg of item 2:
// it fails, naming every site, while any direct writer of gl_journal_lines can still leave a line
// with no branch.
func TestFW1_08b_PartB_NoDirectJournalLineWriterLeavesABranchlessLine(t *testing.T) {
	coreRoot := filepath.Clean(filepath.Join("..", ".."))
	sites, err := fw1FindDirectJournalLineWriters(coreRoot)
	if err != nil {
		t.Fatalf("scanning %s: %v", coreRoot, err)
	}
	for _, s := range sites {
		if reason, allowed := fw1PartBBranchlessWriterAllowance[s.file]; allowed {
			t.Logf("allowed branchless writer %s:%d: %s", s.file, s.line, reason)
			continue
		}
		t.Errorf("direct writer of gl_journal_lines with no branch: %s:%d: the column list names no branch_id: (%s)",
			s.file, s.line, s.columns)
	}
}

// fw1FindDirectJournalLineWriters walks the core tree and answers every direct INSERT INTO
// gl_journal_lines whose column list names no branch_id.
func fw1FindDirectJournalLineWriters(coreRoot string) ([]fw1DirectWriterSite, error) {
	var sites []fw1DirectWriterSite
	for _, sub := range []string{"cmd", "internal"} {
		root := filepath.Join(coreRoot, sub)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			body, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			rel, rerr := filepath.Rel(coreRoot, path)
			if rerr != nil {
				return rerr
			}
			rel = filepath.ToSlash(filepath.Join("core", rel))
			for _, site := range fw1BranchlessInsertSites(string(body), rel) {
				sites = append(sites, site)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walking %s: %w", root, err)
		}
	}
	return sites, nil
}

// fw1BranchlessInsertSites reads one file's text and answers every INSERT INTO gl_journal_lines
// whose column list names no branch_id.
func fw1BranchlessInsertSites(body, rel string) []fw1DirectWriterSite {
	var sites []fw1DirectWriterSite
	for _, loc := range fw1DirectJournalLineInsert.FindAllStringIndex(body, -1) {
		open := loc[1] - 1 // the open paren of the column list
		depth := 0
		end := -1
		for i := open; i < len(body); i++ {
			switch body[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			continue
		}
		columns := body[open+1 : end]
		if strings.Contains(strings.ToLower(columns), "branch_id") {
			continue
		}
		sites = append(sites, fw1DirectWriterSite{
			file:    rel,
			line:    1 + strings.Count(body[:loc[0]], "\n"),
			columns: strings.Join(strings.Fields(columns), " "),
		})
	}
	return sites
}
