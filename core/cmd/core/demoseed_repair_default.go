//go:build !demo

// demoseed_repair_default.go, the default build's side of the seed-repair seam (the demo build's
// side, the real guarded repair, is demoseed_repair_demo.go): this build carries no home-branch
// write at all. demoseed dispatches in every build, so the pair, not the entrypoint, is what holds
// the gate: the call sites in demoseed_main.go resolve to this file's no-ops at build time, and a
// default binary's `demoseed -apply` plants its INSERT-only world and touches no customers row's
// home branch. The stubs import only what their signatures need and answer 0 or print nothing, so
// the apply's reporting reads as it did before the repair existed.
package main

import (
	"context"
	"os"
	"time"
)

// repairDemoWorld is the no-op: no statement runs and no row moves.
func repairDemoWorld(context.Context, txExec, time.Time, []branch) (int, error) {
	return 0, nil
}

// repairDemoWorldPlan is the no-op: the default build's plan says nothing about a repair pass it
// does not carry.
func repairDemoWorldPlan(context.Context, *os.File, rowQuery, []branch, time.Time) {}
