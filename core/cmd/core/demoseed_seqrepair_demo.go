//go:build demo

// demoseed_seqrepair_demo.go, the demo build's repair that gives every seeded row its place in the
// device sync feeds.
//
// WHY. The applier writes rows straight into their tables, so they carry seq NULL (migration 0213
// leaves carried rows unsequenced), and every sync pull reads `seq IS NOT NULL AND seq > cursor`.
// REST showed the demo world and every Field and Pro offline view showed none of it. The Driver's
// day was repaired alone (driverSeqRepairSQL); this file does the rest through the same
// scope_sequences upsert the modules' own writes use.
//
// THE DRAW. One statement per table: the unsequenced rows are numbered in physical order, ONE
// relative, locked write bumps the table's scope counter by the block's size (a concurrent
// allocator's uncommitted draw makes the upsert wait, and the add lands on top of it), and each row
// takes its place in the block read back from what that write returned. next_value holds the last
// number issued, as the live allocator keeps it. The guards hold it to its case: the row is still
// NULL (a second run matches nothing, and with nothing to do the bump is never written, so no
// counter moves), and, on a table that carries an org stamp, the org row exists (the first apply of
// a fresh box runs before the populate provisions it, and the apply after it draws).
//
// THE ORG STAMP. The pulls read `org_id = caller's org AND seq IS NOT NULL`, and the seed wrote
// most of these rows with org_id NULL, so a seq alone would still serve nothing. The same statement
// therefore stamps the demo org on the row it sequences (COALESCE, so an org somebody set is kept).
// The contractor portal's tenant feeds (tenant_id, a customer) are a different fence and are not
// touched here.
//
// SCOPES. A table that a module writes and pulls draws from that module's own counter (the
// override column below), so a seeded row and a live write share one monotonic series per scope
// and a device cursor never skips either. Every other table draws from a counter named by the
// table, the convention customerquote records. A table whose seq is not a scope_sequences number
// (audit_log's own series, the messaging tables' serial, the per purchase order receipt number) is
// listed in demoSeqNotScoped and never touched, and tasks are stamped by repairDemoTaskSync.
//
// THE BRANCHES. Five pulls (drivers, trucks, warranty, will call, purchase orders and the parked
// tickets among them) reach their org through the branch row (`JOIN locations l ... l.org_id`), and the
// migrations plant the demo branches with org_id NULL, so those feeds stay empty whatever a row's
// seq says. The pass stamps the demo org on the unstamped branches first, by the same guard.
//
// ONCE PER WORLD. The pass is a build step of the world, not a standing sweep: staging carries rows
// testers typed after the build, and the applier's rule is that a built box's apply leaves them
// alone. So the pass records demoSeqDoneScope in scope_sequences the moment it has run with the org in
// place, and every later apply skips it whole. A rebuilt world (a fresh database) has no marker and
// is sequenced again; a box built before this pass existed is sequenced once, on its next boot, where
// the only rows still carrying seq NULL are the world's own and the carried ones (a row written
// through the core always has its seq).
//
// THE LIST. demoSeqTables and demoSeqNotScoped together name every table with a seq column; the
// guard test reads the schema and fails when a table is in neither, so a new table cannot be missed
// silently. The statement text is built from these fixed names only, never from input.
package main

import (
	"context"
	"fmt"
	"strings"
)

// seqTable is one table the repair sequences. scope is "" when the table draws from a counter
// named by itself.
type seqTable struct {
	table string
	scope string
}

func (s seqTable) scopeName() string {
	if s.scope != "" {
		return s.scope
	}
	return s.table
}

// demoSeqTables is every seq table the repair sequences, in the order it draws.
var demoSeqTables = []seqTable{
	{"customers", "customers"},
	{"customer_category_overrides", ""},
	{"customer_transactions", ""},
	{"products", "products"},
	{"product_categories", "product_categories"},
	{"vendors", "vendors"},
	{"orders", "orders"},
	{"order_lines", ""},
	{"invoices", "invoices"},
	{"invoice_lines", ""},
	{"payments", "payments"},
	{"payment_allocations", ""},
	{"portal_payment_methods", "payment_methods"},
	{"quotes", "quotes"},
	{"quote_lines", ""},
	{"purchasing_pos", "purchase_orders"},
	{"purchasing_po_lines", ""},
	{"purchase_orders", ""},
	{"po_lines", ""},
	{"po_receipts", ""},
	{"purchasing_receipt_lines", ""},
	{"warranty_claims", "warranty_claims"},
	{"will_call_pickups", "pickups"},
	{"will_call_pickup_events", ""},
	{"dispatch_drivers", "drivers"},
	{"dispatch_trucks", "trucks"},
	{"delivery_runs", "runs"},
	{"delivery_stops", "stops"},
	{"delivery_pod_photos", "pod"},
	{"driver_profile", ""},
	{"load_scan_checkpoints", ""},
	{"task_pick_lines", ""},
	{"task_shortages", "shortages"},
	{"permission_approval_requests", "approvals"},
	{"pos_parked_tickets", "parked_tickets"},
	{"portal_users", "team"},
	{"projects", "projects"},
	{"procurement_projects", "projects"},
	{"procurement_project_lines", ""},
	{"pricing_rules", "price_matrix"},
	{"pricing_list_prices", "price_matrix"},
	{"pricing_promotions", "price_matrix"},
	{"pricing_clearance_items", "price_matrix"},
	{"stock_balances", "inventory_lookup"},
	{"count_sheets", "cycle_counts"},
	{"count_lines", "cycle_counts"},
	{"customer_quote", "customer_quote"},
	{"quote_acceptance", "quote_acceptance"},
	{"plan", "orders"},
	{"plan_line", "scope_items"},
	{"attachment", "attachments"},
	{"dashboard_profile_widgets", ""},
	{"dashboard_user_widgets", ""},
	{"change_order_lines", ""},
	{"pos_sale_tenders", ""},
	{"purchasing_bill_instalments", ""},
	{"staff_role", ""},
	{"staff_role_grant", ""},
	{"msg_acks", ""},
	{"msg_notification_prefs", ""},
}

// demoSeqNotScoped is every seq table the repair leaves alone, with the reason.
var demoSeqNotScoped = map[string]string{
	"audit_log":           "its seq is the audit chain's own series (migration 0078)",
	"msg_channels":        "its seq is a serial the messaging repository advances",
	"msg_messages":        "its seq is a serial the messaging repository advances",
	"purchasing_receipts": "its seq is the receipt number within one purchase order",
	"stock_moves":         "an append-only ledger (trigger trg_stock_moves_no_update refuses any update) that no sync pull reads",
	"tasks":               "stamped into the pick_tasks and weights feeds by repairDemoTaskSync",
}

// seqRepairSQL sequences one table's unsequenced rows. %[1]s is the table, %[2]s the scope, %[3]s the
// extra row guard (empty, or the org row exists), %[4]s the extra column the update sets (empty, or the
// org stamp). ctid addresses the rows because not every seq table has
// an id column, and it is stable inside the apply's one transaction.
const seqRepairSQL = `WITH todo AS (
	SELECT ctid AS row_ctid, row_number() OVER (ORDER BY ctid) AS n
	  FROM %[1]s
	 WHERE seq IS NULL%[3]s
), bump AS (
	INSERT INTO scope_sequences (scope, next_value)
	SELECT '%[2]s', count(*) FROM todo HAVING count(*) > 0
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + EXCLUDED.next_value
	RETURNING next_value
)
UPDATE %[1]s t
   SET seq = (SELECT next_value FROM bump) - (SELECT count(*) FROM todo) + todo.n%[4]s
  FROM todo
 WHERE t.ctid = todo.row_ctid`

// locationOrgRepairSQL stamps the demo org on the branch rows that carry none. The org row must exist
// (a fresh box's first apply runs before the populate provisions it), and a branch that already
// names an org is left alone.
const locationOrgRepairSQL = `UPDATE locations
	   SET org_id = (SELECT id FROM org WHERE slug = $1::citext)
	 WHERE org_id IS NULL AND EXISTS (SELECT 1 FROM org WHERE slug = $1::citext)`

// demoSeqTableSet is the table names demoSeqTables and demoSeqNotScoped name, for the schema check.
func demoSeqTableSet() map[string]bool {
	set := make(map[string]bool, len(demoSeqTables)+len(demoSeqNotScoped))
	for _, s := range demoSeqTables {
		set[s.table] = true
	}
	for n := range demoSeqNotScoped {
		set[n] = true
	}
	return set
}

// repairDemoSeq sequences (and org stamps) every unsequenced row of every listed table that exists and
// carries a seq column, and returns how many rows it sequenced. A listed table that the schema does
// not have is skipped (the list is checked against the schema by the guard test, not by the apply).
func repairDemoSeq(ctx context.Context, tx txExec, rows rowQuery) (int, error) {
	var done bool
	if err := rows.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scope_sequences WHERE scope = $1)`, demoSeqDoneScope).Scan(&done); err != nil {
		return 0, fmt.Errorf("demoseed: reading the seq repair marker: %w", err)
	}
	if done {
		return 0, nil
	}
	have, err := seqColumnTables(ctx, rows)
	if err != nil {
		return 0, err
	}
	total := 0
	tag, err := tx.Exec(ctx, locationOrgRepairSQL, demoOrgSlug)
	if err != nil {
		return 0, fmt.Errorf("demoseed: stamping the demo org on the branches: %w", err)
	}
	total += int(tag.RowsAffected())
	for _, s := range demoSeqTables {
		cols, ok := have[s.table]
		if !ok {
			continue
		}
		guard, stamp, args := "", "", []any{}
		if cols.org {
			guard = " AND EXISTS (SELECT 1 FROM org WHERE slug = $1::citext)"
			stamp = ",\n       org_id = COALESCE(t.org_id, (SELECT id FROM org WHERE slug = $1::citext))"
			args = append(args, demoOrgSlug)
		}
		tag, err := tx.Exec(ctx, fmt.Sprintf(seqRepairSQL, s.table, s.scopeName(), guard, stamp), args...)
		if err != nil {
			return 0, fmt.Errorf("demoseed: sequencing %s into the %s sync scope: %w", s.table, s.scopeName(), err)
		}
		total += int(tag.RowsAffected())
	}
	// The marker is written once the org is in place: the first apply of a fresh box runs before the
	// populate provisions it and has not yet been able to sequence the org stamped tables.
	if _, err := tx.Exec(ctx, `
		INSERT INTO scope_sequences (scope, next_value)
		SELECT $1, 0 WHERE EXISTS (SELECT 1 FROM org WHERE slug = $2::citext)
		ON CONFLICT (scope) DO NOTHING`, demoSeqDoneScope, demoOrgSlug); err != nil {
		return 0, fmt.Errorf("demoseed: recording the seq repair marker: %w", err)
	}
	return total, nil
}

// demoSeqDoneScope is the scope_sequences row that records the seq repair has run on this world. No
// table is written under it; it is a marker row, and the pass skips once it exists.
const demoSeqDoneScope = "demo_seq_repair_done"

// seqCols records what the repair needs to know of one table.
type seqCols struct{ org bool }

// seqColumnTables reads, from the schema, the tables that have a seq column and whether each also
// has an org_id column.
func seqColumnTables(ctx context.Context, rows rowQuery) (map[string]seqCols, error) {
	var names, orgs []string
	err := rows.QueryRow(ctx, `
		SELECT coalesce(array_agg(c.table_name ORDER BY c.table_name), '{}'),
		       coalesce(array_agg(CASE WHEN o.column_name IS NOT NULL THEN 'y' ELSE 'n' END ORDER BY c.table_name), '{}')
		  FROM information_schema.columns c
		  JOIN information_schema.tables t
		    ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
		  LEFT JOIN information_schema.columns o
		    ON o.table_schema = c.table_schema AND o.table_name = c.table_name AND o.column_name = 'org_id'
		 WHERE c.table_schema = current_schema() AND c.column_name = 'seq'`).Scan(&names, &orgs)
	if err != nil {
		return nil, fmt.Errorf("demoseed: reading the tables that carry a seq column: %w", err)
	}
	out := make(map[string]seqCols, len(names))
	for i, n := range names {
		out[n] = seqCols{org: strings.HasPrefix(orgs[i], "y")}
	}
	return out, nil
}
