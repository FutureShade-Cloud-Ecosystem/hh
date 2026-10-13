//go:build demo

// dealer.go (the data; the guarded writes live in populate.go, where the table census states them), the demo dealer's two yards: Gable Landscape Supply, Point Loma
// (code PLM, the main yard, where every demo login is homed) and Julian (code
// JUL). Migration 0003 plants the two BRANCH rows under the first dealer's
// codes, names and addresses; the demo database converges them onto the
// invented dealer here, on the demo database only. Real deployments keep
// their own branches: nothing in a default build calls this.
//
// WHO CALLS IT, AND WHY TWICE. The populate calls it (renameDemoDealer) and the
// demoseed's demo-build pre-step calls it before the seed resolves its
// branches. The seed derives every order, purchase order, run and stock move
// reference from the branch CODE, so the first seed pass must already see the
// final codes: a seed that wrote DEMO-TRT-O01 and then met a branch coded PLM
// would write a second set of stock moves on the next pass. Both callers are
// guarded on the value they write, so whichever runs second writes nothing.
//
// THE ORDER OF THE TWO CODES MATTERS. The seed maps its branch 0 and branch 1
// onto the active BRANCH rows ordered by code. The first dealer's codes sorted
// KGN then TRT, so branch 1 (the branch every demo login reaches) was TRT.
// JUL then PLM sorts the same way, so branch 1 is Point Loma and branch 0 is
// Julian, and no seeded row changes yard.
package demo

// DealerBranch is one yard of the demo dealer as the demo database carries it.
type DealerBranch struct {
	ID, Code, Name string
	Line1, City    string
	Province       string
	Postal         string
}

// DealerBranches is the demo dealer's two yards, in the order Point Loma then
// Julian. The addresses are invented, in San Diego County.
var DealerBranches = []DealerBranch{
	{trentonBranchID, "PLM", "Gable Landscape Supply, Point Loma", "2410 Harbor Yard Road", "San Diego", "CA", "92106"},
	{kingstonBranchID, "JUL", "Gable Landscape Supply, Julian", "1180 Quarry Gate Road", "Julian", "CA", "92036"},
}
