//go:build integration

package lien

import (
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit/legsweep"
)

// Row 0.46: the package leaves CI's one database as it found it. See legsweep.GuardMain.
func TestMain(m *testing.M) { os.Exit(legsweep.GuardMain(m)) }
