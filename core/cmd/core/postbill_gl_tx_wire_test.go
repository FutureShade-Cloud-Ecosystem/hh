// postbill_gl_tx_wire_test.go, the production adapters for purchasing PostBill's GL seams carry the
// transaction variants: the compile-time pins fail the build if an adapter loses one, and the integration leg
// drives both through a real transaction and shows a rollback takes the journal with it.
package main

import "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"

var (
	_ purchasing.GLPoster     = w4cGLPoster{}
	_ purchasing.RuleResolver = glRuleAdapter{}
)
