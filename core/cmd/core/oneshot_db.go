// oneshot_db.go is the one-shot roles' database URL rule (HH-199, the
// two names for the database URL): one name decides where migrate, seed
// and demoseed act, the decision is made before anything connects, and
// the target is printed before anything acts, so an operator who sets
// both variables to different values is refused loudly instead of
// migrating one database while serve reads another with nothing
// reported. serve and the demo build keep their own reads
// (CORE_DATABASE_URL, required, no default), and config.Load's
// DATABASE_URL default stays for its other consumers; the one-shot
// roles alone stop taking the URL from it.
package main

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
)

// allowDevDefaultDB guards the localhost dev default the way seed's
// ALLOW_SEED is guarded: the exact word true, nothing else, so a stray
// "yes" or "1" in a shared environment file cannot quietly point a
// one-shot role at the default database.
const allowDevDefaultDB = "ALLOW_DEV_DEFAULT_DB"

// resolveOneshotDatabaseURL picks the one URL a one-shot role acts on.
// CORE_DATABASE_URL wins, DATABASE_URL is the legacy fallback (CI and
// the demo entrypoint export it), both set and equal is one target, and
// both set and different refuses: that pair is two names for what
// should be one database, and silently picking either is how a migrate
// lands on someone else's ledger. Whitespace around each value is
// trimmed before the comparison and the comparison itself is exact:
// two URLs that differ only in surrounding whitespace are one target,
// and anything else, a different port, database or parameter, is two.
// With neither variable set, the localhost dev default is reachable
// only behind ALLOW_DEV_DEFAULT_DB=true; without the flag the refusal
// says how to set the URL, because a silent default is the defect's
// quiet half.
func resolveOneshotDatabaseURL(getenv func(string) string) (string, error) {
	core := strings.TrimSpace(getenv("CORE_DATABASE_URL"))
	legacy := strings.TrimSpace(getenv("DATABASE_URL"))
	switch {
	case core != "" && legacy != "" && core != legacy:
		// The refusal names the two variables and never their values: a
		// database URL carries its password, and an error message is the
		// one line guaranteed to reach a log.
		return "", fmt.Errorf("CORE_DATABASE_URL and DATABASE_URL are both set and name different targets: set only one (the values are not echoed because a database URL carries its password)")
	case core != "":
		return core, nil
	case legacy != "":
		return legacy, nil
	case getenv(allowDevDefaultDB) == "true":
		return config.DevDatabaseURLDefault, nil
	}
	return "", fmt.Errorf("neither CORE_DATABASE_URL nor DATABASE_URL is set: set CORE_DATABASE_URL (or DATABASE_URL, the legacy fallback) to the database to act on; the localhost dev default is reached only by setting %s=true", allowDevDefaultDB)
}

// printOneshotDatabaseTarget prints where a one-shot role is about to
// act: the host (with its port) and the database name, and nothing
// else. Never userinfo, never the password, never the query string: a
// DSN is a credential by content, and the operator's question is only
// which database this run touches. The writer seam lets tests capture
// the line.
func printOneshotDatabaseTarget(w io.Writer, role, dsn string) {
	host, database := dsnTarget(dsn)
	fmt.Fprintf(w, "%s: database target: host=%s database=%s\n", role, host, database)
}

// dsnTarget reduces a DSN to its host (with port) and database name.
// URL form first (every DSN this tree writes is URL form); the keyword
// form second, best effort, so a keyword DSN still names its target. A
// DSN in neither shape is named as absent rather than guessed, the same
// never-render-a-confident-wrong-number rule the rest of the tree
// carries.
func dsnTarget(dsn string) (host, database string) {
	if u, err := url.Parse(strings.TrimSpace(dsn)); err == nil && u.Scheme != "" {
		host, database = u.Host, strings.TrimPrefix(u.Path, "/")
	} else {
		var port string
		for _, field := range strings.Fields(dsn) {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			switch key {
			case "host":
				host = value
			case "port":
				port = value
			case "dbname":
				database = value
			}
		}
		if host != "" && port != "" {
			host = host + ":" + port
		}
	}
	if host == "" {
		host = "(no host in the DSN)"
	}
	if database == "" {
		database = "(no database name in the DSN)"
	}
	return host, database
}
