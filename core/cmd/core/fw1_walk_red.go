package main

// fw1_walk_red.go is the FW1-00 skip list: one line per still red assertion of the month walk
// (fw1_month_walk_integration_test.go), naming the item that turns it green. Each item's PR
// deletes only its own line, in the same PR, when its assertion goes green. Sorted by name.
//
// The walk is committed red first with this map empty; the lines below name exactly the
// assertions that failed on main, with their red lines quoted in FW1-00-REPORT.md.
var fw1WalkRed = map[string]string{
	"AR_aging_ties_to_GL_1100":                                          "FW1-06a",
	"HST_reconcile_ties":                                                "FW1-11",
	"bank_lodgement_match_posts_no_second_journal":                      "FW1-05",
	"bank_month_signs_off_at_zero_with_outstanding_items":               "FW1-04",
	"every_journal_line_carries_a_branch_and_branch_trial_balances_sum": "FW1-08",
	"stock_value_ties_to_GL_1200":                                       "FW1-07",
	"till_lodgement_posts_bank_1000_at_lodgement":                       "FW1-13",
	"walk_month_and_next_year_periods_open":                             "FW1-01",
	"year_end_close_opens_next_year_and_keeps_income_statement":         "FW1-09",
}
