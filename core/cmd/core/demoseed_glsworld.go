// demoseed_glsworld.go, the part of the demo world seam both builds share: the
// transaction shape the rename pass needs. The seam itself is a pair, the default
// build's no-ops in demoseed_glsworld_default.go and the demo build's Gable Landscape
// Supply world in demoseed_glsworld_demo.go.
package main

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// glsTx is what the rename pass of the demo world needs from the apply's transaction: the
// INSERT-only applier's Exec, and a Query to read the rows it renames.
type glsTx interface {
	txExec
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}
