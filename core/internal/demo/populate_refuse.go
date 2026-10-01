//go:build demo

package demo

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The populate's own refusal, the G3 shape: the demo populate refuses to
// run against anything but a database that is either freshly migrated and
// seeded (empty org, the migrations' planted users, no payment
// configuration) or already the demo world this populate itself built. Any
// other database, a real one above all, is refused before a single write.
//
// The error messages name counts and tables, never row values: a refusal
// aimed at a real database has no business echoing that database's emails
// into a log.

// refuseForeignWorld runs the three shape checks inside the caller's
// transaction, so the checks and the writes see one database.
func refuseForeignWorld(ctx context.Context, tx pgx.Tx) error {
	if err := refuseForeignOrg(ctx, tx); err != nil {
		return err
	}
	if err := refuseForeignUser(ctx, tx); err != nil {
		return err
	}
	return refuseForeignPaymentConfig(ctx, tx)
}

// refuseForeignOrg: no org other than hh-demo. On a fresh migrate the
// table is empty (the populate provisions the anchor itself); after the
// populate it holds exactly the one row. Anything else is somebody's real
// dealer.
func refuseForeignOrg(ctx context.Context, tx pgx.Tx) error {
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM org WHERE lower(slug::text) <> $1`, demoOrgSlug).Scan(&n); err != nil {
		return fmt.Errorf("demo-populate: org check: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("demo-populate: refusing: the database holds %d org row(s) other than %s, the demo populate runs only on a demo database", n, demoOrgSlug)
	}
	return nil
}

// refuseForeignUser: every users row must be the migrations' planted set
// (the fixed ids, plus 0077's row by its migration-fixed email, the one
// planted id the database minted itself) or one of the two role accounts
// this populate creates. The set is pinned in code, never queried.
func refuseForeignUser(ctx context.Context, tx pgx.Tx) error {
	plantedIDs := make(map[string]bool, len(plantedUserIDs)+2)
	for _, id := range plantedUserIDs {
		plantedIDs[strings.ToLower(id)] = true
	}
	plantedIDs[strings.ToLower(salesUserID)] = true
	plantedIDs[strings.ToLower(managerUserID)] = true
	plantedEmails := make(map[string]bool, len(plantedUserEmails))
	for _, e := range plantedUserEmails {
		plantedEmails[strings.ToLower(e)] = true
	}

	rows, err := tx.Query(ctx, `SELECT id::text, lower(email::text) FROM users`)
	if err != nil {
		return fmt.Errorf("demo-populate: user check: %w", err)
	}
	defer rows.Close()
	foreign := 0
	for rows.Next() {
		var id, email string
		if err := rows.Scan(&id, &email); err != nil {
			return fmt.Errorf("demo-populate: user check: %w", err)
		}
		if plantedIDs[strings.ToLower(id)] || plantedEmails[strings.ToLower(email)] {
			continue
		}
		foreign++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("demo-populate: user check: %w", err)
	}
	if foreign > 0 {
		return fmt.Errorf("demo-populate: refusing: the database holds %d user row(s) outside the migrations' planted set and the demo role accounts, the demo populate runs only on a demo database", foreign)
	}
	return nil
}

// refuseForeignPaymentConfig: no payment configuration but this populate's
// own. On a fresh migrate the table is empty; after the populate it holds
// exactly the invented row. A second configuration, present or appearing
// between runs, is a refusal: the demo box carries exactly one card rail.
func refuseForeignPaymentConfig(ctx context.Context, tx pgx.Tx) error {
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM payment_config WHERE id <> $1::uuid`, demoPaymentConfigID).Scan(&n); err != nil {
		return fmt.Errorf("demo-populate: payment configuration check: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("demo-populate: refusing: the database holds %d payment configuration row(s) the demo populate did not write, the demo box carries exactly one", n)
	}
	return nil
}
