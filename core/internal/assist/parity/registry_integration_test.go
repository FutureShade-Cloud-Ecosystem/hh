//go:build integration

package parity

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

// The registry and the database say the same thing: every column of every view in schema ai is registered,
// every registered column exists, and none is secret.
func TestRegistry_EveryColumnOfEveryAIViewIsRegisteredExactly(t *testing.T) {
	env := testsupport.Open(t)
	rows, err := env.Admin.Query(testsupport.Ctx(t), `SELECT c.relname || '.' || a.attname FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'ai' AND c.relkind = 'v' AND a.attnum > 0 AND NOT a.attisdropped`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	inDB := map[string]bool{}
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatal(err)
		}
		inDB["ai."+col] = true
	}
	reg := assist.RegisteredColumns()
	for col := range inDB {
		class, ok := reg[col]
		if !ok {
			t.Errorf("the view column %s is in the database but not in the classification registry", col)
		}
		if class == assist.ClassSecret {
			t.Errorf("the view column %s is a secret column", col)
		}
	}
	for col := range reg {
		if !inDB[col] {
			t.Errorf("the registry names %s, which is in no view", col)
		}
	}
}
