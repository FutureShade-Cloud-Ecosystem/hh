package sync

// seqstreams.go: the sync seq trigger's table to stream map (migration 0422, ADR 0025), kept in code so
// the catalogue test here and the demo build's seq repair map (cmd/core) check their lists against the
// same one. It changes only with a migration that adds or retires a trigger.

// seqTriggerStreams: every table that carries the sync_seq_draw trigger, with its one stream. The source of
// each stream: the live writers on main that already draw (grep of every scope_sequences draw), the plan's
// section 3, and the demo repair map's (demoSeqTables), which all agree on the entries below. A table no
// writer draws for yet (SF-3 to SF-14 own those) takes the stream its scope's store pulls on.
var seqTriggerStreams = map[string]string{
	"payments":                     "payments",
	"invoices":                     "invoices",
	"quotes":                       "quotes",
	"delivery_runs":                "runs",
	"delivery_stops":               "stops",
	"delivery_pod_photos":          "pod",
	"products":                     "products",
	"customers":                    "customers",
	"vendors":                      "vendors",
	"portal_payment_methods":       "payment_methods",
	"portal_users":                 "team",
	"warranty_claims":              "warranty_claims",
	"pos_parked_tickets":           "parked_tickets",
	"customer_quote":               "customer_quote",
	"procurement_projects":         "projects",
	"dispatch_trucks":              "trucks",
	"dispatch_drivers":             "drivers",
	"attachment":                   "attachments",
	"plan":                         "orders",
	"plan_line":                    "scope_items",
	"permission_approval_requests": "approvals",
	"pricing_list_prices":          "price_matrix",
	"purchasing_pos":               "purchase_orders",
	"task_shortages":               "shortages",
	"count_sheets":                 "cycle_counts",
	"will_call_pickups":            "will_call",
}

// SeqTriggerStreams returns a copy of the table to stream map of the sync seq trigger.
func SeqTriggerStreams() map[string]string {
	out := make(map[string]string, len(seqTriggerStreams))
	for tb, s := range seqTriggerStreams {
		out[tb] = s
	}
	return out
}
