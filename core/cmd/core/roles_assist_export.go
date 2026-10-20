// roles_assist_export.go carries the assist-billing-export role (TanyAI slice 2): a one-shot beside
// migrate that writes one month of the assistant's settled spend, per user and role, per audience,
// per alias and model, plus a dealer summary line carrying the org slug, as JSON or CSV on standard
// output. It is a job, not a route: no HTTP surface exists for it and no Dealer Admin screen shows
// it (review r2 P2-4). The database target is printed to standard error so standard output is the
// document alone.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

func assistBillingExportRole(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("assist-billing-export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	month := fs.String("month", "", "the month to export, YYYY-MM (required)")
	org := fs.String("org", "", "the dealer's org slug, carried on the summary line (required)")
	format := fs.String("format", "json", "json or csv")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := time.Parse("2006-01", strings.TrimSpace(*month)); err != nil {
		return fmt.Errorf("assist-billing-export: -month must be YYYY-MM, got %q", *month)
	}
	if strings.TrimSpace(*org) == "" {
		return errors.New("assist-billing-export: -org is required")
	}
	if *format != "json" && *format != "csv" {
		return fmt.Errorf("assist-billing-export: -format must be json or csv, got %q", *format)
	}

	dsn, err := resolveOneshotDatabaseURL(getenv)
	if err != nil {
		return fmt.Errorf("assist-billing-export: %w", err)
	}
	printOneshotDatabaseTarget(stderr, "assist-billing-export", dsn)
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("assist-billing-export: cannot connect: %w", err)
	}
	defer pool.Close()

	// The month is the dealer's month in the dealer's business zone, the one the caps and budgets use.
	zone, err := houseday.Resolve(ctx, newDealerZone(pool))
	if err != nil {
		return fmt.Errorf("assist-billing-export: %w", err)
	}
	from, to, err := parseExportMonth(*month, zone)
	if err != nil {
		return err
	}
	return writeAssistBillingExport(ctx, assist.NewRepository(pool), *org, *month, from, to, *format, stdout)
}

// parseExportMonth turns YYYY-MM into the half open window the budgets use: a calendar month in the
// dealer's business zone.
func parseExportMonth(raw string, zone houseday.Zone) (time.Time, time.Time, error) {
	t, err := time.ParseInLocation("2006-01", strings.TrimSpace(raw), zone.Location())
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("assist-billing-export: -month must be YYYY-MM, got %q", raw)
	}
	from, to := assist.MonthWindow(t)
	return from, to, nil
}

// writeAssistBillingExport is the role's body over a store, so a test can run it without a database.
func writeAssistBillingExport(ctx context.Context, store assist.SpendStore, org, month string, from, to time.Time, format string, w io.Writer) error {
	settled, err := store.Settled(ctx, from, to)
	if err != nil {
		return fmt.Errorf("assist-billing-export: read settlements: %w", err)
	}
	exp := assist.BuildBillingExport(org, month, settled)
	if format == "csv" {
		return exp.WriteCSV(w)
	}
	return exp.WriteJSON(w)
}
