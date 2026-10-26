package main

// fw1_walk_red.go is the FW1-00 skip list: one line per still red assertion of the month walk
// (fw1_month_walk_integration_test.go), naming the item that turns it green. Each item's PR
// deletes only its own line, in the same PR, when its assertion goes green. Sorted by name.
//
// The walk is committed red first with this map empty; the lines below name exactly the
// assertions that failed on main, with their red lines quoted in FW1-00-REPORT.md.
var fw1WalkRed = map[string]string{}
