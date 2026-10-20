package assist

// dataquery_shape.go, the plan shape check of plan r3 section 3.5.
//
// EXPLAIN runs before every query, in the same read only transaction, and the query is refused on its
// SHAPE: a plan above the cost ceiling, or a sequential scan of a large table above a lower cost. The
// reason is one fixed sentence ("too broad"). EXPLAIN's estimates come from whole table statistics, so
// they say how big a table is whatever row security hides: they are logged on the server and never
// reach the model or the user (review r1 P3-1).

import (
	"encoding/json"
)

// Default bounds. The ceiling is a plan cost, in the planner's units; a sequential scan of about two
// million narrow rows costs a few tens of thousands. The sequential scan limit is lower: the cost of
// scanning a large table end to end for a question that named no selective filter.
const (
	DefaultCostCeiling      = 50000.0
	DefaultSeqScanCostLimit = 10000.0
)

// largeTables are the base tables behind the large views, by name as the planner reports them.
var largeTables = map[string]bool{"orders": true, "order_lines": true, "stock_balances": true, "quotes": true, "quote_lines": true}

// planNode is the part of an EXPLAIN node the check reads.
type planNode struct {
	NodeType     string     `json:"Node Type"`
	RelationName string     `json:"Relation Name"`
	TotalCost    float64    `json:"Total Cost"`
	PlanRows     float64    `json:"Plan Rows"`
	Plans        []planNode `json:"Plans"`
}

// Shape is what the check found, for the server side log only.
type Shape struct {
	TooBroad  bool
	TopCost   float64
	WorstScan float64
	Reason    string
}

// CheckShape reads the JSON of EXPLAIN (FORMAT JSON) and decides. A plan it cannot read is refused:
// an unverifiable plan is not a safe one.
func CheckShape(explain []byte, ceiling, seqLimit float64) Shape {
	var top []struct {
		Plan planNode `json:"Plan"`
	}
	if err := json.Unmarshal(explain, &top); err != nil || len(top) == 0 {
		return Shape{TooBroad: true, Reason: "unreadable plan"}
	}
	sh := Shape{TopCost: top[0].Plan.TotalCost}
	if sh.TopCost > ceiling {
		sh.TooBroad, sh.Reason = true, "plan cost above the ceiling"
	}
	var walk func(n planNode)
	walk = func(n planNode) {
		if n.NodeType == "Seq Scan" && largeTables[n.RelationName] {
			if n.TotalCost > sh.WorstScan {
				sh.WorstScan = n.TotalCost
			}
			if n.TotalCost > seqLimit && !sh.TooBroad {
				sh.TooBroad, sh.Reason = true, "sequential scan of a large table"
			}
		}
		for _, p := range n.Plans {
			walk(p)
		}
	}
	walk(top[0].Plan)
	return sh
}
