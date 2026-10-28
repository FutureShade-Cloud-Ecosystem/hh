package main

// fw1_08b_part_b_branchless_writer_round2_test.go is the P3-4 red (FW1-08b part B round 2,
// FROM-PRINCIPAL 309): the writer census's pattern required the open paren of a column list, so an
// INSERT INTO gl_journal_lines that names no column list at all, and a COPY into the table, both
// walked past it.
//
// Both write a branchless line: an INSERT with no column list takes the table's own column order,
// so it cannot name a branch, and a COPY with no column list copies every column, while a COPY
// with a column list leaves the columns it does not name at their default.
//
// The samples below are planted as TEXT, so the census finds them here and names them; the
// census's own file is allowed for the same reason.

import "testing"

// TestFW1_08b_PartB_Round2_TheWriterCensusCatchesAColumnlessInsertAndACopy is the P3-4 red.
func TestFW1_08b_PartB_Round2_TheWriterCensusCatchesAColumnlessInsertAndACopy(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{
			name: "an insert with no column list",
			body: "package p\n\nfunc f() {\n\t_, _ = db.Exec(`INSERT INTO gl_journal_lines SELECT $1, $2, $3, $4`)\n}\n",
			want: 1,
		},
		{
			name: "a copy into the table",
			body: "package p\n\nfunc f() {\n\t_, _ = db.Exec(`COPY gl_journal_lines FROM '/tmp/lines.csv'`)\n}\n",
			want: 1,
		},
		{
			name: "a copy whose column list names no branch",
			body: "package p\n\nfunc f() {\n\t_, _ = db.Exec(`COPY gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents) FROM '/tmp/lines.csv'`)\n}\n",
			want: 1,
		},
		{
			name: "a copy whose column list names the branch",
			body: "package p\n\nfunc f() {\n\t_, _ = db.Exec(`COPY gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, branch_id) FROM '/tmp/lines.csv'`)\n}\n",
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sites := fw1BranchlessInsertSites(tc.body, "core/cmd/core/somewhere.go")
			if len(sites) != tc.want {
				t.Fatalf("the census found %d branchless writer(s) in %q, want %d", len(sites), tc.name, tc.want)
			}
		})
	}
}
