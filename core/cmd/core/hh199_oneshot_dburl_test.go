package main

// HH-199, the two names for the database URL, proven at role level
// through the real binary. The one-shot roles (migrate, seed, demoseed)
// must refuse when CORE_DATABASE_URL and DATABASE_URL are both set and
// name different targets, must proceed on DATABASE_URL when
// CORE_DATABASE_URL is unset, and must print the host and database name
// of the target they act on before any connection is attempted, never
// the password the DSN carries.
//
// Every leg runs the built binary as a subprocess rather than calling a
// helper: the legs name behaviour, not symbols, so this same file runs
// against a tree without the fix and fails there for the defect's own
// reason (the refusal text is absent, the target print is absent, a
// connection is attempted where a refusal should have come first) and
// not because a new symbol does not resolve.
//
// The database ends of every leg are TCP sinks, one per leg on an
// ephemeral loopback port: a sink accepts and immediately closes each
// connection and counts what reached it. A proceed leg points its DSNs
// at the sink, so the role's connection attempt is counted and its
// handshake fails without a database anywhere; the printed target still
// appears, which is itself the ordering proof, because the connection
// it precedes never succeeds. A refusal leg asserts the sink count
// stays zero, which is the proof the refusal comes before any
// connection attempt. No leg touches a real database, and the neither-
// set leg runs inside a private user and network namespace where it can
// reach nothing even if its refusal were removed (see hh199RunRole).

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// hh199CoreBin is the built core binary the legs run, built once for the
// package's whole test run by TestMain below.
var hh199CoreBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hh199-core-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "hh199: temp dir for the core binary: %v\n", err)
		os.Exit(1)
	}
	bin := filepath.Join(dir, "core")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "hh199: building the core binary: %v\n%s", err, out)
		os.Exit(1)
	}
	hh199CoreBin = bin
	code := m.Run()
	// Removed here and not by a defer: os.Exit never runs deferred calls,
	// and the built binary is tens of megabytes every test run would
	// otherwise leave behind.
	os.RemoveAll(dir)
	os.Exit(code)
}

// hh199Sink is a TCP sink: it accepts and immediately closes every
// connection and counts what reached it. It is never a database, so a
// role pointed at it decides and prints and then fails its handshake,
// having written nothing anywhere.
type hh199Sink struct {
	addr  string
	count atomic.Int32
}

func hh199NewSink(t *testing.T) *hh199Sink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hh199: sink listen: %v", err)
	}
	s := &hh199Sink{addr: ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.count.Add(1)
			c.Close()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

// dsn builds a URL-form DSN at the sink with the given database name and
// password, so a leg's assertions can demand one and forbid the other.
func (s *hh199Sink) dsn(dbname, password string) string {
	return "postgres://hh199operator:" + password + "@" + s.addr + "/" + dbname + "?sslmode=disable"
}

// hh199Env builds the subprocess environment: the outer environment's
// database variables, environment word, vault key and every override
// flag are dropped, so only the leg's own variables decide where the
// role looks and whether it may proceed.
func hh199Env(set map[string]string) []string {
	out := make([]string, 0, len(os.Environ())+len(set))
	for _, kv := range os.Environ() {
		name := kv
		if eq := strings.IndexByte(kv, '='); eq >= 0 {
			name = kv[:eq]
		}
		switch {
		case name == "DATABASE_URL", name == "CORE_DATABASE_URL", name == "APP_ENV", name == "PAYMENT_VAULT_KEY":
			continue
		case strings.HasPrefix(name, "ALLOW_"):
			continue
		}
		out = append(out, kv)
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	return out
}

var (
	hh199NetNSOnce sync.Once
	hh199NetNSOK   bool
)

// hh199RunRole runs the built binary's role with the leg's environment
// and returns the combined output and exit code (a refused role exits
// non-zero through main's dispatch). isolated runs the role inside a
// private user and network namespace, so a leg whose role would
// otherwise fall back to the localhost default cannot reach any real
// listener even if its refusal were removed; the namespace's loopback
// is down, so the default target fails in milliseconds rather than
// hanging. Where no namespace can be had the isolated leg skips, on a
// developer's machine and on CI alike: running it plainly would let a
// regressed tree dial whatever answers on the default port, and a set
// CI variable is not evidence that port is dead.
func hh199RunRole(t *testing.T, role string, roleArgs []string, set map[string]string, isolated bool) (string, int) {
	t.Helper()
	prefix := []string{}
	if isolated {
		hh199NetNSOnce.Do(func() {
			hh199NetNSOK = exec.Command("unshare", "-Urn", "--", "true").Run() == nil
		})
		if !hh199NetNSOK {
			t.Skipf("no private network namespace on this machine: the isolated leg never runs without one")
		}
		prefix = []string{"unshare", "-Urn", "--"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := append(append(append([]string{}, prefix...), hh199CoreBin, role), roleArgs...)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = hh199Env(set)
	// The package directory on purpose: migrate globs db/migrations
	// relative to the working directory, and from here the glob finds
	// nothing, so even a leg that somehow reaches a live database cannot
	// apply a migration to it.
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("hh199: run %s: %v\n%s", role, err, out)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

// hh199TargetLine returns the shared printed-target line from a role's
// output, the line carrying "database target:". Empty means the role
// printed no target at all, which is the defect's silent half.
func hh199TargetLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "database target:") {
			return line
		}
	}
	return ""
}

// Leg 1, the both-set-and-differ refusal: every one-shot role refuses,
// names both variables, says they name different targets, leaks neither
// DSN (a refusal that echoed the URL would echo the password), and
// attempts no connection: the sink must stay at zero. Against a tree
// without the fix the role reads DATABASE_URL alone, connects to the
// sink (the count proves the attempt) and fails its handshake with no
// refusal text, so every assertion below fails there.
func TestHH199BothSetAndDifferRefusesBeforeAnyConnection(t *testing.T) {
	for _, tc := range []struct {
		role string
		args []string
	}{
		{"migrate", nil},
		{"seed", nil},
		{"demoseed", []string{"-plan"}},
	} {
		t.Run(tc.role, func(t *testing.T) {
			sink := hh199NewSink(t)
			out, code := hh199RunRole(t, tc.role, tc.args, map[string]string{
				"CORE_DATABASE_URL": sink.dsn("core-says-this", "core-only-secret"),
				"DATABASE_URL":      sink.dsn("legacy-says-that", "legacy-only-secret"),
			}, false)
			if code == 0 {
				t.Fatalf("%s ran to completion with both database variables set and different; output:\n%s", tc.role, out)
			}
			for _, want := range []string{"CORE_DATABASE_URL", "DATABASE_URL"} {
				if !strings.Contains(out, want) {
					t.Errorf("%s's refusal does not name %s; output:\n%s", tc.role, want, out)
				}
			}
			if !strings.Contains(out, "different") {
				t.Errorf("%s's refusal does not say the two variables name different targets; output:\n%s", tc.role, out)
			}
			for _, secret := range []string{"core-only-secret", "legacy-only-secret"} {
				if strings.Contains(out, secret) {
					t.Errorf("%s's refusal echoes a DSN value, leaking the password it carries; output:\n%s", tc.role, out)
				}
			}
			if n := sink.count.Load(); n != 0 {
				t.Errorf("%s attempted %d connection(s) before refusing: the refusal must come before any connection", tc.role, n)
			}
		})
	}
}

// Leg 2, the fallback: CORE_DATABASE_URL unset and DATABASE_URL set, the
// role proceeds on DATABASE_URL and the printed target derives from it,
// the sink's host and the DSN's database name. Against a tree without
// the fix the role also proceeds on DATABASE_URL (the connection is
// counted) but prints no target, which is exactly the silent half of
// the defect: the run acted on a target it never named. The exit code
// is deliberately not asserted: the sink's handshake fails by design,
// and this leg proves the decision and the print, not a completed
// migration.
func TestHH199FallbackProceedsOnLegacyURLAndPrintsItsTarget(t *testing.T) {
	for _, tc := range []struct {
		role string
		args []string
	}{
		{"migrate", nil},
		{"seed", nil},
		{"demoseed", []string{"-plan"}},
		{"demoseed", []string{"-apply"}},
	} {
		name := tc.role
		if len(tc.args) > 0 {
			name += " " + strings.Join(tc.args, " ")
		}
		t.Run(name, func(t *testing.T) {
			sink := hh199NewSink(t)
			out, _ := hh199RunRole(t, tc.role, tc.args, map[string]string{
				"DATABASE_URL": sink.dsn("fallbackdb", "fallback-password"),
			}, false)
			line := hh199TargetLine(out)
			if line == "" {
				t.Fatalf("the role printed no database target; output:\n%s", out)
			}
			if !strings.Contains(line, sink.addr) {
				t.Errorf("the printed target does not carry the DATABASE_URL host %s; line: %s", sink.addr, line)
			}
			if !strings.Contains(line, "fallbackdb") {
				t.Errorf("the printed target does not carry the DATABASE_URL database name fallbackdb; line: %s", line)
			}
			if strings.Contains(out, "fallback-password") {
				t.Errorf("the output leaks the DSN password; output:\n%s", out)
			}
			if n := sink.count.Load(); n == 0 {
				t.Errorf("the role did not proceed on DATABASE_URL: no connection was attempted")
			}
		})
	}
}

// Leg 3, the printed target: the host and the database name appear, a
// DSN carrying a password never leaks it anywhere in the output, and the
// target line carries no query string. CORE_DATABASE_URL alone points
// this leg, which is also the resolver's core-only path: the preferred
// name set by itself proceeds and prints its own target. The leg runs
// isolated because it leaves DATABASE_URL unset, and on a tree without
// the fix that is exactly the silent fall back to the localhost default:
// isolated, the fall back reaches only the namespace's dead loopback and
// no real database, while the unfixed error still names the default it
// silently chose.
func TestHH199PrintedTargetNamesHostAndDatabaseNeverTheSecrets(t *testing.T) {
	for _, tc := range []struct {
		role string
		args []string
	}{
		{"migrate", nil},
		{"seed", nil},
		{"demoseed", []string{"-plan"}},
	} {
		t.Run(tc.role, func(t *testing.T) {
			sink := hh199NewSink(t)
			out, _ := hh199RunRole(t, tc.role, tc.args, map[string]string{
				"CORE_DATABASE_URL": "postgres://hh199operator:print-leg-secret@" + sink.addr +
					"/printdb?sslmode=disable&application_name=hh199",
			}, true)
			line := hh199TargetLine(out)
			if line == "" {
				t.Fatalf("the role printed no database target; output:\n%s", out)
			}
			if !strings.Contains(line, sink.addr) {
				t.Errorf("the printed target does not carry the host %s; line: %s", sink.addr, line)
			}
			if !strings.Contains(line, "printdb") {
				t.Errorf("the printed target does not carry the database name printdb; line: %s", line)
			}
			if strings.Contains(out, "print-leg-secret") {
				t.Errorf("the output leaks the DSN password; output:\n%s", out)
			}
			if strings.Contains(line, "sslmode") || strings.Contains(line, "application_name") {
				t.Errorf("the printed target carries the DSN's query string; line: %s", line)
			}
		})
	}
}

// Leg 4, both set and equal: the role proceeds on the one target both
// variables agree on, prints it, and refuses nothing. Against a tree
// without the fix the role proceeds on the same URL but prints no
// target, so the print assertion is what fails.
func TestHH199BothSetAndEqualProceedsOnTheAgreedTarget(t *testing.T) {
	sink := hh199NewSink(t)
	agreed := sink.dsn("agreeddb", "agreed-password")
	out, code := hh199RunRole(t, "migrate", nil, map[string]string{
		"CORE_DATABASE_URL": agreed,
		"DATABASE_URL":      agreed,
	}, false)
	if strings.Contains(out, "different") && strings.Contains(out, "CORE_DATABASE_URL") && strings.Contains(out, "DATABASE_URL") {
		t.Fatalf("the role refused two variables set to the same URL; output:\n%s", out)
	}
	if code == 0 {
		t.Errorf("migrate reported success against a sink, which cannot serve a handshake; output:\n%s", out)
	}
	line := hh199TargetLine(out)
	if line == "" {
		t.Fatalf("the role printed no database target; output:\n%s", out)
	}
	if !strings.Contains(line, sink.addr) || !strings.Contains(line, "agreeddb") {
		t.Errorf("the printed target does not carry the agreed host and database name; line: %s", line)
	}
	if strings.Contains(out, "agreed-password") {
		t.Errorf("the output leaks the DSN password; output:\n%s", out)
	}
	if n := sink.count.Load(); n == 0 {
		t.Errorf("the role refused in practice: no connection was attempted on the agreed URL")
	}
}

// Leg 5, neither variable set: without the explicit dev flag the role
// refuses and says how to set the URL, touching nothing. Against a tree
// without the fix the role silently falls back to the localhost default
// and dials it, which is the defect's quiet half; this leg therefore
// runs isolated (a private network namespace whose loopback is down), so
// the demonstration against an unfixed tree reaches no real listener
// either. The with-flag half of the leg, where the default is allowed
// and its host and name print, is a unit leg over the resolver and the
// print helper beside oneshot_db.go: running it here would point the
// real binary at the real localhost default, and on a machine where a
// shared dev database answers there, a passing test would have migrated
// it.
func TestHH199NeitherSetRefusesWithoutTheDevFlag(t *testing.T) {
	out, code := hh199RunRole(t, "migrate", nil, nil, true)
	if code == 0 {
		t.Fatalf("migrate ran to completion with no database variable set at all; output:\n%s", out)
	}
	if !strings.Contains(out, "CORE_DATABASE_URL") {
		t.Errorf("the refusal does not tell the operator to set CORE_DATABASE_URL; output:\n%s", out)
	}
	if !strings.Contains(out, "ALLOW_DEV_DEFAULT_DB") {
		t.Errorf("the refusal does not name the flag that alone reaches the localhost dev default; output:\n%s", out)
	}
}
