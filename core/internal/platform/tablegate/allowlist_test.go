package tablegate

// allowlist_test.go states today's known cross-module table accesses. Each
// entry names the accessing module, the file (repo relative), the tables it
// touches outside its ownership, and the reason read from that file's own
// source (its header, the statement's own comment, or the statement itself
// where no comment exists). The gate fails ONLY on a cross-module access
// that has no entry here; an entry for a file that stops touching the table
// is harmless, and the census's bite proof removes an entry and expects the
// red.
//
// THE RECURRING PATTERNS, stated once so each entry can lean on them:
//
//   - scope_sequences (0213, owned by identity): the sync scope allocator.
//     Every sync adapter stamps its pushed rows through the one seq stream
//     with the WITH seq AS (...) CTE the sync core's adapters share; the
//     allocator write is part of the frozen 0213 shape, not a module
//     reaching into identity's data.
//   - events_outbox (0013, owned by audit): the transactional outbox IS the
//     event bus. AGENTS.md routes cross-module write side-effects through
//     the event bus only; inserting an event row is that rule working, not
//     a broken fence. audit owns the table; every producer is listed.
//   - org anchor reads on locations (SELECT org_id FROM locations WHERE
//     id = $1, or EXISTS(SELECT 1 FROM locations WHERE org_id = $2)): the
//     tenant fence predicate. branches owns locations; the sync adapters
//     and writers resolve or check the caller's org through it, read only.
//   - users (0004, owned by identity): display name and attribution joins
//     on lists and histories, LEFT JOIN so a deleted user degrades to a
//     blank name rather than dropping the row.
//   - products (0002, owned by product): catalog enrichment on lines and
//     lists (sku, name, uom, weight, images), and the WAC cost row
//     (product_branch_costs) the pricing engine's own readers use.
//
// Every entry below was verified against the named file's SQL at authoring
// time; two files carry headers their SQL no longer honours
// (pricing/pricefile_repository.go and inventory/incoming_repository.go
// both state "no foreign join" and both carry one), and their entries
// state what the SQL actually does.

// allowlistEntry states one file's known cross-module accesses: tables are
// the foreign objects it READS there, writes the foreign objects it WRITES
// there. Matching requires the verb: an UPDATE under an entry that lists
// the table only as a read fails, which is the point, since AGENTS.md
// routes cross-module writes through the event bus and a read-only reason
// must not become a write licence.
type allowlistEntry struct {
	module string
	file   string
	tables []string
	writes []string
	reason string
}

var allowlist = []allowlistEntry{
	{
		// the item's given first entry, verbatim from the source.
		module: "pricing",
		file:   "core/internal/pricing/repository.go",
		tables: []string{"customers", "order_lines", "orders", "pos_sale", "product_branch_costs", "products", "quote_lines", "quotes", "users"},
		// the file's own comment: LowMarginSalesLines reads the COMMITTED
		// sold lines for the principal's daily low-margin report (D-PA-3).
		// This is the ONE deliberately cross-module reporting query in the
		// pricing repo; it joins orders/customers/products (normally read
		// only through the zero-trust catalog seam) because the report IS a
		// live-sales feed; it never WRITES those tables. product_branch_costs
		// is the LEFT JOIN keyed on (product_id, branch_id) that supplies
		// avg_unit_cost_cents, and pos_sale is the COMMITTED facet that
		// catches POS cash sales the orders.status arm never sees. The
		// report covers quotes as well as orders, so the same query reads
		// quotes and quote_lines (the quote's own prices and the clearing
		// stamp on the line's metadata, which an order line reaches through
		// its quote), and LEFT JOINs users for the salesperson's and the
		// clearer's display labels (the users pattern above).
		reason: "D-PA-3 low-margin report over orders and quotes: the one deliberately cross-module reporting query, read only, stated in the function's own comment",
	},
	{
		module: "assist",
		file:   "core/internal/assist/readtx/revoke.go",
		tables: []string{"pg_catalog"},
		// the file's own comment: the provisioning oneshot that closes the extensions' functions to PUBLIC
		// has to find them, which only the system catalogue can say. Read only; run by the operator as the
		// extensions' owner.
		reason: "assist provisioning oneshot reads the system catalogue to find the extensions' functions (no module's data, read only, operator run)",
	},
	{
		module: "assist",
		file:   "core/internal/assist/repository.go",
		tables: []string{"roles", "user_roles", "users"},
		// the file's own header: the admin spend list has to name the people
		// it reports on. Read only, admin gated at the route, and the
		// package's one stated exception.
		reason: "admin spend list names the staff it reports on; read only, admin gated, the file's own stated single exception",
	},
	{
		module: "attachments",
		file:   "core/internal/attachments/store.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		reason: "sync allocator: the 0213 seq stamp on pushed rows (shared pattern above)",
	},
	{
		module: "bankrec",
		file:   "core/internal/bankrec/repository.go",
		tables: []string{},
		writes: []string{"events_outbox", "payments"},
		// the reconciliation marks card payments deposited as the statement
		// lines match (UPDATE payments SET status), and raises its events
		// through the outbox.
		reason: "reconciliation marks matched card payments deposited (the deposit stamp the recon flow exists for) and raises events through the outbox",
	},
	{
		module: "billing",
		file:   "core/internal/billing/load_receivables.go",
		tables: []string{"locations"},
		writes: []string{"scope_sequences"},
		// HH-LOAD-1 LD-11: the open receivables import writer checks the
		// named branch belongs to the loading org (a read of the locations
		// anchor, the same anchor the counter receipt reads) and stamps seq
		// through the 0213 allocator the module's other writers use.
		reason: "the receivables import writer reads the branch's org through the locations anchor and draws the invoices feed seq through the 0213 allocator; its own invoices and customer_transactions rows are billing's",
	},
	{
		module: "billing",
		file:   "core/internal/billing/finance_charge.go",
		tables: []string{"customers", "locations", "payment_allocations", "payment_reapplications", "payments"},
		// Wave 3 FC-1: the monthly finance charge run measures each
		// customer's overdue balance by the aging's own dated rebuild (the
		// payments module's allocations, contra allocations and
		// reapplications, the same reconstruction credit_repository.go's
		// aging runs, read only), checks the run's branch through the
		// locations anchor, and the waiver doors check the customer exists.
		// Its own finance_charge_runs, finance_charge_waivers, invoices and
		// customer_transactions rows are billing's; the charge document
		// itself rides the invoice writer's own statements.
		reason: "the finance charge run's candidates read rebuilds each invoice's balance at the month end instant from the dated payment acts (the aging's own expression, read only), the run's branch resolves through the locations anchor, and the waiver doors' existence check reads the customer row; read only, the writes are billing's own tables and the writer's",
	},
	{
		module: "billing",
		file:   "core/internal/billing/finance_charge_route.go",
		tables: []string{"customers"},
		// Wave 3 FC-1c: the Controller's waiver read lists each waiver with its
		// customer's name, account number and home branch, and holds the list to
		// the caller's branch reach by that home branch (or a document at the
		// branch). The waivers and invoices are billing's own.
		reason: "the finance charge waiver list reads the waived customer's name, account number and home branch for the screen and the branch reach; read only, the waiver rows and invoices are billing's own tables",
	},
	{
		module: "billing",
		file:   "core/internal/billing/ar_landing_store.go",
		tables: []string{"credit_holds", "customer_contacts", "customers"},
		// the file's own header: every read here is a projection for the
		// landing and its reminders; it never edits money.
		reason: "landing and reminder projections read the customer's name and contacts and the hold state; read only, stated in the file header",
	},
	{
		module: "billing",
		file:   "core/internal/billing/credit_apply.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		// FW1-12b: the apply and the reversal move the invoice's balance
		// through creditFeedSeqCTE, the invoices feed counter every balance
		// move draws beside its write, so the payer's device sees the
		// change on its invoices feed.
		reason: "the credit application door's balance moves draw the invoices feed seq through the 0213 allocator (creditFeedSeqCTE, the settle's own posture); its own invoices and customer_transactions rows are billing's",
	},
	{
		module: "billing",
		file:   "core/internal/billing/write_off.go",
		tables: []string{},
		writes: []string{"events_outbox", "scope_sequences"},
		// W3 item 0.10: the write off and its reversal settle the invoice
		// through creditFeedSeqCTE and emit their GL events beside the
		// settlement, the deposit cancel's discipline; its own invoices and
		// customer_transactions rows are billing's, and ar_credit_policy's
		// limit is read inside the act's own transaction.
		reason: "the write off door's balance moves draw the invoices feed seq through the 0213 allocator and its GL events ride the outbox (the deposit cancel's discipline); its own invoices, customer_transactions and ar_credit_policy rows are billing's",
	},
	{
		module: "billing",
		file:   "core/internal/billing/credit_memo_repost.go",
		tables: []string{"gl_journals"},
		// DIB-429: the sweep finds credit-memo journals whose inline post
		// failed, by the derived idempotency key the memo carries.
		reason: "DIB-429 self-healing sweep reads gl_journals by the derived idempotency key to re-post failed credit-memo journals; read only",
	},
	{
		module: "billing",
		file:   "core/internal/billing/credit_repository.go",
		tables: []string{"credit_holds", "customers", "orders", "payment_allocations", "payment_reapplications", "payments"},
		writes: []string{"credit_holds"},
		// the file's own header, restating the module note: billing WRITES
		// credit_holds, deliberate and documented in migration 0104, the
		// alternative was a second holds table duplicating the one ACTIVE
		// hold invariant. customers and orders feed the credit gates. The
		// as-of aging (FW1-06b) rebuilds each document's balance at the
		// as-of instant inside the one aging query, from the dated acts
		// that moved it: the payments module's allocations, contra
		// allocations and reapplications.
		reason: "AR credit gates place and read holds (documented in migration 0104, stated in the file header), read the customer and order facts the gates decide on, and the as-of aging rebuild reads the dated payment acts per document in the one aging query; read only",
	},
	{
		module: "billing",
		file:   "core/internal/billing/job_exposure.go",
		tables: []string{"orders"},
		// the file's own header: the customer's exposure is its running AR balance plus its open
		// orders, and a job's exposure is its invoices plus its open orders. An order not yet
		// invoiced is committed money no invoice carries, so the gates read it here; the read is
		// the same one credit_repository.go already makes for the credit gates, and the file
		// writes nothing.
		reason: "the one exposure reader the credit gates share counts the open (uninvoiced) orders a customer and a job have committed, stated in the file header; read only",
	},
	{
		module: "billing",
		file:   "core/internal/billing/customer_ar_read.go",
		tables: []string{"payments"},
		// the file's own header: the arithmetic is not re-implemented here;
		// the consumer read picks its answer out of the same rows the AR
		// console reads.
		reason: "consumer-facing AR read reuses the payments stream rather than re-implementing the arithmetic, stated in the file header",
	},
	{
		module: "billing",
		file:   "core/internal/billing/holdback_release.go",
		tables: []string{"customers"},
		// The due to release list names the customer on each row in the same
		// query (no read per row), so the confirm can say who the money is held
		// for: the customer's display name, read only.
		reason: "the holdback due to release read joins customers for the display name of each row in the one query; read only",
	},
	{
		module: "billing",
		file:   "core/internal/billing/gl_seam.go",
		tables: []string{"payment_allocations", "payment_reapplications", "payments"},
		// the file's own doc: ARBalanceCents is the 1100 tie-out's subledger
		// figure. The as-of arm (FW1-06b) rebuilds each invoice's balance at
		// the as-of instant inside the one query, from the same dated acts
		// the aging's rebuild reads, so the tie-out at a past date compares
		// what was owed then with the GL at that date.
		reason: "the 1100 tie-out's as-of arm rebuilds each invoice's balance from the dated payment acts in the one query, the same reconstruction the aging runs, so the two tie by construction; read only",
	},
	{
		module: "billing",
		file:   "core/internal/billing/invoice_writer.go",
		tables: []string{"locations", "orders"},
		writes: []string{"events_outbox", "scope_sequences"},
		// DIB-422: the order to invoice writer outside the POS ring. It
		// stamps the invoice row with its org through the locations anchor,
		// draws the invoice stream from the 0213 allocator, and raises its
		// events through the outbox. The orders read is lockOrder taking the
		// order's row lock, the one lock every writer that bills an order
		// takes first, stated in lockOrder's own comment.
		reason: "the order to invoice writer stamps org (locations anchor) and seq (0213 allocator) on the document it raises and publishes events through the outbox; the orders read is lockOrder's row lock so a group invoice and a counter invoice of one order queue behind each other, new in HH-RD2 (the Quote Desk cycle), a same transaction read, granted to move behind the owning module's transaction taking seam (the till hold's WithCallerTx shape)",
	},
	{
		module: "billing",
		file:   "core/internal/billing/register_detail_repository.go",
		tables: []string{"credit_holds", "customer_contacts", "customers", "delivery_pod_photos", "delivery_stops", "orders", "payment_allocations", "payments", "product_images", "products", "users"},
		// the file's own header: ONE document read, everything the right
		// hand panel shows, in one round trip. Read only.
		reason: "DIB-510 register detail: one document read joining the names, payments, delivery proof and product images the invoice panel shows; read only, stated in the file header",
	},
	{
		module: "billing",
		file:   "core/internal/billing/register_repository.go",
		tables: []string{"credit_holds", "customers", "orders", "users"},
		reason: "the register list joins the customer name, order reference, salesperson name and active hold state each invoice row shows (registerSelect, the file's stated single shaping of the rows), read only",
	},
	{
		module: "billing",
		file:   "core/internal/billing/statement_repository.go",
		tables: []string{"customers", "locations"},
		writes: []string{"print_queue"},
		reason: "statements carry the customer and branch names they are addressed by, and print through the shared print_queue substrate (0088)",
	},
	{
		module: "billing",
		file:   "core/internal/billing/syncscope_invoices.go",
		tables: []string{"orders", "locations"},
		reason: "the invoice sync scope holds the order the document bills to the caller's org for the sync fence, through the order's branch and the org anchor read on locations, read only, locations.id and locations.org_id and nothing else (UX-95a: no writer stamps orders.org_id; row 2954)",
	},
	{
		module: "branches",
		file:   "core/internal/branches/repository.go",
		tables: []string{"system_settings"},
		reason: "default branch resolution reads system_settings' default_branch_id, the one setting branches itself seeds and consumes",
	},
	{
		module: "customer",
		file:   "core/internal/customer/repository.go",
		tables: []string{"customer_transactions"},
		reason: "account lookup and search read the account's latest AR balance (balance_after) beside the account rows, read only",
	},
	{
		module: "customer",
		file:   "core/internal/customer/account_lookup.go",
		tables: []string{"customer_transactions"},
		// the file's own header: resolve ONE account from what a person
		// typed; the lookup carries the account's live AR balance beside its
		// identity, read through the file's scan closure.
		reason: "the single account lookup reads the account's latest AR balance beside the account row, read only",
	},
	{
		module: "customerquote",
		file:   "core/internal/customerquote/store.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		reason: "sync allocator: the 0213 seq stamp on pushed rows (shared pattern above)",
	},
	{
		module: "dashboard",
		file:   "core/internal/dashboard/quickaction_repository.go",
		tables: []string{"quick_action_profile_visibility", "quick_action_settings"},
		writes: []string{"quick_action_profile_visibility", "quick_action_settings"},
		// the dashboard is the quick action surface; 0151 built the substrate
		// it configures. Its own reads and writes.
		reason: "the quick action surface reads and writes its visibility config in the 0151 substrate; the substrate was migrated under the settings topic but the dashboard is its only writer",
	},
	{
		module: "dashboard",
		file:   "core/internal/dashboard/repository.go",
		tables: []string{"dealer_hotkeys", "platform_hotkeys"},
		reason: "hotkey aggregation reads the dealer's own hotkeys over the platform roster (the 0191 reset to the platform roster flow), read only",
	},
	{
		module: "dashboard",
		file:   "core/internal/dashboard/viz_repository.go",
		tables: []string{"surface_role_items"},
		// the file's own header: which metrics a job shape may compose from
		// is presentation curation, so it lives where every other
		// presentation curation lives: surface_role_items.
		reason: "metric curation reads the 0172 presentation substrate (surface_role_items), stated in the file header; read only",
	},
	{
		module: "demo",
		file:   "core/internal/demo/boot/database.go",
		tables: []string{"information_schema", "org", "payment_config", "users"},
		// the boot guard checks the database is a demo database in shape
		// before anything runs: one org, no users outside the planted set,
		// the demo payment configuration.
		reason: "demo boot guard reads org, users and payment_config to verify the demo shape before serving; read only, stated in the populate header",
	},
	{
		module: "demo",
		file:   "core/internal/demo/populate.go",
		tables: []string{"locations", "roles", "user_branch_grants", "user_roles"},
		writes: []string{"locations", "payment_config", "system_settings", "user_branch_grants", "user_roles", "users"},
		// the file's own header: the one-shot demo populate turns a freshly
		// migrated database into the demo world; every insert carries an
		// explicit deterministic id and ends in ON CONFLICT DO NOTHING. It
		// is demo-tagged and ships in no production build.
		reason: "demo populate seeds the demo world across the org's substrate (role accounts, grants, payment configuration, planted names); demo-tagged, idempotent, refused unless the database is already demo-shaped",
	},
	{
		module: "demo",
		file:   "core/internal/demo/populate_refuse.go",
		tables: []string{"org", "payment_config", "users"},
		reason: "demo populate's refusal checks read org, users and payment_config to decide whether the database is already a demo box; read only",
	},
	{
		module: "dispatch",
		file:   "core/internal/dispatch/repository.go",
		tables: []string{"customers", "locations", "order_lines", "orders"},
		writes: []string{"scope_sequences"},
		reason: "manifests and deliveries read the order, its lines and the customer and branch names the manifest carries; the 0213 seq stamp marks its rows for device pull",
	},
	{
		module: "dispatch",
		file:   "core/internal/dispatch/repository_freightraise.go",
		tables: []string{"customers", "orders"},
		reason: "the 0195 freight raise reads the order and customer facts the raise and its approval stamp cite; read only",
	},
	{
		module: "dispatch",
		file:   "core/internal/dispatch/repository_loadboard.go",
		tables: []string{"customers", "orders"},
		writes: []string{"scope_sequences"},
		reason: "the load board reads the order and customer names the board rows show; the 0213 seq stamp marks its rows for device pull",
	},
	{
		module: "dispatch",
		file:   "core/internal/dispatch/repository_w3.go",
		tables: []string{"customers", "locations", "orders"},
		writes: []string{"scope_sequences"},
		reason: "the Wave 3 reads (pick feed, MTO, reschedule, scan trail) read the order and customer facts the stops carry; the 0213 seq stamp marks its rows; the org held ticket and pending feed reads hold the stop's or order's branch to the caller's org through the org anchor read on locations, read only, locations.id and locations.org_id and nothing else (UX-81 rounds 5 and 6: no writer stamps orders.org_id; approved, decision record row 2879)",
	},
	{
		module: "dispatch",
		file:   "core/internal/dispatch/syncscope.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		reason: "sync allocator: the 0213 seq stamp on pushed rows (shared pattern above)",
	},
	{
		module: "dispatch",
		file:   "core/internal/dispatch/syncscope_board.go",
		tables: []string{"locations"},
		reason: "the board scope's org fence resolves through the locations anchor; read only",
	},
	{
		module: "dispatch",
		file:   "core/internal/dispatch/syncscope_pickups.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		reason: "sync allocator: the 0213 seq stamp on pushed rows (shared pattern above)",
	},
	{
		module: "dispatch",
		file:   "core/internal/dispatch/syncscope_readcache.go",
		tables: []string{"locations"},
		// the file's own header: the drivers and trucks pull-only stores; the
		// org fence is the branch, and branch_id resolves through locations.
		reason: "the drivers and trucks sync scopes resolve their branch org fence through the locations anchor, read only, stated in the file header",
	},
	{
		module: "dispatch",
		file:   "core/internal/dispatch/syncscope_runs.go",
		tables: []string{"locations"},
		writes: []string{"scope_sequences"},
		reason: "org fence through the locations anchor (read only) plus the 0213 seq stamp",
	},
	{
		module: "finstmt",
		file:   "core/internal/finstmt/dib510_register_mount.go",
		tables: []string{"locations"},
		reason: "the receivables register reads the branch name for the register header; read only",
	},
	{
		module: "gl",
		file:   "core/internal/gl/repository.go",
		tables: []string{"users", "v_gl_account_balances", "v_gl_detail"},
		// the file's own header: it reads the 0034 balance and detail views
		// (the views the finstmt migration built over the GL's own tables);
		// users names approvers on the assignment list.
		reason: "tie-out and balance reads go through the 0034 views over the GL's own tables (stated in the file header) and approver lists join users for display names",
	},
	{
		module: "landedcost",
		file:   "core/internal/landedcost/ap_payable.go",
		tables: []string{"vendors"},
		// the statement's own comment: the AP aging must label the payable
		// with the payee's name, so the open payable rows LEFT JOIN the
		// vendors table the create route's payee_vendor_id names; read only.
		reason: "the open payable read joins vendors for the payee vendor's display name (0458 payee_vendor_id), the label the AP aging shows; a row that names no payee degrades to its PO reference",
	},
	{
		module: "identity",
		file:   "core/internal/identity/populate.go",
		tables: []string{"dispatch_drivers", "portal_users"},
		writes: []string{"portal_users"},
		// population provisions the portal accounts (the backfill stamps
		// tenant_id, the sweep lists unlinked portal users) and reads the
		// driver profiles it provisions beside them.
		reason: "identity population provisions and backfills portal accounts and reads the driver profiles it stamps; the provisioning act the 0019/0215 substrate expects",
	},
	{
		module: "identity",
		file:   "core/internal/identity/portal.go",
		tables: []string{"portal_users"},
		reason: "the portal credential leg reads the portal account's role and status to answer who the caller is on the portal surface; read only",
	},
	{
		module: "identity",
		file:   "core/internal/identity/staffcontext/syncscope_approvals.go",
		tables: []string{"locations"},
		reason: "the approval scope's org fence checks branch reachability through the locations anchor; read only",
	},
	{
		module: "invcount",
		file:   "core/internal/invcount/repository.go",
		tables: []string{"products"},
		writes: []string{"scope_sequences"},
		reason: "count sheet lines join the product sku and name the counter sees on the sheet; the 0213 seq stamp marks its rows",
	},
	{
		module: "invcount",
		file:   "core/internal/invcount/syncscope.go",
		tables: []string{"locations"},
		writes: []string{"scope_sequences"},
		reason: "org fence through the locations anchor (read only) plus the 0213 seq stamp",
	},
	{
		module: "inventory",
		file:   "core/internal/inventory/availability.go",
		tables: []string{"products"},
		// the file's own header: reads the product discontinued flag plus the
		// stock read model; a courtesy flag, not an oversell guard.
		reason: "availability check reads the product discontinued flag, stated in the file header; read only",
	},
	{
		module: "inventory",
		file:   "core/internal/inventory/browse.go",
		tables: []string{"locations", "products"},
		reason: "the browse surface is the stock-aware product list; it reads the catalog columns and promoted UDF facets it filters on, and holds every stock and cost column to the caller's org through the org anchor read on locations (UX-88)",
	},
	{
		module: "inventory",
		file:   "core/internal/inventory/incoming_repository.go",
		tables: []string{"locations", "products", "vendors"},
		// the header states NO JOIN THIS MODULE DOES NOT OWN; the SQL says
		// otherwise and the gate records reality: the in-transit lines join
		// locations for the branch names, products for the sku, and vendors
		// for the preferred vendor label.
		reason: "in-transit detail joins branch names, product skus and the preferred vendor label (the file header's no foreign join note is stale; the SQL carries the joins), read only",
	},
	{
		module: "inventory",
		file:   "core/internal/inventory/replenish_repository.go",
		tables: []string{"locations", "products", "vendors"},
		reason: "the DIB-72 worklist joins managed products, branch names and the preferred vendor the suggestion names, read only",
	},
	{
		module: "inventory",
		file:   "core/internal/inventory/repository.go",
		tables: []string{"order_lines", "orders", "po_lines", "products", "purchase_orders"},
		writes: []string{"events_outbox", "po_lines", "po_receipts", "purchase_orders"},
		// the file's own header: it reads and writes the 0011 receiving
		// documents (purchase_orders, po_lines, po_receipts) beside the stock
		// ledger; orders and order_lines feed the reserved computation; the
		// outbox carries its events.
		reason: "receiving writes the goods-received side of the 0011 purchasing documents (stated in the file header) and reserved-quantity reads look at open orders and their lines",
	},
	{
		module: "inventory",
		file:   "core/internal/inventory/stock_lookup.go",
		tables: []string{"locations", "products"},
		reason: "the caller-scoped stock lookup reads the reachable branches and the product row, read only",
	},
	{
		module: "inventory",
		file:   "core/internal/inventory/transfer_eta_repository.go",
		tables: []string{"locations"},
		reason: "the DIB-472 transit-day read joins the from and to branch rows to answer the eta question, read only",
	},
	{
		module: "inventory",
		file:   "core/internal/inventory/transfer_repository.go",
		tables: []string{"products"},
		reason: "transfer lines resolve the product's base uom and sku beside the move, read only",
	},
	{
		module: "inventory",
		file:   "core/internal/inventory/visibility.go",
		tables: []string{"locations"},
		reason: "the cross-branch visibility read names the branches it answers for, read only",
	},
	{
		module: "messaging",
		file:   "core/internal/messaging/payment_rules_repo.go",
		tables: []string{"customers", "invoices", "locations", "orders", "payment_allocations", "payments", "users"},
		reason: "payment notification rules evaluate the AR facts (invoice, payment, allocation) they notify about and join the customer, branch and staff names the message names, read only",
	},
	{
		module: "messaging",
		file:   "core/internal/messaging/repassign_repo.go",
		tables: []string{"customers", "orders", "users"},
		reason: "the rep assignment queue joins the customer, order and staff facts each conversation row carries, read only",
	},
	{
		module: "messagingprovider",
		file:   "core/internal/messagingprovider/collections.go",
		tables: []string{"ar_notice_log"},
		writes: []string{"ar_notice_log"},
		// the file calls ar_notice_log the collections ledger: claimBucket
		// INSERTs the (invoice, bucket) row that makes the claim atomic,
		// the sweep links the queued notice to it and backfills the buckets
		// an invoice sailed past unnotified, and the cooldown read excludes
		// the just-claimed row so the claim itself never suppresses the
		// job's own notice. The 0110 topic owns the table (billing); the
		// sweep's touches are the fence crossing this entry states.
		reason: "the collections sweep keeps its notice ledger (claim, link, backfill, cooldown) in ar_notice_log, read and written",
	},
	{
		module: "messaging",
		file:   "core/internal/messaging/repository.go",
		tables: []string{"locations", "roles", "user_branch_grants", "user_roles", "users"},
		reason: "branch team channels resolve the staff side: branch membership, grants and role rows for the recipients the channels name, read only",
	},
	{
		module: "messagingprovider",
		file:   "core/internal/messagingprovider/collections_aging.go",
		tables: []string{"customers", "invoices"},
		reason: "collections campaigns read the AR aging and the customer facts the campaign messages name, read only",
	},
	{
		module: "notify",
		file:   "core/internal/notify/requeue.go",
		tables: []string{"events_outbox"},
		writes: []string{"events_outbox"},
		// the file's own header: the scaffold-owned dead-row replay seam on
		// the outbox store; gl's DeadRequeuer satisfied by notify. FW1-09b
		// adds a read beside the reset: the close pre flight counts the
		// month's PENDING and DEAD rows through UnpostedForWindow on the
		// same store, the outbox read staying in its keeper module.
		reason: "the outbox dead-row replay resets DEAD rows to PENDING for redelivery, and the close pre flight counts the month's waiting or dead rows, both stated in the file header",
	},
	{
		module: "notify",
		file:   "core/internal/notify/store.go",
		tables: []string{"events_outbox"},
		writes: []string{"events_outbox"},
		// the outbox store implementation itself lives here: PgOutboxStore is
		// the write and read side of the 0013 bus every producer inserts to.
		reason: "the transactional outbox store's own implementation (PgOutboxStore); the bus is the table and this module is its keeper",
	},
	{
		module: "order",
		file:   "core/internal/order/repository.go",
		tables: []string{"locations"},
		reason: "the org held brief read (OrderBriefInOrg) holds an order's branch to the caller's org through the org anchor read on locations, read only, locations.id and locations.org_id and nothing else (UX-81 round 6: no writer stamps orders.org_id; approved, decision record row 2879)",
	},
	{
		module: "order",
		file:   "core/internal/order/duplicate.go",
		tables: []string{"locations"},
		reason: "the duplicate draft insert stamps orders.org_id with the branch's org through the org anchor read on locations, read only, locations.id and locations.org_id and nothing else (UX-95: every orders writer stamps the org)",
	},
	{
		module: "order",
		file:   "core/internal/order/reorder.go",
		tables: []string{"locations"},
		reason: "the reorder draft insert stamps orders.org_id with the branch's org through the org anchor read on locations, read only, locations.id and locations.org_id and nothing else (UX-95: every orders writer stamps the org)",
	},
	{
		module: "order",
		file:   "core/internal/order/activity_repository.go",
		tables: []string{"invoices", "payment_allocations", "payments", "users"},
		// the file's own header: a READ ONLY presentation projection, never a
		// money decision, following the till's attributed history and this
		// repository's own shipment-plan precedents.
		reason: "activity trail merges the immediate documents projection (invoices, payments, allocations, staff names); read only, stated in the file header",
	},
	{
		module: "order",
		file:   "core/internal/order/change_repository.go",
		tables: []string{"products"},
		reason: "change order lines join the product sku and name the change document shows, read only",
	},
	{
		module: "order",
		file:   "core/internal/order/history.go",
		tables: []string{"users"},
		// the file's own header: actor display names come from a LEFT JOIN to
		// users on the audit actor_id.
		reason: "status timeline joins users for actor display names, stated in the file header; read only",
	},
	{
		module: "order",
		file:   "core/internal/order/invoice_source.go",
		tables: []string{"customers", "invoices"},
		// the invoice source seam returns FACTS about the order. The
		// invoices read is OrderHasCounterInvoice: the fulfilment road asks
		// whether the counter already raised the order's invoice (the header
		// metadata marker the finalized event carries) so it completes the
		// move instead of raising a second document, per that function's own
		// comment.
		reason: "the invoice source seam returns FACTS about the order (the customer's terms beside them); read only, no permission decided here; the invoices read is OrderHasCounterInvoice, new in HH-RD2 (the Quote Desk cycle), a pool read made after the status move commits, by the outbox observer whose claim connection is its own pool (PR 163), not a same transaction read",
	},
	{
		module: "order",
		file:   "core/internal/order/reservation_repository.go",
		tables: []string{"products", "stock_reservations"},
		// the file's own header: this module writes NO stock table; the
		// injected StockReserver (inventory's code) writes inventory's tables.
		// The reads project the holds that already exist.
		reason: "reservations read the existing holds and the product rows the hold projection shows; stock WRITES go through the injected StockReserver, stated in the file header",
	},
	{
		module: "order",
		file:   "core/internal/order/shipgroup_repository.go",
		tables: []string{"invoices"},
		// the file's own header: the shipment plan read LEFT JOINs invoices
		// for the group's invoice number.
		reason: "shipment plan reads join invoice headers for the group's invoice number, stated in the file header; read only",
	},
	{
		module: "order",
		file:   "core/internal/order/soft_hold_expiry.go",
		tables: []string{"products", "stock_reservations"},
		reason: "the soft hold expiry projection reads the expired holds and the product facts the silence-filling order note names, read only",
	},
	{
		module: "order",
		file:   "core/internal/order/summary.go",
		tables: []string{"customers", "locations"},
		// the file's own header: it resolves the number to a customer name
		// and a branch name because those are the two facts that make the
		// answer recognisable as the right order.
		reason: "the order by number read resolves the customer and branch names, stated in the file header; read only",
	},
	{
		module: "payments",
		file:   "core/internal/payments/counter_receipt.go",
		tables: []string{"invoices", "locations"},
		writes: []string{"scope_sequences"},
		reason: "the counter receipt reads the invoice balance it applies against, stamps org through the locations anchor and seq through the 0213 allocator",
	},
	{
		module: "payments",
		file:   "core/internal/payments/discount.go",
		tables: []string{"invoices"},
		reason: "the early payment discount cap reads the invoice's discountable base on the settle's own transaction (TM-2): the same invoice row the settle already locks; read only",
	},
	{
		module: "payments",
		file:   "core/internal/payments/discount_giveback.go",
		tables: []string{},
		writes: []string{"customer_transactions"},
		reason: "the one helper every undo door (reversal, void, the till's NSF bounce) uses to write the discount's contra row into the AR subledger projection, as reversal.go and void.go already write their rows (TM-2)",
	},
	{
		module: "payments",
		file:   "core/internal/payments/payment_lookup.go",
		tables: []string{"customers", "locations"},
		reason: "find me that payment resolves the customer and branch names the three lookup shapes answer with, read only",
	},
	{
		module: "payments",
		file:   "core/internal/payments/repository.go",
		tables: []string{"customers", "invoices", "users"},
		writes: []string{"customer_transactions", "events_outbox", "invoices", "scope_sequences"},
		// the payments module maintains the AR subledger projection of the
		// payment stream (customer_transactions), applies payments to invoice
		// balances, and stamps org and seq on its rows.
		reason: "payment writes maintain the AR subledger projection and the invoice balances they apply to; the saved method cache is the module's own table; org and seq stamped",
	},
	{
		module: "payments",
		file:   "core/internal/payments/reversal.go",
		tables: []string{"customers", "invoices"},
		writes: []string{"customer_transactions", "events_outbox", "invoices", "scope_sequences"},
		// the file's own header: an undo posts a REVERSING entry; the AR
		// subledger and the general ledger are append-only projections of the
		// payment stream.
		reason: "reversals post the reversing rows into the AR subledger projection and un-apply the invoice balances, per the file header's pre-ratified ruling; events through the outbox, org and seq stamped",
	},
	{
		module: "payments",
		file:   "core/internal/payments/void.go",
		tables: []string{"invoices"},
		writes: []string{"customer_transactions", "events_outbox", "invoices", "scope_sequences"},
		// the file's own header: the true void of a mis-keyed desk receipt
		// (FW1-06b) undoes the receipt as never taken in ONE transaction,
		// the reversal's sibling with no 2310 hold: the allocation is
		// deleted, the invoice balances re-open, the subledger gets the
		// mirror rows, and payment.voided rides the outbox.
		reason: "the void of a mis-keyed receipt re-opens the invoice balances its allocation settled, mirrors the AR subledger rows, emits payment.voided through the outbox and stamps seq, the reversal's sibling act with no 2310 hold",
	},
	{
		module: "product",
		file:   "core/internal/product/loadwriter.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		reason: "the load doors' category writer stamps its rows through the 0213 seq stream (HH-LOAD-1 LD-4); the allocator write is part of the frozen 0213 shape",
	},
	{
		module: "purchasing",
		file:   "core/internal/purchasing/vendorload.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		reason: "the load doors' supplier writer stamps its vendor rows through the 0213 seq stream (HH-LOAD-1 LD-4); the allocator write is part of the frozen 0213 shape",
	},
	{
		module: "customer",
		file:   "core/internal/customer/loadwriter.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		reason: "the load doors' customer writer stamps its customer rows through the 0213 seq stream (HH-LOAD-1 LD-6); the allocator write is part of the frozen 0213 shape",
	},
	{
		module: "portal",
		file:   "core/internal/portal/jobload.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		reason: "the load doors' job writer stamps its project rows through the 0213 seq stream (HH-LOAD-1 LD-6); the allocator write is part of the frozen 0213 shape",
	},
	{
		module: "product",
		file:   "core/internal/product/productload.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		reason: "the load doors' product master writer stamps its rows through the 0213 seq stream (HH-LOAD-1 LD-5); the allocator write is part of the frozen 0213 shape",
	},
	{
		module: "planflow",
		file:   "core/internal/planflow/syncscope.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		reason: "sync allocator: the 0213 seq stamp on pushed rows (shared pattern above)",
	},
	{
		module: "platform",
		file:   "core/internal/platform/authz/orgresolver.go",
		tables: []string{"org"},
		// the authz middleware resolves the org anchor by slug for tenant
		// routing before any module handler runs. The platform is scanned as
		// a module with an empty ownership scope (see knownDirs), so its one
		// production read is stated here like any other.
		reason: "authz org anchor resolution for tenant routing; read only, the platform substrate's only production table read",
	},
	{
		module: "portal",
		file:   "core/internal/portal/addresses_repository.go",
		tables: []string{"customers", "delivery_stops", "orders"},
		// the file's own header: the sites read joins the SAME tables the
		// project store already owns (projects, orders) plus two columns; arm
		// 3 is scoped through orders.customer_id, which is the reason a
		// dispatch table can be read here without exposing anybody else's
		// geography.
		reason: "the addresses read joins the account's billing address, its projects and its delivered stops, scoped through the tenant's own orders, stated in the file header; read only",
	},
	{
		module: "portal",
		file:   "core/internal/portal/projects.go",
		tables: []string{"invoices", "manifest_lines", "manifests", "orders", "quotes"},
		reason: "the project work timeline walks the documents the project produced (quote, order, manifest, invoice), read only",
	},
	{
		module: "portal",
		file:   "core/internal/portal/repository.go",
		tables: []string{"customers"},
		writes: []string{"events_outbox", "scope_sequences"},
		// the scope_sequences write is the teamSeqCTE allocator, run through
		// the file's own exec and guardedAdminUpdate wrappers.
		reason: "the portal session resolves the customer account behind the caller, raises its events through the outbox, and stamps its rows through the 0213 scope allocator",
	},
	{
		module: "portal",
		file:   "core/internal/portal/syncscope.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		reason: "sync allocator: the 0213 seq stamp on pushed rows (shared pattern above)",
	},
	{
		module: "pos",
		file:   "core/internal/pos/repository.go",
		tables: []string{"customer_transactions", "customers", "invoices", "locations", "order_lines", "orders", "payments", "product_reorder_points", "product_uoms", "products", "staff_role", "staff_role_grant", "stock_balances", "user_roles", "users"},
		writes: []string{"customer_transactions", "events_outbox", "invoice_lines", "invoices", "order_lines", "orders", "payment_allocations", "scope_sequences"},
		// the POS sale ring: one counter sale writes the order and its lines,
		// raises the invoice and its lines, the AR transaction and the tender
		// allocation in one transaction (the file's header isolates these
		// behind posStore so they can be re-homed; the payments INSERT is
		// till.TakeTender's, the allocation of that payment is the ring's).
		// The reads are the counter's catalogue, stock and supervisor facts.
		reason: "the POS ring writes the sale's documents (order, lines, invoice, AR transaction, tender allocation) in one transaction and reads the catalogue, stock, tax snapshot and supervisor facts the counter acts on; the counter discount reads the staff role grants and the role catalogue to judge who may approve a discount at the branch (ListDiscountStaff) and the role label a committed discount's approver held (ReadSaleDiscounts), read only, the dealer's list of who may approve staying in roleconfig",
	},
	{
		module: "pos",
		file:   "core/internal/pos/repository_quickpicks.go",
		tables: []string{"order_lines", "product_categories", "product_images", "products", "stock_balances"},
		// the file's own header: both reads stay inside the POS read boundary
		// the same way the rev-8 add-on reads do; they compute nothing about
		// price. The category representatives read stock_balances only to know
		// whether a category has stock available (on hand less reserved), the
		// figure the POS glance reads, so the grid can show every stocked category.
		reason: "quick pick tiles read the catalogue and committed lines for display only, stated in the file header; the category representatives also read whether stock is available in a category, read only",
	},
	{
		module: "pos",
		file:   "core/internal/pos/repository_rev8.go",
		tables: []string{"order_lines", "pricing_list_prices", "product_branch_costs", "product_images", "products"},
		// the file's own header: the SAME isolated M2-debt cross-read posture,
		// display-only reads of catalog and costing data; pos never writes
		// those tables.
		reason: "rev 8 store reads (weight, per-branch cost, list price, images, add-on pairings, top applications): display-only M2-debt cross-reads, stated in the file header; read only",
	},
	{
		module: "pos",
		file:   "core/internal/pos/repository_w7.go",
		tables: []string{"customer_transactions", "customers", "invoices", "order_lines", "orders", "payments", "products"},
		writes: []string{"invoices", "scope_sequences"},
		// the file's own header: the W7 counter-v2 persistence over the 0043
		// tables plus the invoice-credit write, which stays in the M2-debt
		// family the repository header isolates behind posStore.
		reason: "counter v2 returns credit the customer's invoice and AR balance and read the sale's facts; the invoice credit write is the isolated M2-debt family, stated in the file header",
	},
	{
		module: "pos",
		file:   "core/internal/pos/return_requests.go",
		tables: []string{"customers", "order_lines", "orders"},
		// the file's own header: the portal BFF submits and reads requests
		// through the service seam here, tenant-scoped by customer_id; a
		// PENDING request moves NO money and NO stock.
		reason: "return requests read the customer's own order and lines to validate the request before the staff approval executes the existing W7 rail; the request tables are this module's own",
	},
	{
		module: "pos",
		file:   "core/internal/pos/syncscope.go",
		tables: []string{"locations"},
		// the file's own header: the org scope rides the branch; the branch
		// resolves to the org through locations, the join stays inside the
		// module's own read surface, locations being the shared branch table
		// every module reads for scoping. The statements live in the
		// readcache.Query fields, built from the file's shared consts.
		reason: "the parked tickets sync scope resolves its branch org fence through the locations anchor, read only, stated in the file header",
	},
	{
		module: "pos",
		file:   "core/internal/pos/sale_lookup.go",
		tables: []string{"customers", "invoices", "order_lines", "orders"},
		reason: "find me that sale walks the committed sale's own order, lines, invoice and customer facts, read only",
	},
	{
		module: "pricing",
		file:   "core/internal/pricing/batch_repository.go",
		tables: []string{"products", "vendors"},
		reason: "price batch profiles list their vendor and the product skus the batch touches, the same 0039 joins the price file reader carries, read only",
	},
	{
		module: "pricing",
		file:   "core/internal/pricing/pricefile_repository.go",
		tables: []string{"users", "vendors"},
		// the header states the products and vendors reads were removed in
		// favour of consumer-declared seams; the SQL still joins vendors in
		// the profile list and the run list, and users for the audit names.
		// The gate records what the file does, not what its header wishes.
		reason: "price file profile and run lists join vendor names and audit user names (the header's seams-claim is stale; the joins remain), read only",
	},
	{
		module: "pricing",
		file:   "core/internal/pricing/syncscope.go",
		tables: []string{"products"},
		writes: []string{"scope_sequences"},
		// the file's own header, THE PRODUCTS READ, stated plainly: the
		// module's own shipped apply statement already derives the anchor's
		// unit from products, and this adapter reads products.org_id beside it
		// for the org fence, fail closed on a NULL or foreign org.
		reason: "the sync adapter reads products.org_id for the org fence beside the shipped apply path's own products read, stated in the file header, plus the 0213 seq stamp",
	},
	{
		module: "pricing",
		file:   "core/internal/pricing/w4_repository.go",
		tables: []string{"products", "vendors"},
		// the file's own header: two deliberate, read-only JOIN exceptions
		// that exist purely to put a human-readable label on an admin list;
		// without them the manager's screens render columns of uuids.
		reason: "promo, rebate and conversion admin lists join product sku and vendor name for display labels, stated in the file header; read only",
	},
	{
		module: "printing",
		file:   "core/internal/printing/receipt_repository.go",
		tables: []string{"customers", "invoices", "locations", "order_lines", "orders", "payment_allocations", "payments", "pos_sale", "pos_sale_tenders", "products", "quote_policy", "staff_role", "system_settings", "users"},
		reason: "receipt assembly reads the whole printed truth: org name, HST and quantity notice settings, the branch, the sale, its lines and tenders, the invoice it billed, the payments and allocations, the customer and the staff who took it, and the role label of the manager who approved a discount (Discount approved by), read only",
	},
	{
		module: "procurement",
		file:   "core/internal/procurement/repository.go",
		tables: []string{"products"},
		reason: "procurement project lines resolve the product sku and name the project document shows, read only",
	},
	{
		module: "purchasing",
		file:   "core/internal/purchasing/incoming_repository.go",
		tables: []string{},
		writes: []string{"scope_sequences"},
		reason: "sync allocator: the 0213 seq stamp on pushed rows (shared pattern above)",
	},
	{
		module: "purchasing",
		file:   "core/internal/purchasing/repository.go",
		tables: []string{"products"},
		writes: []string{"scope_sequences"},
		reason: "the PO line columns join the product sku the line shows (the identical join vendor_order_repository.go's header documents) and the 0213 seq stamp marks rows for device pull",
	},
	{
		module: "purchasing",
		file:   "core/internal/purchasing/syncscope.go",
		tables: []string{"locations"},
		writes: []string{"scope_sequences"},
		reason: "org fence through the locations anchor (read only) plus the 0213 seq stamp",
	},
	{
		module: "purchasing",
		file:   "core/internal/purchasing/vendor_order_repository.go",
		tables: []string{"products"},
		// the file's own header: the only non-purchasing table touched is
		// products, for the SKU a mapping editor has to show beside the
		// vendor's code.
		reason: "vendor catalogue mapping shows the product sku beside the vendor's code, stated in the file header; read only",
	},
	{
		module: "quote",
		file:   "core/internal/quote/acceptcontrols_repository.go",
		tables: []string{"orders", "system_settings"},
		writes: []string{"system_settings"},
		// the file's own header: AllowMultiAccept answers the display
		// questions (should the accept form be offered, should CanConvert
		// appear); the DIB-393 admin switch lives in system_settings.
		reason: "quote accept controls read whether the quote already converted (orders) and keep the admin accept switch in the system_settings substrate, stated in the file header",
	},
	{
		module: "quote",
		file:   "core/internal/quote/builder_repository.go",
		tables: []string{"product_branch_costs", "product_images", "products", "system_settings", "users"},
		// the file's own header: the margin-glance raw read over the
		// per-branch WAC join, THE engine's cost source; products and images
		// for the builder display; the HST default from settings; users for
		// the prepared-by name.
		reason: "quote builder reads the margin cost source (per-branch WAC), product display facts, the HST default and the preparer name, stated in the file header; read only",
	},
	{
		module: "quote",
		file:   "core/internal/quote/credit.go",
		tables: []string{"customer_transactions", "customers"},
		// the file's own header: the quote to order conversion is an ORDER
		// WRITE that draws on the customer's account; this gate reads the
		// account state so a customer on an ACTIVE hold cannot convert their
		// way past the counter.
		reason: "DIB-387 conversion credit gate reads the account's AR position and customer facts to refuse conversion under an active hold, stated in the file header; read only",
	},
	{
		module: "quote",
		file:   "core/internal/quote/edit_repository.go",
		tables: []string{"audit_log", "customers", "locations", "products", "users"},
		writes: []string{"scope_sequences"},
		reason: "the quote editor joins the customer, branch, product and preparer facts the document shows, and its acceptance history reads the quote's own staff decision rows from audit_log (a staff accept writes no acceptance row; the audit row is its record), read only; the 0213 seq stamp marks its rows",
	},
	{
		module: "quote",
		file:   "core/internal/quote/lineage_repository.go",
		tables: []string{"invoices", "orders"},
		// the file's own header: the lineage read walks exactly the links the
		// conversion left behind (its orders and their invoices), document
		// HEADERS only, no lines, no costs.
		reason: "quote lineage reads the order and invoice headers the conversion produced, stated in the file header; read only",
	},
	{
		module: "quote",
		file:   "core/internal/quote/order_payment_repository.go",
		tables: []string{"customers"},
		// the file's own header and the function's comment: OrderPaymentFactsTx
		// reads the quote's status, owner, branch, figures, lines sum, the
		// customer's credit limit and the orders its acceptances became, on
		// the door's transaction; the order, the invoice and the payment are
		// reached through the seams in order_payment.go.
		reason: "the conversion door's facts read takes the customer's credit limit beside the quote's own figures on the door's transaction, stated in the function's comment; new in HH-RD2 (the Quote Desk cycle), a same transaction read, granted to move behind the owning module's transaction taking seam (the till hold's WithCallerTx shape)",
	},
	{
		module: "quote",
		file:   "core/internal/quote/repository.go",
		tables: []string{"locations", "order_lines", "orders", "products"},
		writes: []string{"order_lines", "orders", "scope_sequences"},
		// the lineage header states it: this module already writes
		// orders/order_lines inside Convert, the conversion IS the order
		// write, 0008's design. products feeds the quote lines' display.
		reason: "conversion writes the order and its lines (0008's design: the conversion is the order write, stated in lineage_repository.go's header), stamps the order's org through the org anchor read on locations (read only, locations.id and locations.org_id, UX-95), and reads product facts for the quote's own lines",
	},
	{
		module: "quote",
		file:   "core/internal/quote/reserve_repository.go",
		tables: []string{"products", "stock_reservations"},
		reason: "quote holds read the existing reservations and the product facts the hold shows; the reserve write itself goes through the injected StockReserver",
	},
	{
		module: "quote",
		file:   "core/internal/quote/share.go",
		tables: []string{"audit_log"},
		reason: "the public share surface reads the audit trail's seq for the quote to detect replay of a logged act; read only",
	},
	{
		module: "quote",
		file:   "core/internal/quote/share_idempotency.go",
		tables: []string{"audit_log"},
		// the function's own comment: findKeyedAcceptance returns the earlier
		// acceptance of this quote through this share link that carried the
		// key, rebuilt as the confirmation it first produced, and must run
		// under the quote row lock.
		reason: "share acceptance idempotency rebuilds the earlier keyed acceptance from the audit trail inside the accept's transaction, stated in the function's comment (it must run under the quote row lock); new in HH-RD2 (the Quote Desk cycle), a same transaction read, granted to move behind the owning module's transaction taking seam (the till hold's WithCallerTx shape)",
	},
	{
		module: "quote",
		file:   "core/internal/quote/share_repository.go",
		tables: []string{"customers", "product_images", "products"},
		writes: []string{"scope_sequences"},
		reason: "the public share payload resolves the customer name, product facts and the image the public page shows; the 0213 seq stamp marks its rows",
	},
	{
		module: "quote",
		file:   "core/internal/quote/staff_decision_repository.go",
		tables: []string{"audit_log"},
		writes: []string{"scope_sequences"},
		reason: "the staff accept and reject replay a keyed decision from the audit trail inside the decision's own transaction under the quote row lock (the share_idempotency.go shape, read only), and the 0213 seq stamp marks the quote row it moves",
	},
	{
		module: "quote",
		file:   "core/internal/quote/syncscope.go",
		tables: []string{"locations"},
		writes: []string{"scope_sequences"},
		reason: "org fence through the locations anchor (read only) plus the 0213 seq stamp",
	},
	{
		module: "reporting",
		file:   "core/internal/reporting/commission_repo.go",
		tables: []string{"customers", "invoice_lines", "invoices", "product_categories", "products", "rep_assignments", "users", "v_product_weighted_avg_cost"},
		// the module's own header: PEER TABLES, READ ONLY, and only through
		// the fixed queries; a reporting module that writes is a reporting
		// module that can be blamed for a number changing.
		reason: "commission attribution reads the sales facts it reports on (invoices, lines, products, categories, customers, rep assignments, cost view), stated in the module header; read only",
	},
	{
		module: "reporting",
		file:   "core/internal/reporting/catalog.go",
		tables: []string{"ar_statement_runs", "credit_holds", "customers", "delivery_stops", "invoice_lines", "invoices", "locations", "payments", "product_categories", "product_reorder_points", "products", "purchasing_bill_lines", "purchasing_bills", "purchasing_po_lines", "purchasing_pos", "purchasing_receipt_lines", "purchasing_receipts", "rep_assignments", "stock_balances", "stock_moves", "users", "v_ar_aging", "vendors", "v_product_weighted_avg_cost", "warranty_claims"},
		// the file's own header: the compiled report definitions, ONE fixed
		// SQL string per template code; these queries read PEER TABLES,
		// read-only, no locks beyond a snapshot, and the coupling is
		// deliberate, the price of a reporting module that reports on the
		// business rather than on a private copy of it.
		reason: "the compiled report definitions read the peer tables they report on, read only, stated in the file's own header (the statements live in catalogEntry.sql fields the runner executes)",
	},
	{
		module: "reporting",
		file:   "core/internal/reporting/repository.go",
		tables: []string{},
		writes: []string{"print_queue"},
		// the module's own header: print_queue is the shared substrate every
		// printed document goes through; a new document type joins it by
		// widening the CHECK rather than growing a private print path.
		reason: "reports print through the shared print_queue substrate (0088), stated in the module header; the one sanctioned peer INSERT",
	},
	{
		module: "settings",
		file:   "core/internal/settings/store.go",
		tables: []string{"dashboard_role_profiles"},
		// the file's own header: role-profile resolution reads the 0140
		// dashboard_role_profiles table, one role to shape truth
		// program-wide; a second mapping for the same person is exactly the
		// drift 0149 ruled against.
		reason: "role-profile resolution reads the 0140 dashboard role profile mapping, stated in the file header; read only",
	},
	{
		module: "tasks",
		file:   "core/internal/tasks/repository.go",
		tables: []string{"locations"},
		writes: []string{"scope_sequences"},
		reason: "sync allocator: the 0213 seq stamp on pushed rows (shared pattern above); the REST reads (GetTask, ListTasks, the transition lock) hold a task's branch to the caller's org through the org anchor read on locations, read only, locations.id and locations.org_id and nothing else (UX-81 round 6: no writer stamps tasks.org_id on every path; approved, decision record row 2879)",
	},
	{
		module: "tax",
		file:   "core/internal/tax/repository.go",
		tables: []string{"tax_decision_snapshot"},
		writes: []string{"tax_decision_snapshot"},
		// the file's own header: the repository reads and writes the
		// inherited 0029 tax_decision_snapshot (created by the POS counter
		// migration, owned there by the topic rule); the sale ring inserts
		// the snapshot at sale time and tax reads it for the HST tie-out and
		// writes it for the admin replay paths.
		reason: "tax reads the snapshots the POS ring writes for the HST to GL tie-out and writes the admin replay paths' own snapshots, stated in the file header as the inherited 0029 table",
	},
	{
		module: "till",
		file:   "core/internal/till/history.go",
		tables: []string{"invoices", "payment_allocations", "payments", "users"},
		// the file's own header: the till owns the payments read side, so the
		// attributed histories live here; display names join users; a portal
		// payment surfaces with an empty name.
		reason: "attributed payment and application histories read the till's payment rows and join users for staff names, stated in the file header; read only",
	},
	{
		module: "till",
		file:   "core/internal/till/holds.go",
		tables: []string{"credit_holds"},
		// the file's own header: HasActiveHold backs both the AR console and
		// the hold-aware credit gate the POS Sale consults, fail closed while
		// a hold is active.
		reason: "the hold-aware credit gate reads active holds to deny credit tenders, stated in the file header; read only",
	},
	{
		module: "till",
		file:   "core/internal/till/recon_seam.go",
		tables: []string{"events_outbox", "payments"},
		writes: []string{"payments"},
		// the file's own header: FROZEN scaffold seam; the sanctioned
		// cross-module seam for bankrec and gl, read and deposit-stamp only.
		// The events_outbox READ is the till's own emission: the seam says
		// whether a lodgement carries its own journal (the movement-posted
		// event the till wrote in the movement's transaction), so the bank
		// match can confirm the line without booking the deposit twice. It
		// reads the till's own events, it writes none.
		reason: "the frozen bankrec and gl read seams over the payment rows (UnreconciledCardPayments, MarkDeposited, PaymentForGL) and the till's own movement-posted emission, which says whether a lodgement carries its own journal, stated in the file header and in carriesJournalSQL",
	},
	{
		module: "till",
		file:   "core/internal/till/repository.go",
		tables: []string{"credit_holds", "customer_transactions", "invoices", "locations", "nsf_policy", "payment_allocations", "payment_nsf", "payments"},
		writes: []string{"credit_holds", "customer_transactions", "events_outbox", "invoices", "nsf_policy", "payment_allocations", "payment_attempts", "payment_nsf", "payments", "scope_sequences"},
		// the counter ring: A-03 TakeTender inserts the payment and its
		// allocation and the AR transaction; the NSF rail (0035) records the
		// bounce and places and releases the holds it causes; invoices carry
		// the balances tenders apply against. The till's own seam header
		// claims sole ownership of the payments tables; the 0010 topic rule
		// gives them to the payments module and this entry is the fence
		// between the two readings (disclosed in the item report).
		reason: "the till ring takes tenders (payment, allocation, AR transaction), runs the 0035 NSF rail and the holds it places, and reads and adjusts the invoice balances tenders apply against",
	},
	{
		module: "warranty",
		file:   "core/internal/warranty/syncscope.go",
		tables: []string{"locations"},
		// the file's own header: the warranty claims and recovery board
		// pull-only stores; the org fence rides the branch through locations.
		reason: "the warranty sync scopes resolve their branch org fence through the locations anchor, read only",
	},
	{
		module: "warranty",
		file:   "core/internal/warranty/repository.go",
		tables: []string{"customers", "products", "users", "vendors"},
		reason: "warranty claims join the customer, product, vendor and staff facts the claim and its routing show, read only",
	},
	{
		module: "warranty",
		file:   "core/internal/warranty/repository_w5b.go",
		tables: []string{"users", "vendors"},
		reason: "the recovery board joins the vendor and staff names the recovery rows show, read only",
	},
}
