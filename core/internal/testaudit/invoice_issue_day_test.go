package testaudit

// invoice_issue_day_test.go, the census of invoice writers that bind the issue day.
//
// THE DEFECT CLASS. invoices.issue_date was born (0009) with DEFAULT CURRENT_DATE: a writer that
// leaves the column out does not fail, it silently takes the DATABASE SESSION's day, which is the
// UTC day on every deployment this house runs. An invoice raised in the Ontario evening is then
// dated tomorrow while the yard is still working today, and every aging figure derived from the
// issue date is one day wrong from the moment it is written.
//
// WHY A CENSUS AND NOT A DROPPED DEFAULT. The obvious cure is to drop the column default so a
// forgetful writer fails loudly. It is too wide for this tree today: more than two dozen test
// sites across eighteen files, in packages fenced to other lanes (quote and till among them),
// still insert into invoices without naming issue_date, so the drop would leave suites this lane
// cannot repair red on main. Until those sites are bound by their owners, the writers themselves
// are bound in Go on the house day (platform/houseday), and THIS census is the loud failure: every
// INSERT INTO invoices in the production Go tree must name issue_date in its column list, so a
// new writer that goes back to the default fails CI's untagged run with the file and line.
//
// WHAT IT CANNOT KNOW. It reads the statement's column list (the text between the table name and
// VALUES), not the bound value: a writer that names the column and binds the UTC day passes here,
// and the pinned-clock legs in the billing and pos packages are the behavioural proof those days
// are the house zone's. Fixture SQL in migrations and seed files is not walked: a seeded row's
// dates are the seed's own facts, not a writer's decision.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// invoiceInsert matches an INSERT INTO invoices statement and captures its column list.
var invoiceInsert = regexp.MustCompile(`(?is)INSERT\s+INTO\s+invoices\s*\((.*?)\)\s*VALUES`)

// issueDateColumn is the column this census exists to keep named.
var issueDateColumn = regexp.MustCompile(`(?i)\bissue_date\b`)

func TestInvoiceWriters_NameTheIssueDayTheyBind(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "internal")
	var missing []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		for _, m := range invoiceInsert.FindAllSubmatchIndex(raw, -1) {
			if issueDateColumn.Match(raw[m[2]:m[3]]) {
				continue
			}
			line := 1 + strings.Count(string(raw[:m[0]]), "\n")
			missing = append(missing, rel+":"+strconv.Itoa(line))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the census could not walk the production tree: %v", err)
	}
	if len(missing) > 0 {
		t.Fatalf("INSERT INTO invoices without a bound issue_date (the database default is the "+
			"session zone's day, and the default cannot be dropped while fenced test writers still "+
			"rely on it): %s", strings.Join(missing, ", "))
	}
}
