//go:build !demo

// demoseed_glsworld_default.go, the default build's side of the demo world seam (the demo
// build's side, the Gable Landscape Supply world, is demoseed_glsworld_demo.go): this build
// carries no world rewrite at all. demoseed dispatches in every build, so the pair, not the
// entrypoint, is what holds the gate: the call sites in demoseed_main.go resolve to this
// file's no-ops at build time, a default binary's `demoseed -apply` plants the plan exactly as
// the plan's own literals read, and demoseed_glsworld_build_test.go proves it by reading both
// builds' binaries for the demo world's literals. The stubs import only what their signatures
// need and answer nothing, so the apply's reporting reads as it did before the seam existed.
package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedBuildIsDemo says this binary seeds the Ontario dealer (the demo build's twin says true).
const seedBuildIsDemo = false

// glsPrepareWorld is the no-op: no branch row moves.
func glsPrepareWorld(context.Context, *pgxpool.Pool) error { return nil }

// glsRewritePlan is the no-op: the plan's rows are returned as built.
func glsRewritePlan(rows []row) []row { return rows }

// glsRenamePlanted is the no-op: no migration-planted row is renamed.
func glsRenamePlanted(context.Context, glsTx, time.Time) (int, error) { return 0, nil }

// personaEmailAliases is empty: a default database keeps the personas' migration emails.
func personaEmailAliases() map[string]string { return nil }
