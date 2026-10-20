package main

// assist_startup.go, the serve startup check of the assistant's database boundary (PR 536 review r2 P2-2).
//
// Migration 0419 enables row security on the tables the assistant's views read. An owner, a superuser or
// a BYPASSRLS role is exempt, so the app keeps its full reads; a serving role that is none of the three
// would read a filtered, possibly empty table. This check turns that into a refusal to start that names the
// table, instead of an app that silently reads nothing. It also requires the serving role to hold the
// function privileges migration 0418 closed to PUBLIC: a superuser, or a member of the database's function
// users role.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
)

// assistPolicedTables are the tables the assist migrations put row security on. Each data slice adds its own.
var assistPolicedTables = []string{"public.orders", "public.order_lines", "public.stock_balances", "public.product_branch_costs", "public.quotes", "public.quote_lines", "public.purchasing_pos", "public.purchasing_po_lines", "public.invoices", "public.payments", "public.payment_allocations"}

type startupRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func assistRequireServingRole(ctx context.Context, db startupRower) error {
	var database, super string
	if err := db.QueryRow(ctx, `SELECT current_database(), current_setting('is_superuser')`).Scan(&database, &super); err != nil {
		return errors.New("serve: cannot read the database session for the assist startup check")
	}
	for _, table := range assistPolicedTables {
		var active bool
		if err := db.QueryRow(ctx, `SELECT row_security_active($1::text::regclass)`, table).Scan(&active); err != nil {
			return fmt.Errorf("serve: cannot check row security on %s", table)
		}
		if active {
			return fmt.Errorf("serve: row security is active for the serving role on %s: it would read a filtered table; run the core as the table owner, a superuser or a BYPASSRLS role", table)
		}
	}
	if super == "on" {
		return nil
	}
	role := readtx.RolesFor(database).FunctionUsers
	var member bool
	if err := db.QueryRow(ctx, `SELECT pg_has_role(current_user, $1, 'MEMBER')`, role).Scan(&member); err != nil {
		return fmt.Errorf("serve: the function users role %s is missing: run core migrate", role)
	}
	if !member {
		return fmt.Errorf("serve: the serving role must be a member of %s (GRANT %s TO <serving role>) or the app loses the functions migration 0418 closed to PUBLIC", role, role)
	}
	return nil
}
