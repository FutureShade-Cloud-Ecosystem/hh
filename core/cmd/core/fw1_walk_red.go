package main

// fw1_walk_red.go is the FW1-00 skip list: one line per still red assertion of the month walk
// (fw1_month_walk_integration_test.go), naming the item that turns it green. Each item's PR
// deletes only its own line, in the same PR, when its assertion goes green. Sorted by name.
//
// The walk is committed red first with this map empty; the lines below name exactly the
// assertions that failed on main, with their red lines quoted in FW1-00-REPORT.md.
//
// The AR tie line and the month close line (FW1-09d) read the same $700.00: the walk's opening AR
// posting books the two CP03 demo invoices a second time beside their own finalize journals. The
// stock tie passes in the walk. Both lines go green with the walk follow up's marker filter on that
// posting (FW1-00, PR 634), not with FW1-06a, which has merged.
var fw1WalkRed = map[string]string{
	"month_close_passes_its_pre_flight_and_seals_the_month": "FW1-00",
}
