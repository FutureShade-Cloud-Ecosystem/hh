//go:build !demo

// demoseed_gableaddr_default.go, the default build's side of the Gable-address seam (the demo
// build's side, the real rename, is demoseed_gableaddr_demo.go): this build carries no rename path
// at all. demoseed dispatches in every build, so the pair, not the entrypoint, is what holds the
// gate: the call site in demoseed_main.go resolves to this file's no-op at build time, a default
// binary's `demoseed -apply` writes no system_settings, warranty_vendor_profiles or users row from
// the rename, and demoseed_gableaddr_build_test.go proves it by grepping both builds' binaries for
// the rename's literals. The stub imports nothing and answers 0, so the apply's reporting reads as
// it did before the rename existed.
package main

import (
	"context"
	"os"
	"time"
)

// renameGableAddresses is the no-op: no statement runs and no row moves, so a default build's apply
// plants its INSERT-only world and touches nothing else.
func renameGableAddresses(context.Context, txExec, time.Time) (int, error) {
	return 0, nil
}

// renameGableDisplayNames is the no-op: the default build carries no display_name rename path.
func renameGableDisplayNames(context.Context, txExec, time.Time) (int, error) {
	return 0, nil
}

// gableAddressesPlan is the no-op: the default build's plan says nothing about an address rename
// pass it does not carry.
func gableAddressesPlan(*os.File) {}
