// Command demoseed plants a demo world on an EXISTING database, additively.
//
//	go run ./cmd/demoseed -plan          # print every row it would write, and write nothing
//	go run ./cmd/demoseed -apply         # write the missing rows, in one transaction
//	go run ./cmd/demoseed -apply -now 2026-09-01T09:00:00Z   # pin the clock
//
// THE CONSTRAINT THIS COMMAND WAS BUILT AROUND. Staging carries real homework from live testers and
// must never be wiped, reset, truncated or reseeded. So: nothing here deletes, nothing updates,
// nothing truncates, and nothing mints from a shared sequence. Every write is an INSERT with an
// explicit deterministic id and ON CONFLICT DO NOTHING (ids.go explains the derivation; apply.go is
// the only writer and can express no other statement kind). Running it twice produces the same
// world: the second run's inserts all conflict, create nothing, and report `created=0`.
//
// -plan IS THE DEFAULT. A command that can write to staging should not do so because somebody
// pressed enter with no arguments. -apply is required, and it is the only flag that writes.
// demoseed_main carries the pin's cmd/demoseed/main.go (the fold:
// one-shot subcommand of cmd/core, plan and apply modes preserved).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/logging"
)

// demoseedRole is the demoseed subcommand's entrypoint (the pin's
// cmd/demoseed main, folded here with its behaviour unchanged).
func demoseedRole(args []string, out *os.File, getenv func(string) string) error {
	return run(args, out)
}

func run(args []string, out *os.File) error {
	fs := flag.NewFlagSet("demoseed", flag.ContinueOnError)
	doApply := fs.Bool("apply", false, "write the missing rows (without this the command only prints its plan)")
	// -plan HAD NO FLAG, AND THIS FILE'S OWN HEADER DOCUMENTS IT. `demoseed -plan`, the invocation
	// printed at line 3, the one a runbook copies, exited 1 with "flag provided but not defined".
	// Dry-run mode was reachable only by passing NOTHING, which is the one way of invoking a command
	// that also looks like a mistake. Accepting the flag explicitly costs a line and makes the
	// documented call work; it is still the default, so nothing about the safe posture changes.
	doPlan := fs.Bool("plan", false, "print the plan and write nothing (the default; accepted so the documented invocation works)")
	nowFlag := fs.String("now", "", "RFC3339 instant to seed relative to (default: the wall clock, once, at start)")
	verbose := fs.Bool("ids", false, "with -plan, also print every row id the plan would write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Both flags together is not a preference to resolve, it is two contradictory instructions about
	// whether to write to a database somebody else is using. Refuse, and say which one to drop.
	if *doPlan && *doApply {
		return fmt.Errorf("demoseed: -plan and -apply say opposite things, pass one")
	}

	cfg := config.Load()
	log := logging.New(cfg.Env)
	ctx := context.Background()

	// THE INJECTED CLOCK. Read exactly once, here, and threaded through the plan as a value. Nothing
	// downstream of this line calls time.Now(): two runs pinned to the same -now produce byte-identical
	// rows, which is what makes "run it twice and compare the counts" a real proof rather than a
	// coincidence of two runs happening in the same second.
	now := time.Now().UTC()
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			return fmt.Errorf("demoseed: -now must be RFC3339 (e.g. 2026-09-01T09:00:00Z): %w", err)
		}
		now = t.UTC()
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("demoseed: cannot connect: %w", err)
	}
	defer pool.Close()

	// The salvaged composition root still builds. It is constructed here, and its construction is
	// the assertion, so this command cannot silently drift away from cmd/server's wiring while
	// claiming to be a transcription of it. The demo world is written as rows rather than driven
	// through these services for one reason, recorded in NOTES: a service call mints its own ids and
	// bumps shared sequences, and neither can be made idempotent against a database somebody else is
	// using.
	if _, err := compose(pool, log, cfg.PaymentVaultKey); err != nil {
		return fmt.Errorf("demoseed: composing modules: %w", err)
	}

	branches, err := resolveBranches(ctx, pool)
	if err != nil {
		return err
	}
	cats, err := resolveCategories(ctx, pool)
	if err != nil {
		return err
	}
	// The demo LOGINS 0200 planted. Read here, beside the branches and the categories, because the
	// plan may not read a database, and reported below, because "which personas does this box have"
	// is the first question when a driver persona's run sheet comes up empty.
	personas, err := resolvePersonas(ctx, pool)
	if err != nil {
		return err
	}
	seal, keyed, err := newSealer(cfg.PaymentVaultKey)
	if err != nil {
		return err
	}

	rows, summary, err := buildPlan(planInput{
		Now: now, Branches: branches, Categories: cats, Personas: personas, Seal: seal})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "demoseed %s\n", demoMarker)
	fmt.Fprintf(out, "  clock          %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(out, "  database       %s\n", redactDSN(cfg.DatabaseURL))
	fmt.Fprintf(out, "  branches       %s\n", branchLine(branches))
	fmt.Fprintf(out, "  demo logins    %s\n", personaLine(personas))
	fmt.Fprintf(out, "  POD sealing    %s\n", sealMode(keyed))
	fmt.Fprintf(out, "  plan           %d rows across %d tables, dated %s … %s\n\n",
		summary.Rows, len(summary.ByTable), summary.FirstDate, summary.LastDate)
	writeSummary(out, summary)
	if *verbose {
		fmt.Fprintln(out, "\nrow ids (this list is also the removal manifest):")
		for _, r := range rows {
			fmt.Fprintf(out, "  %-28s %v\n", r.table, r.vals[0])
		}
	}

	if !*doApply {
		fmt.Fprintln(out, "\nnothing written, re-run with -apply to write the missing rows.")
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("demoseed: begin: %w", err)
	}
	res, err := apply(ctx, tx, rows)
	if err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("demoseed: nothing was written: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("demoseed: commit: %w", err)
	}

	fmt.Fprintf(out, "\napplied: %d proposed · %d created · %d already present\n",
		res.Proposed, res.Created, res.Skipped)
	if res.Created == 0 {
		fmt.Fprintln(out, "the demo world was already complete, this run changed nothing.")
	}
	for _, t := range sortedKeys(res.ByTable) {
		fmt.Fprintf(out, "  %-28s %d\n", t, res.ByTable[t])
	}
	return nil
}

func writeSummary(out *os.File, s planSummary) {
	fmt.Fprintln(out, "what this plants")
	fmt.Fprintf(out, "  orders       %s\n", counts(s.Orders))
	fmt.Fprintf(out, "  AR aging     %s\n", counts(s.Aging))
	fmt.Fprintf(out, "  stops        %s\n", counts(s.Stops))
	fmt.Fprintf(out, "  pick tasks   %s\n", counts(s.Tasks))
	fmt.Fprintln(out, "  load fill")
	for _, l := range s.RunFill {
		fmt.Fprintf(out, "    %s\n", l)
	}
	fmt.Fprintln(out, "  will-call")
	for _, l := range s.WillCall {
		fmt.Fprintf(out, "    %s\n", l)
	}
	// THE ONLY ROWS THAT GROW PER DEPLOY, printed rather than described, because "which rows are
	// day-keyed" is a question about THIS run's plan and an operator should not have to read ids.go
	// to answer it. Everything not on this list is written once, ever.
	// KEYS, not rows, and the difference is one line of honesty: pos_sale's idempotency_key is minted
	// from a day key too, it has to be, being UNIQUE, or tomorrow's counter sale would collide with
	// today's and abort the transaction, but it is a column value rather than a row of its own.
	fmt.Fprintf(out, "  day-keyed   %d keys carry this calendar day; every other row is written once, ever\n",
		len(s.DayKeys))
	for _, k := range s.DayKeys {
		fmt.Fprintf(out, "    %s\n", k)
	}
	fmt.Fprintln(out, "  rows by table")
	for _, t := range sortedKeys(s.ByTable) {
		fmt.Fprintf(out, "    %-26s %d\n", t, s.ByTable[t])
	}
}

func counts(m map[string]int) string {
	parts := make([]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, " · ")
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func branchLine(bs []branch) string {
	parts := make([]string, len(bs))
	for i, b := range bs {
		parts[i] = b.Code
	}
	return strings.Join(parts, ", ")
}

// personaLine names which of 0200's three demo logins this database actually has. It prints the
// EMAILS rather than a count, because the only useful form of this line is the one that tells you
// which account is missing: a driver persona whose run sheet is empty and a driver persona who does
// not exist are two different problems with one symptom.
func personaLine(personas map[string]string) string {
	found := make([]string, 0, len(demoPersonaEmails))
	for _, e := range demoPersonaEmails {
		if personas[e] != "" {
			found = append(found, e)
		}
	}
	if len(found) == 0 {
		return "none, this database is behind migration 0200, so no demo row will name a person"
	}
	return strings.Join(found, ", ")
}

func sealMode(keyed bool) string {
	if keyed {
		return "PAYMENT_VAULT_KEY is set, demo attestations are sealed with the deployment's key"
	}
	return "PAYMENT_VAULT_KEY is unset, demo attestations are sealed with an ephemeral key and cannot be reopened (this is fine: no surface opens one)"
}

// redactDSN prints WHERE the command is pointed without printing the credential it points with.
func redactDSN(dsn string) string {
	at := strings.LastIndex(dsn, "@")
	scheme := strings.Index(dsn, "://")
	if at < 0 || scheme < 0 || at < scheme {
		return dsn
	}
	return dsn[:scheme+3] + "…@" + dsn[at+1:]
}
