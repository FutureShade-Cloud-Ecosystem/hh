// Command demoseed plants a demo world on an EXISTING database, additively.
//
//	go run ./cmd/demoseed -plan          # print every row it would write, and write nothing
//	go run ./cmd/demoseed -apply         # write the missing rows, in one transaction
//	go run ./cmd/demoseed -apply -now 2026-09-01T09:00:00Z   # pin the clock
//
// THE CONSTRAINT THIS COMMAND WAS BUILT AROUND. Staging carries real homework from live testers and
// must never be wiped, reset, truncated or reseeded. So: nothing here deletes, nothing truncates,
// and the only sequence it draws from is the sync scope counters, by the demo build's repair alone
// (the seq repair named below). Every write is an INSERT with an explicit deterministic
// id and ON CONFLICT DO NOTHING (ids.go explains the derivation; apply.go is the only writer and
// can express no other statement kind). The TWO exceptions are demo-build-only pairs, each a fixed
// list of guarded UPDATEs that repair rows this command itself (or a migration) already planted,
// never a live tester's row: the scrub-name rename pass (the demoseed_scrubname pair), which moves
// migration-planted demo customers off their scrub placeholders, and the seed-repair pass (the
// demoseed_repair pair), which re-homes the one account whose home branch a later ruling moved and
// stamps the demo org on the Driver's day-keyed rows the boot's first apply wrote before the
// populate provisioned the org. The seq repair (demoseed_seqrepair_demo.go) is the third: it gives
// every row still carrying seq NULL in a table with a seq column its place in the table's sync scope
// (and the demo org where the row has none), so the device sync feeds serve the world; it touches
// only rows nobody sequenced, draws nothing on a second run, and the default build carries none of it.
// Each statement in the first two names the row by fixed id AND the exact value being repaired, so nothing
// outside the fixed list moves, a second run matches no row, and a hand-edited row is left alone.
// Running it twice produces the same world: the second run's inserts all conflict, create nothing,
// and report `created=0`.
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
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// demoseedRole is the demoseed subcommand's entrypoint (the pin's
// cmd/demoseed main, folded here with its behaviour unchanged).
func demoseedRole(args []string, out *os.File, getenv func(string) string) error {
	return run(args, out, getenv)
}

func run(args []string, out *os.File, getenv func(string) string) error {
	// The finance box refuses the role before anything else: the demo world
	// is additive by construction, and the finance box is exactly the world
	// it must never be added to. First the box statement, before even the
	// database is resolved; then, once connected, the database's own
	// box_kind through the settings seam, before the plan reads a branch or
	// writes a row.
	if strings.TrimSpace(getenv(envBoxKind)) == boxKindFinance {
		return fmt.Errorf("demoseed: refusing to run: %s=%s (this command writes a demo world, and the finance box carries no demo world)", envBoxKind, boxKindFinance)
	}

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

	// The target comes from the one-shot resolver, not config.Load:
	// HH-199's rule (oneshot_db.go), refused before anything here runs
	// and printed before the connection is attempted, on both the -plan
	// and -apply paths (the connection below is the first thing either
	// path does with a database). cfg still supplies Env and
	// PaymentVaultKey.
	dsn, err := resolveOneshotDatabaseURL(getenv)
	if err != nil {
		return fmt.Errorf("demoseed: %w", err)
	}

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

	printOneshotDatabaseTarget(out, "demoseed", dsn)
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("demoseed: cannot connect: %w", err)
	}
	defer pool.Close()

	// The database's own answer: a database a finance box seeded is the
	// finance box's world, and the demo writer touches none of it (the
	// settings module owns system_settings; this is the only read).
	if kind, err := settings.NewDealerConfigReader(pool).BoxKind(ctx); err != nil {
		return fmt.Errorf("demoseed: %s could not be read: %w", settings.KeyBoxKind, err)
	} else if kind == boxKindFinance {
		return fmt.Errorf("demoseed: refusing to run: the database's %s is %s (the finance box's world takes no demo rows)", settings.KeyBoxKind, boxKindFinance)
	}

	// The salvaged composition root still builds. It is constructed here, and its construction is
	// the assertion, so this command cannot silently drift away from cmd/server's wiring while
	// claiming to be a transcription of it. The demo world is written as rows rather than driven
	// through these services for one reason, recorded in NOTES: a service call mints its own ids and
	// bumps shared sequences, and neither can be made idempotent against a database somebody else is
	// using.
	if _, err := compose(pool, log, cfg.PaymentVaultKey); err != nil {
		return fmt.Errorf("demoseed: composing modules: %w", err)
	}

	// The demo world seam (the glsworld pair): in the demo build, -apply moves the two branch rows
	// onto the demo dealer before the plan reads them, because the plan derives every document
	// reference from the branch code. The default build's side is a no-op.
	if *doApply {
		if err := glsPrepareWorld(ctx, pool); err != nil {
			return err
		}
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
	contractor, err := resolveContractor(ctx, pool)
	if err != nil {
		return err
	}
	seal, keyed, err := newSealer(cfg.PaymentVaultKey)
	if err != nil {
		return err
	}

	rows, summary, err := buildPlan(planInput{
		Now: now, Branches: branches, Categories: cats, Personas: personas, Seal: seal, Contractor: contractor})
	if err != nil {
		return err
	}

	// The demo world's words (the glsworld pair): the built plan's strings pass through the demo
	// dictionary before anything prints or writes. The default build returns the rows as built.
	rows = glsRewritePlan(rows)
	summary = glsRewriteSummary(summary)

	fmt.Fprintf(out, "demoseed %s\n", demoMarker)
	fmt.Fprintf(out, "  clock          %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(out, "  database       %s\n", redactDSN(dsn))
	fmt.Fprintf(out, "  branches       %s\n", branchLine(branches))
	fmt.Fprintf(out, "  demo logins    %s\n", personaLine(personas))
	fmt.Fprintf(out, "  POD sealing    %s\n", sealMode(keyed))
	// The rename pass is the demo build's (the scrubname pair): the demo build's plan
	// says how many migration-planted customers -apply renames, the default build's
	// says nothing because it carries no rename at all.
	scrubNamesPlan(out)
	// The Gable-address rename pass is the demo build's too (the gableaddr pair): the demo build's
	// plan says how many migration-planted email addresses -apply renames onto the demo dealer's
	// addresses; the default build's says nothing.
	gableAddressesPlan(out)
	// The seed-repair pass is the demo build's too (the repair pair): the plan says the guarded
	// repair -apply would make, and only when the row still sits where the pass moves it from,
	// so a world with nothing to repair reports nothing.
	repairDemoWorldPlan(ctx, out, pool, branches, now)
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
	// The one write the INSERT-only applier cannot express: the migration-planted demo
	// customers' scrub placeholder names (the scrubname pair). The symbol resolves at
	// build time: the real rename in the demo build, a no-op stub in the default build,
	// which writes no customers.name row from this call. Inside the same transaction,
	// so the world still lands all-or-nothing.
	renamed, err := renameScrubbedDemoCustomers(ctx, tx, now)
	if err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("demoseed: nothing was written: %w", err)
	}
	// The demo world's migration-planted words (the glsworld pair), in the same transaction for the
	// same reason: the rows the migrations plant carry the first dealer's names, and the demo build
	// moves them through the demo dictionary, one guarded update per changed value.
	worldRenamed, err := glsRenamePlanted(ctx, tx, now)
	if err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("demoseed: nothing was written: %w", err)
	}
	// The Gable-address display-name rename pass (the gableaddr pair), in the same transaction for the
	// same reason: the migration-planted demo staff display names move onto invented Gable Landscape
	// Supply role labels. This runs BEFORE the email rename because the UAT staff account has no fixed
	// id and is looked up by its migration-planted email; once that email moves, the display_name guard
	// would match no row.
	displayRenamed, err := renameGableDisplayNames(ctx, tx, now)
	if err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("demoseed: nothing was written: %w", err)
	}
	// The Gable-address rename pass (the gableaddr pair), in the same transaction: the migration-planted
	// demo email addresses move onto invented Gable Landscape Supply addresses, one guarded UPDATE per
	// fixed id / exact old value.
	addrRenamed, err := renameGableAddresses(ctx, tx, now)
	if err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("demoseed: nothing was written: %w", err)
	}
	// The seed-repair pass (the repair pair), inside the same transaction for the same reason:
	// the world lands all-or-nothing. One guarded UPDATE, demo build only, its guarantees argued
	// in demoseed_repair_demo.go beside the scrub-name pass it follows.
	repaired, err := repairDemoWorld(ctx, tx, now, branches)
	if err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("demoseed: nothing was written: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("demoseed: commit: %w", err)
	}

	fmt.Fprintf(out, "\napplied: %d proposed · %d created · %d already present\n",
		res.Proposed, res.Created, res.Skipped)
	if renamed > 0 {
		fmt.Fprintf(out, "renamed:  %d migration-planted demo customer(s) off their scrub placeholder names\n", renamed)
	}
	if worldRenamed > 0 {
		fmt.Fprintf(out, "world words:  %d migration-planted value(s) moved onto the demo dealer's words\n", worldRenamed)
	}
	if addrRenamed > 0 {
		fmt.Fprintf(out, "gable addresses: %d migration-planted email address(es) moved onto Gable Landscape Supply addresses\n", addrRenamed)
	}
	if displayRenamed > 0 {
		fmt.Fprintf(out, "gable display names: %d migration-planted display name(s) moved onto Gable Landscape Supply role labels\n", displayRenamed)
	}
	if repaired > 0 {
		fmt.Fprintf(out, "repaired:  %d row(s) an older apply left wrong (a home branch, an empty product application, a missing org stamp)\n", repaired)
	}
	if res.Created == 0 && renamed == 0 && worldRenamed == 0 && addrRenamed == 0 && displayRenamed == 0 && repaired == 0 {
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
	if len(s.Quotes) == 0 {
		fmt.Fprintln(out, "  quotes       none, the two demo role logins are not on this database yet (apply again after the demo populate)")
	} else {
		fmt.Fprintln(out, "  quotes")
		for _, l := range s.Quotes {
			fmt.Fprintf(out, "    %s\n", l)
		}
		fmt.Fprintln(out, "  quote share links")
		for _, l := range s.QuoteLinks {
			fmt.Fprintf(out, "    %s\n", l)
		}
	}
	if len(s.ContractorLines) == 0 {
		fmt.Fprintln(out, "  contractor   none, the contractor account is not on this database yet (apply again after the demo populate)")
	} else {
		fmt.Fprintln(out, "  contractor world")
		for _, l := range s.ContractorLines {
			fmt.Fprintf(out, "    %s\n", l)
		}
	}
	fmt.Fprintln(out, "  deposit sales (item 9.9, walk them at the counter till and on Pay balance)")
	for _, l := range s.DepositOrders {
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
