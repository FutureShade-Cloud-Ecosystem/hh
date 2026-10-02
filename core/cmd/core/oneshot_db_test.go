package main

// The resolver's table and the print helper's reduction, the unit half
// of HH-199's legs. The role-level half beside hh199_oneshot_dburl_test.go
// proves the wiring through the built binary; these prove the rule
// itself, including the with-flag half of the neither-set leg, which
// cannot be a role-level leg: running the real role on the localhost
// default would point it at whatever answers there, and on a machine
// whose shared dev database answers on that port a passing test would
// have acted on it.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
)

func hh199EnvFunc(set map[string]string) func(string) string {
	return func(k string) string { return set[k] }
}

func TestResolveOneshotDatabaseURL(t *testing.T) {
	const coreURL = "postgres://op:core-secret@db-one.example:5432/onedb?sslmode=disable"
	const legacyURL = "postgres://op:legacy-secret@db-two.example:5432/twodb?sslmode=disable"
	cases := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{
			name:    "both set and different refuses",
			env:     map[string]string{"CORE_DATABASE_URL": coreURL, "DATABASE_URL": legacyURL},
			wantErr: true,
		},
		{
			name: "both set and equal proceeds on the one value",
			env:  map[string]string{"CORE_DATABASE_URL": coreURL, "DATABASE_URL": coreURL},
			want: coreURL,
		},
		{
			name: "equal after trimming surrounding whitespace is one target",
			env: map[string]string{
				"CORE_DATABASE_URL": "  " + coreURL + "  ",
				"DATABASE_URL":      coreURL,
			},
			want: coreURL,
		},
		{
			name: "only CORE_DATABASE_URL proceeds on it",
			env:  map[string]string{"CORE_DATABASE_URL": coreURL},
			want: coreURL,
		},
		{
			name: "only DATABASE_URL proceeds on it (the fallback)",
			env:  map[string]string{"DATABASE_URL": legacyURL},
			want: legacyURL,
		},
		{
			name:    "neither set without the flag refuses",
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name:    "neither set with a flag that is not the exact word true refuses",
			env:     map[string]string{"ALLOW_DEV_DEFAULT_DB": "yes"},
			wantErr: true,
		},
		{
			name: "neither set with the flag reaches the localhost dev default",
			env:  map[string]string{"ALLOW_DEV_DEFAULT_DB": "true"},
			want: config.DevDatabaseURLDefault,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveOneshotDatabaseURL(hh199EnvFunc(tc.env))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolver returned %q, want a refusal", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolver refused (%v) where the case must proceed", err)
			}
			if got != tc.want {
				t.Fatalf("resolver returned %q, want %q", got, tc.want)
			}
		})
	}
}

// The both-different refusal names both variables and says they name
// different targets, and carries neither value: a database URL is a
// credential by content, and the refusal is the one line guaranteed to
// reach a log. The neither-set refusal says how to set the URL and names
// the flag that alone reaches the default.
func TestResolveOneshotDatabaseURLRefusalText(t *testing.T) {
	const coreURL = "postgres://op:core-secret@db-one.example:5432/onedb?sslmode=disable"
	const legacyURL = "postgres://op:legacy-secret@db-two.example:5432/twodb?sslmode=disable"

	_, err := resolveOneshotDatabaseURL(hh199EnvFunc(map[string]string{
		"CORE_DATABASE_URL": coreURL, "DATABASE_URL": legacyURL}))
	if err == nil {
		t.Fatal("both set and different did not refuse")
	}
	for _, want := range []string{"CORE_DATABASE_URL", "DATABASE_URL", "different"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the both-different refusal does not contain %q: %v", want, err)
		}
	}
	for _, secret := range []string{"core-secret", "legacy-secret", "db-one.example", "db-two.example", "onedb", "twodb"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the both-different refusal echoes the DSN value %q: %v", secret, err)
		}
	}

	_, err = resolveOneshotDatabaseURL(hh199EnvFunc(nil))
	if err == nil {
		t.Fatal("neither variable set did not refuse")
	}
	for _, want := range []string{"CORE_DATABASE_URL", "DATABASE_URL", "ALLOW_DEV_DEFAULT_DB"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the neither-set refusal does not contain %q: %v", want, err)
		}
	}
}

func TestPrintOneshotDatabaseTarget(t *testing.T) {
	cases := []struct {
		name     string
		role     string
		dsn      string
		wantLine string
		forbid   []string
	}{
		{
			name:     "a URL DSN prints host with port and database name",
			role:     "migrate",
			dsn:      "postgres://hh199operator:print-secret@127.0.0.1:5599/printdb?sslmode=disable&application_name=probe",
			wantLine: "migrate: database target: host=127.0.0.1:5599 database=printdb",
			forbid:   []string{"print-secret", "hh199operator", "sslmode", "application_name"},
		},
		{
			name:     "the guarded localhost default prints its host and name",
			role:     "seed",
			dsn:      config.DevDatabaseURLDefault,
			wantLine: "seed: database target: host=localhost:5435 database=hardscapeos",
			forbid:   []string{"hardscape:", "sslmode"},
		},
		{
			name:     "a keyword DSN still names its target",
			role:     "demoseed",
			dsn:      "host=kb.example port=5433 dbname=kbdb user=kb password=kw-secret",
			wantLine: "demoseed: database target: host=kb.example:5433 database=kbdb",
			forbid:   []string{"kw-secret", "user=kb"},
		},
		{
			name:     "a URL whose query parameters override its host prints the override, the driver's target, not the decoy half",
			role:     "migrate",
			dsn:      "postgres://op:decoy-secret@decoy.example:5432/decoydb?host=real.example&port=6543&dbname=realdb",
			wantLine: "migrate: database target: host=real.example:6543 database=realdb",
			forbid:   []string{"decoy.example", "decoydb", "decoy-secret", "op:"},
		},
		{
			name:     "a socket DSN prints the socket directory and the database, no port tacked onto a path",
			role:     "seed",
			dsn:      "postgres://op@/sockdb?host=/var/run/postgresql",
			wantLine: "seed: database target: host=/var/run/postgresql database=sockdb",
			forbid:   []string{"/var/run/postgresql:5432"},
		},
		{
			name:     "a keyword DSN whose quoted password contains host= prints the host, never a password fragment",
			role:     "demoseed",
			dsn:      `host=real.example port=6543 dbname=realdb password="pass word host=decoy"`,
			wantLine: "demoseed: database target: host=real.example:6543 database=realdb",
			forbid:   []string{"pass word", "host=decoy", "decoy"},
		},
		{
			name:     "a keyword DSN with spaces around the equals parses the way the driver does",
			role:     "migrate",
			dsn:      "host = spaced.example dbname = spaceddb",
			wantLine: "migrate: database target: host=spaced.example:5432 database=spaceddb",
		},
		{
			name:     "a DSN the driver cannot parse is named as such, never guessed",
			role:     "migrate",
			dsn:      "not a dsn at all",
			wantLine: "migrate: database target: host=(unparseable DSN) database=(unparseable DSN)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printOneshotDatabaseTarget(&buf, tc.role, tc.dsn)
			if got := buf.String(); got != tc.wantLine+"\n" {
				t.Fatalf("printed %q, want %q", got, tc.wantLine)
			}
			for _, banned := range tc.forbid {
				if strings.Contains(buf.String(), banned) {
					t.Errorf("the printed target carries %q, which is not the host or the database name", banned)
				}
			}
		})
	}
}
