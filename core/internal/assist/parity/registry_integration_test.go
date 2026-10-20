//go:build integration

package parity

import (
	"sort"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

// registryProblems holds the registry to the database: every column of every view in schema ai AND schema
// ai_pro is registered, every registered column exists in some view, and none is a secret column. An
// unregistered column is secret, so it is in no catalogue, but a view that carries one must still fail CI.
func registryProblems(t *testing.T, env *testsupport.Env) []string {
	t.Helper()
	rows, err := env.Admin.Query(testsupport.Ctx(t), `SELECT n.nspname || '.' || c.relname || '.' || a.attname FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname IN ('ai', 'ai_pro') AND c.relkind IN ('v', 'm') AND a.attnum > 0 AND NOT a.attisdropped`)
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
		inDB[col] = true
	}
	reg := assist.RegisteredColumns()
	var out []string
	for col := range inDB {
		class, ok := reg[col]
		if !ok {
			out = append(out, "the view column "+col+" is in the database but not in the classification registry")
		}
		if class == assist.ClassSecret {
			out = append(out, "the view column "+col+" is a secret column")
		}
	}
	for col := range reg {
		if !inDB[col] {
			out = append(out, "the registry names "+col+", which is in no view")
		}
	}
	sort.Strings(out)
	return out
}

// The registry and the database say the same thing for both schemas.
func TestRegistry_EveryColumnOfEveryAIViewIsRegisteredExactly(t *testing.T) {
	env := testsupport.Open(t)
	for _, p := range registryProblems(t, env) {
		t.Error(p)
	}
}

// The check bites in ai_pro as well as in ai: a view planted in either schema is reported. (CI runs this
// package in the integration job and again in the assist parity path rule step.)
func TestRegistry_APlantedViewInEitherSchemaIsReported(t *testing.T) {
	env := testsupport.Open(t)
	for _, schema := range []string{"ai", "ai_pro"} {
		name := schema + ".planted_" + randomHex(t, 3)
		if _, err := env.Admin.Exec(testsupport.Ctx(t), `CREATE VIEW `+name+` AS SELECT 1 AS gate_code`); err != nil {
			t.Fatalf("plant %s: %v", name, err)
		}
		problems := registryProblems(t, env)
		_, _ = env.Admin.Exec(testsupport.Ctx(t), `DROP VIEW `+name)
		found := false
		for _, p := range problems {
			found = found || strings.Contains(p, name+".gate_code is in the database but not in the classification registry")
		}
		if !found {
			t.Errorf("a view planted in %s was not reported: %v", schema, problems)
		}
	}
}
