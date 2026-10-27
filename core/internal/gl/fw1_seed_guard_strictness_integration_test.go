//go:build integration

// fw1_seed_guard_strictness_integration_test.go, FW1-09b round 7: the seed guard must be strict.
//
// The guard lives in TestMain, so nothing inside one process can watch it: the only way to prove
// what it does is to run this package's own test binary again and read what it says and how it
// ended. Two legs, both about the guard's own behaviour:
//
//   - a guard that could not read its BEFORE snapshot must FAIL the package, not warn and let the
//     run through (S3's round 6 P3: a run it cannot watch is a run it must not pass);
//   - the "changed" line must name the account's OLD TYPE, where the old code printed its name
//     (S3's round 6 P3, the same note).
//
// The second leg changes a seeded account, because a change has to survive until the guard looks
// (the guard runs after the package's tests): the change is the subprocess's probe's, and this leg
// puts the account back whatever happens.
package gl

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// fw1GuardRun runs this package's test binary again with the given arguments and environment, and
// answers its exit code and its output.
func fw1GuardRun(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run the package's own binary again: %v", err)
	}
	return exit.ExitCode(), string(out)
}

// The guard could not read its before snapshot: the package must fail, not warn and pass.
func TestIntegration_FW1SeedGuardFailsWhenItCannotWatchTheRun(t *testing.T) {
	// A database that is not there: the guard's before snapshot cannot be read. No leg runs in the
	// subprocess (its name matches nothing), so the package's exit code is the guard's own answer.
	code, out := fw1GuardRun(t,
		[]string{"DATABASE_URL=postgres://postgres:postgres@127.0.0.1:1/postgres?sslmode=disable&connect_timeout=1"},
		"-test.run=TestFW1SeedGuardNoSuchLegRuns")
	if code == 0 {
		t.Fatalf("the seed guard let a run it could not watch pass (exit 0); it must fail the package: %s", out)
	}
	if !strings.Contains(out, "the before snapshot could not be read") {
		t.Fatalf("the guard did not say why it failed the package: %s", out)
	}
}

// The probe changes a seeded account and leaves it changed, so the guard, which looks after the
// package's tests, sees a change and says what changed. It runs only inside the subprocess the
// leg below starts: in this package's own run it would leave the account changed for every other
// leg.
func TestFW1SeedGuardChangeProbe(t *testing.T) {
	if os.Getenv("FW1_SEED_GUARD_PROBE") == "" {
		t.Skip("the probe runs only inside the guard's own subprocess")
	}
	pool := intPool(t)
	if _, err := pool.Exec(context.Background(),
		`UPDATE gl_accounts SET account_type = 'ASSET' WHERE code = '9999'`); err != nil {
		t.Fatalf("change the account: %v", err)
	}
}

// The "changed" line names the account's old type, where the old code printed its name.
func TestIntegration_FW1SeedGuardNamesTheOldTypeWhenAnAccountChanges(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var was string
	if err := pool.QueryRow(ctx, `SELECT account_type FROM gl_accounts WHERE code = '9999'`).Scan(&was); err != nil {
		t.Fatalf("read the account: %v", err)
	}
	// The account is the seed's, not ours: it goes back whatever happens.
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx,
			`UPDATE gl_accounts SET account_type = $1 WHERE code = '9999'`, was); err != nil {
			t.Errorf("put the account back: %v", err)
		}
	})

	code, out := fw1GuardRun(t,
		[]string{"FW1_SEED_GUARD_PROBE=1"},
		"-test.run=TestFW1SeedGuardChangeProbe")
	if code == 0 {
		t.Fatalf("the seed guard passed a run that changed an account (exit 0); it must fail the package: %s", out)
	}
	if !strings.Contains(out, "gl_account 9999 ("+was+") changed") {
		t.Fatalf("the changed line does not name the account's old type %s: %s", was, out)
	}
}
