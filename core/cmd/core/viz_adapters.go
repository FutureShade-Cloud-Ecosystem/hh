package main

// viz_adapters.go, the SeriesProvider adapters for the home screen's composable metric catalog.
//
// SAME SEAM, SAME OBLIGATION AS THE TILE ADAPTERS (see main.go's SWARM-W6-A block). internal/dashboard
// declares SeriesProvider and knows nothing about who owns the data; every implementation lives here,
// as SQL, over the module that does. And the non-negotiable half is unchanged: Scope.BranchIDs /
// Scope.AllBranches are the caller's GRANTS and every query below applies them through w6aBranchScope.
// A composed chart is not a way around branch scoping.
//
// THE SECOND GATE. The two cost-class metrics refuse outright when Scope.CostAllowed is false. The
// service already refuses to build those tiles for such a caller (internal/dashboard/viz_service.go),
// so this can only fire if some future route forgets the gate, which is exactly when you want a
// loud error in the log and "Temporarily unavailable" on the screen rather than a margin chart.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/jackc/pgx/v5/pgxpool"
)

// errCostNotAllowed is the refusal a cost-class series answers when it is reached without the gate.
var errCostNotAllowed = errors.New("dashboard: cost-class series refused for a caller without cost visibility")

// vizPoints runs one labelled-bucket query and returns its rows in the order the SQL decided.
//
// ORDER IS THE QUERY'S, NOT THE CLIENT'S. A bar chart's order is a statement, ranked by size for a
// comparison, chronological for a band, and letting the browser re-sort would make two people
// reading the same chart disagree about which branch is worst.
func vizPoints(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) ([]dashboard.Point, error) {
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dashboard.Point{}
	for rows.Next() {
		var p dashboard.Point
		if err := rows.Scan(&p.Label, &p.Number); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// vizUnknownDim is what a provider answers for a dimension it does not implement: an EMPTY series,
// not an error. The catalog already refuses a dimension the metric does not declare, so reaching
// here means a build shipped a dimension ahead of its query, and an empty chart that says "nothing
// to draw yet" is the honest face of that, where an error surface would blame the module.
func vizUnknownDim() ([]dashboard.Point, error) { return []dashboard.Point{}, nil }

// -- pos · sales today, by branch ---------------------------------------------------------------
func vizSalesToday(pool *pgxpool.Pool) dashboard.SeriesFunc {
	const byBranch = `
		SELECT l.code::text,
		       COALESCE(SUM(COALESCE(i.total_cents, o.total_cents, 0)), 0)::bigint
		  FROM pos_sale s
		  JOIN locations l  ON l.id = s.branch_id
		  LEFT JOIN invoices i ON i.id = s.invoice_id
		  LEFT JOIN orders   o ON o.id = s.order_id
		 WHERE s.status = 'COMMITTED'
		   AND s.created_at >= $2
		   AND s.created_at <  $3
		   AND ($1::uuid[] IS NULL OR s.branch_id = ANY($1::uuid[]))
		 GROUP BY l.code
		 ORDER BY 2 DESC, 1`
	return func(ctx context.Context, sc dashboard.Scope, dim string) ([]dashboard.Point, error) {
		if dim != dashboard.DimBranch {
			return vizUnknownDim()
		}
		since, until := w6aWindow(sc)
		return vizPoints(ctx, pool, byBranch, w6aBranchScope(sc), since, until)
	}
}

// w6aWindow is the [since, until) a measurement covers: the widget's own resolved window when it has
// one, and the house's today when it does not.
//
// TWO CALLERS TODAY AND THAT IS THE POINT. sales.today and deliveries.today are the only two of the
// eight whose SQL is shaped to take a window, one half-open range on one column each, so they are
// the only two that can honestly honour one. The other six have no date predicate at all, which is
// why their catalog entries offer `open`, `now` or `all_time` and never a period.
//
// THE FALLBACK IS NOT "EVERYTHING". A zero window means the caller had no widget, the composed-chart
// route, a preview, and for these two measures the honest answer there is still today: their labels
// say "Sales today" and "Deliveries today", and widening them to all of history because a window was
// absent would be a worse lie than the one this whole change removed.
func w6aWindow(sc dashboard.Scope) (time.Time, time.Time) {
	if !sc.Window.IsZero() {
		return sc.Window.Since, sc.Window.Until
	}
	w := dashboard.ResolveWindowIn(sc.Zone, dashboard.RangeToday, sc.Now)
	return w.Since, w.Until
}

// -- order · open orders, by status or by branch ------------------------------------------------
func vizOrdersOpen(pool *pgxpool.Pool) dashboard.SeriesFunc {
	const byStatus = `
		SELECT status::text, COUNT(*)::bigint FROM orders
		 WHERE status NOT IN ('DRAFT', 'CLOSED', 'CANCELLED')
		   AND ($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))
		 GROUP BY status ORDER BY 2 DESC, 1`
	const byBranch = `
		SELECT l.code::text, COUNT(*)::bigint FROM orders o
		  JOIN locations l ON l.id = o.branch_id
		 WHERE o.status NOT IN ('DRAFT', 'CLOSED', 'CANCELLED')
		   AND ($1::uuid[] IS NULL OR o.branch_id = ANY($1::uuid[]))
		 GROUP BY l.code ORDER BY 2 DESC, 1`
	return func(ctx context.Context, sc dashboard.Scope, dim string) ([]dashboard.Point, error) {
		switch dim {
		case dashboard.DimStatus:
			return vizWordedPoints(ctx, pool, byStatus, vizOrderStatusWords, w6aBranchScope(sc))
		case dashboard.DimBranch:
			return vizPoints(ctx, pool, byBranch, w6aBranchScope(sc))
		}
		return vizUnknownDim()
	}
}

// -- billing · AR, by aging bucket or by branch -------------------------------------------------
//
// The bucket series is the shipped aging tile's own arithmetic (main.go w6aARAging), in the same
// order, on purpose: a chart of AR and the AR tile beside it must not be able to disagree, and the
// only way to guarantee that is for both to ask the same question of the same columns.
func vizARAging(pool *pgxpool.Pool) dashboard.SeriesFunc {
	const byBucket = `
		SELECT b.label, COALESCE(SUM(i.balance_cents), 0)::bigint
		  FROM (VALUES (1, 'Current'), (2, '1-30'), (3, '31-60'), (4, '60+')) AS b (ord, label)
		  LEFT JOIN invoices i
		    ON i.status IN ('FINALIZED', 'PARTIALLY_PAID')
		   AND i.balance_cents > 0
		   AND ($1::uuid[] IS NULL OR i.branch_id = ANY($1::uuid[]))
		   AND b.ord = CASE
		         WHEN i.due_date >= $2::date THEN 1
		         WHEN i.due_date >= $2::date - 30 THEN 2
		         WHEN i.due_date >= $2::date - 60 THEN 3
		         ELSE 4 END
		 GROUP BY b.ord, b.label
		 ORDER BY b.ord`
	// The branch arm is PAST DUE per branch (due before the house's day), the same cut the bucket
	// arm's non-Current bands make, so the branch points sum to the buckets' non-Current total and
	// a past-due card's branch rows tie with its own headline. All open balances by branch is the
	// aging view's job, the bucket arm.
	const byBranch = `
		SELECT l.code::text, COALESCE(SUM(i.balance_cents), 0)::bigint
		  FROM invoices i
		  JOIN locations l ON l.id = i.branch_id
		 WHERE i.status IN ('FINALIZED', 'PARTIALLY_PAID')
		   AND i.balance_cents > 0
		   AND i.due_date < $2::date
		   AND ($1::uuid[] IS NULL OR i.branch_id = ANY($1::uuid[]))
		 GROUP BY l.code ORDER BY 2 DESC, 1`
	return func(ctx context.Context, sc dashboard.Scope, dim string) ([]dashboard.Point, error) {
		switch dim {
		case dashboard.DimBucket:
			return vizPoints(ctx, pool, byBucket, w6aBranchScope(sc), w6aDay(sc))
		case dashboard.DimBranch:
			return vizPoints(ctx, pool, byBranch, w6aBranchScope(sc), w6aDay(sc))
		}
		return vizUnknownDim()
	}
}

// -- warranty · open claims, by phase or by branch ----------------------------------------------
func vizWarrantyOpen(pool *pgxpool.Pool) dashboard.SeriesFunc {
	const byStatus = `
		SELECT phase::text, COUNT(*)::bigint FROM warranty_claims
		 WHERE phase NOT IN ('CLOSED', 'REJECTED', 'ABANDONED')
		   AND ($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))
		 GROUP BY phase ORDER BY 2 DESC, 1`
	const byBranch = `
		SELECT l.code::text, COUNT(*)::bigint FROM warranty_claims c
		  JOIN locations l ON l.id = c.branch_id
		 WHERE c.phase NOT IN ('CLOSED', 'REJECTED', 'ABANDONED')
		   AND ($1::uuid[] IS NULL OR c.branch_id = ANY($1::uuid[]))
		 GROUP BY l.code ORDER BY 2 DESC, 1`
	return func(ctx context.Context, sc dashboard.Scope, dim string) ([]dashboard.Point, error) {
		switch dim {
		case dashboard.DimStatus:
			return vizWordedPoints(ctx, pool, byStatus, vizClaimPhaseWords, w6aBranchScope(sc))
		case dashboard.DimBranch:
			return vizPoints(ctx, pool, byBranch, w6aBranchScope(sc))
		}
		return vizUnknownDim()
	}
}

// -- dispatch · today's stops, by status or by branch -------------------------------------------
func vizDeliveriesToday(pool *pgxpool.Pool) dashboard.SeriesFunc {
	const byStatus = `
		SELECT status::text, COUNT(*)::bigint FROM delivery_stops
		 WHERE scheduled_date >= $2::date AND scheduled_date < $3::date
		   AND status NOT IN ('DELIVERED', 'CANCELLED')
		   AND ($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))
		 GROUP BY status ORDER BY 2 DESC, 1`
	const byBranch = `
		SELECT l.code::text, COUNT(*)::bigint FROM delivery_stops d
		  JOIN locations l ON l.id = d.branch_id
		 WHERE d.scheduled_date >= $2::date AND d.scheduled_date < $3::date
		   AND d.status NOT IN ('DELIVERED', 'CANCELLED')
		   AND ($1::uuid[] IS NULL OR d.branch_id = ANY($1::uuid[]))
		 GROUP BY l.code ORDER BY 2 DESC, 1`
	return func(ctx context.Context, sc dashboard.Scope, dim string) ([]dashboard.Point, error) {
		since, until := w6aWindow(sc)
		switch dim {
		case dashboard.DimStatus:
			return vizWordedPoints(ctx, pool, byStatus, vizStopStatusWords, w6aBranchScope(sc), since, until)
		case dashboard.DimBranch:
			return vizPoints(ctx, pool, byBranch, w6aBranchScope(sc), since, until)
		}
		return vizUnknownDim()
	}
}

// -- tasks · pick queue, by status or by branch -------------------------------------------------
func vizPickQueue(pool *pgxpool.Pool) dashboard.SeriesFunc {
	const byStatus = `
		SELECT status::text, COUNT(*)::bigint FROM tasks
		 WHERE task_type = 'PICK'
		   AND status IN ('QUEUED', 'PUSHED', 'ACCEPTED', 'IN_PROGRESS')
		   AND ($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))
		 GROUP BY status ORDER BY 2 DESC, 1`
	const byBranch = `
		SELECT l.code::text, COUNT(*)::bigint FROM tasks t
		  JOIN locations l ON l.id = t.branch_id
		 WHERE t.task_type = 'PICK'
		   AND t.status IN ('QUEUED', 'PUSHED', 'ACCEPTED', 'IN_PROGRESS')
		   AND ($1::uuid[] IS NULL OR t.branch_id = ANY($1::uuid[]))
		 GROUP BY l.code ORDER BY 2 DESC, 1`
	return func(ctx context.Context, sc dashboard.Scope, dim string) ([]dashboard.Point, error) {
		switch dim {
		case dashboard.DimStatus:
			return vizWordedPoints(ctx, pool, byStatus, vizTaskStatusWords, w6aBranchScope(sc))
		case dashboard.DimBranch:
			return vizPoints(ctx, pool, byBranch, w6aBranchScope(sc))
		}
		return vizUnknownDim()
	}
}

// -- inventory · on-hand at weighted average cost, by branch (COST CLASS) -----------------------
//
// Weighted average is the house cost basis (product_branch_costs, the same table the GL's
// cost-basis posting reads), so this chart and the ledger are answering from one number.
func vizInventoryValue(pool *pgxpool.Pool) dashboard.SeriesFunc {
	const byBranch = `
		SELECT l.code::text,
		       COALESCE(SUM(sb.on_hand_base * c.avg_unit_cost_cents), 0)::bigint
		  FROM stock_balances sb
		  JOIN locations l ON l.id = sb.branch_id
		  JOIN product_branch_costs c
		    ON c.product_id = sb.product_id AND c.branch_id = sb.branch_id
		 WHERE sb.on_hand_base > 0
		   AND ($1::uuid[] IS NULL OR sb.branch_id = ANY($1::uuid[]))
		 GROUP BY l.code ORDER BY 2 DESC, 1`
	return func(ctx context.Context, sc dashboard.Scope, dim string) ([]dashboard.Point, error) {
		if !sc.CostAllowed {
			return nil, errCostNotAllowed
		}
		if dim != dashboard.DimBranch {
			return vizUnknownDim()
		}
		return vizPoints(ctx, pool, byBranch, w6aBranchScope(sc))
	}
}

// -- pos · gross margin in basis points, by branch (COST CLASS) ---------------------------------
//
// Revenue is the stored line total (net of line discount, margin is spoken after the discount,
// which is what the dealer is actually giving up: quote's MarginGlance makes the same call). Cost
// is the branch weighted average at the quantity sold. Integer bps throughout: the server is the
// arithmetic authority and a rate the browser divides is a rate two screens can disagree about.
func vizGrossMargin(pool *pgxpool.Pool) dashboard.SeriesFunc {
	const byBranch = `
		SELECT l.code::text,
		       CASE WHEN COALESCE(SUM(ol.line_total_cents), 0) <= 0 THEN 0::bigint
		            ELSE (((SUM(ol.line_total_cents)
		                    - COALESCE(SUM(ol.qty_base * c.avg_unit_cost_cents), 0)) * 10000)
		                  / SUM(ol.line_total_cents))::bigint END
		  FROM order_lines ol
		  JOIN orders o    ON o.id = ol.order_id
		  JOIN locations l ON l.id = o.branch_id
		  LEFT JOIN product_branch_costs c
		    ON c.product_id = ol.product_id AND c.branch_id = o.branch_id
		 WHERE o.status NOT IN ('DRAFT', 'CANCELLED')
		   AND ($1::uuid[] IS NULL OR o.branch_id = ANY($1::uuid[]))
		 GROUP BY l.code ORDER BY 2 DESC, 1`
	// The aggregate arm (SplitNone): the WEIGHTED all branches rate, the same arithmetic run
	// ungrouped, so total margin cents over total sales cents across the scope. The service asks
	// for it as the tile's headline total; a rate is never summed from its branch points
	// (FROM-PRINCIPAL 132).
	const aggregate = `
		SELECT 'All branches',
		       CASE WHEN COALESCE(SUM(ol.line_total_cents), 0) <= 0 THEN 0::bigint
		            ELSE (((SUM(ol.line_total_cents)
		                    - COALESCE(SUM(ol.qty_base * c.avg_unit_cost_cents), 0)) * 10000)
		                  / SUM(ol.line_total_cents))::bigint END
		  FROM order_lines ol
		  JOIN orders o    ON o.id = ol.order_id
		  LEFT JOIN product_branch_costs c
		    ON c.product_id = ol.product_id AND c.branch_id = o.branch_id
		 WHERE o.status NOT IN ('DRAFT', 'CANCELLED')
		   AND ($1::uuid[] IS NULL OR o.branch_id = ANY($1::uuid[]))`
	return func(ctx context.Context, sc dashboard.Scope, dim string) ([]dashboard.Point, error) {
		if !sc.CostAllowed {
			return nil, errCostNotAllowed
		}
		switch dim {
		case dashboard.DimBranch:
			return vizPoints(ctx, pool, byBranch, w6aBranchScope(sc))
		case dashboard.SplitNone:
			return vizPoints(ctx, pool, aggregate, w6aBranchScope(sc))
		}
		return vizUnknownDim()
	}
}

// vizSeriesRegistrations is the whole wiring table, so main.go's dashboard block grows by one loop
// rather than by eight closures.
func vizSeriesRegistrations(pool *pgxpool.Pool) []struct {
	Metric   string
	Provider dashboard.SeriesProvider
} {
	return []struct {
		Metric   string
		Provider dashboard.SeriesProvider
	}{
		{dashboard.MetricSalesByBranch, vizSalesToday(pool)},
		{dashboard.MetricOrdersOpen, vizOrdersOpen(pool)},
		{dashboard.MetricARBalance, vizARAging(pool)},
		{dashboard.MetricWarrantyOpen, vizWarrantyOpen(pool)},
		{dashboard.MetricDeliveriesToday, vizDeliveriesToday(pool)},
		{dashboard.MetricPickQueue, vizPickQueue(pool)},
		{dashboard.MetricInventoryValue, vizInventoryValue(pool)},
		{dashboard.MetricGrossMargin, vizGrossMargin(pool)},
	}
}

// -- the series LABEL is copy, and four of these queries were shipping a database column ---------
//
// FOUND IN THE BROWSER, not by a scan, and it is a third door onto the same rule. Item 3 closed
// "an enum interpolated into a template" (app/src) and "an enum stored as a row" (0199). This is the
// third: an enum SELECTed as a label and handed to a chart. `SELECT status::text` makes the point's
// label the column's own value, so the Home widget for today's deliveries drew a bar labelled
// "READY" and the open-orders split drew "PARTIALLY_FULFILLED".
//
// No client gate can see it, _counter-vocabulary.test.mjs scans app/src and the words are not in
// app/src, they arrive over the wire. No Go gate saw it either, because nothing was interpolating an
// enum into a sentence; the enum simply WAS the answer.
//
// WHY THE WORDS LIVE HERE. These four queries live here, the composition root owns the SQL that
// produces the label, so it owns the label. Pushing the mapping into the modules would mean four
// modules exporting a vocabulary for a chart they do not know exists, and pushing it into the client
// would mean the client knowing which of a metric's dimensions happens to be enum-valued. The
// dimension that needs words is the one this file wrote a GROUP BY for.
//
// Every map is complete against its CHECK constraint, read out of the live schema rather than
// remembered, and the default arm degrades the way the whole product's do: lowercased, underscores
// out, so a value added to a CHECK later reads as strange rather than as a confident wrong word.

// vizState is what one stored status becomes on a screen: the WORD a person reads, and the SEVERITY
// that word carries, if it carries one at all.
//
// -- WHY SEVERITY LIVES BESIDE THE WORD ---------------------------------------------------------
//
// Ruled 1 Sep 2026: a status board is toned by what the status MEANS, not by how full its bucket is.
// It had been toned by fullness, (value/max) against the doctrine's 80/95 thresholds, and the
// fullest bucket is 100% BY CONSTRUCTION, so the largest bucket on every board was painted `stop`
// and labelled Urgent whatever it was. A delivery day whose biggest bucket was "Ready" showed red.
//
// It has to be decided here because only here is the raw enum still known: item 3 closed the third
// door by wording these labels server-side, so the browser receives "Ready" and could map it back to
// a severity only by putting READY back on the wire.
//
// -- MOST STATES HAVE NO SEVERITY, AND THAT IS THE POINT ----------------------------------------
//
// TWENTY-EIGHT of the thirty-one states below are neutral, and only THREE carry a colour. A board
// painted a colour per row says
// nothing, and the screen doctrine reserves good/watch/stop for STATE and requires a word or an icon
// beside the colour, never colour alone. READY, LOADED and EN_ROUTE are not good or bad; they are
// where the thing is. The test is narrow and worth stating: WOULD A PERSON DO SOMETHING DIFFERENT
// BECAUSE OF THIS COLOUR? If not, it is neutral.
//
// Note also that most of these values never reach a board at all, because every board query filters
// to an open population first, deliveries.today excludes DELIVERED and CANCELLED, picks.queue is
// four statuses, orders.open excludes DRAFT/CLOSED/CANCELLED, warranty.open_claims excludes the
// three terminal phases. The maps are complete against the CHECK anyway, so a dealer who composes a
// board over a different population gets an honest answer rather than a default.
//
// -- NO STATE IS GREEN, WHICH TOOK TWO GOES TO GET RIGHT ----------------------------------------
//
// The first cut of this file marked DELIVERED, DONE and both CLOSEDs `good`, on the reading that
// terminal success deserves a colour. It fails the file's own test three lines up, nobody in a yard
// does anything different because a bar is green, so it was decoration, and mostly-green says
// exactly as much as mostly-red. Two things settled it beyond the argument:
//
//	Every one of the four is on a value its OWN query excludes, so all four were dead the day they
//	were written; the first time they would have rendered is the day a dealer widened a population,
//	which is the worst moment to discover a board has gone green.
//
//	`DONE: good` directly contradicted the yard console, which paints the SAME enum value of the
//	SAME column the other way: app/src/yard-logic.ts statusTone gives STAGED the ready colour and
//	DONE a dim `done`. Two surfaces, one enum, inverted logic, and the console is right, because
//	STAGED is the state a yard hand acts on.
//
// So `good` has no producer here at all. It keeps one in the Needs-you queue (needs.go SeverityFor),
// which scores on COUNT AND AGE, which is what severity on a queue actually is.
type vizState struct {
	Word string
	Tone string // "" neutral - dashboard.ToneGood - ToneWatch - ToneStop
}

var vizStopStatusWords = map[string]vizState{ // delivery_stops.status, 0090:166
	"READY":    {Word: "Ready"},    // waiting to be loaded: a stage, not a problem
	"LOADED":   {Word: "Loaded"},   // on the truck: a stage
	"EN_ROUTE": {Word: "En route"}, // out for delivery: a stage, and the one you want most of
	// A stop that could not be delivered is the one thing on this board somebody must pick up
	// today, and it is the only `stop` in the whole file.
	"FAILED":    {Word: "Failed", Tone: dashboard.ToneStop},
	"DELIVERED": {Word: "Delivered"}, // the day's work, and still a stage
	"CANCELLED": {Word: "Cancelled"}, // called off, by somebody, on purpose
}

var vizTaskStatusWords = map[string]vizState{ // tasks.status, 0073:30 + STAGED
	// EVERY PICK STATE IS NEUTRAL, and a whole board of one tone is the honest answer here. A pick
	// queue's severity is about AGE and DEPTH, how long the oldest has waited, how many there are -
	// and neither is visible in which STAGE a task is at. Colouring the biggest stage red told a
	// yard hand that the normal shape of their morning was an emergency.
	"QUEUED": {Word: "Queued"}, "PUSHED": {Word: "Pushed"}, "ACCEPTED": {Word: "Claimed"},
	"IN_PROGRESS": {Word: "Being picked"}, "STAGED": {Word: "Staged"},
	"DONE":      {Word: "Done"},
	"CANCELLED": {Word: "Cancelled"},
}

var vizOrderStatusWords = map[string]vizState{ // orders.status
	// A yard's order moves through these and every one of them is somebody's normal Tuesday.
	// PARTIALLY_FULFILLED is deliberately NOT a warning: part-filling is how hardscape supply works
	// when a pallet is short, and flagging it would colour half the board on a normal week.
	"DRAFT": {Word: "Draft"}, "CONFIRMED": {Word: "Confirmed"}, "PICKING": {Word: "Picking"},
	"READY": {Word: "Ready"}, "PARTIALLY_FULFILLED": {Word: "Part filled"},
	"FULFILLED": {Word: "Filled"}, "INVOICED": {Word: "Invoiced"},
	"CLOSED":    {Word: "Closed"},
	"CANCELLED": {Word: "Cancelled"},
}

var vizClaimPhaseWords = map[string]vizState{ // warranty_claims.phase, 0053
	"INTAKE": {Word: "Intake"}, "TRIAGE": {Word: "Triage"}, "SUBMITTED": {Word: "Submitted"},
	// The only phase where nothing is happening and nobody here can make it happen, the claim is
	// with the vendor. It is what a dealer chases, so it is the one phase worth marking.
	"WAITING_ON_DECISION": {Word: "Awaiting decision", Tone: dashboard.ToneWatch},
	"DECISION":            {Word: "Decision"},
	"SETTLEMENT":          {Word: "Settlement"},
	"CLOSED":              {Word: "Closed"},
	// A refused claim is money the yard now eats. Not an emergency, the decision is made, but it
	// is the one outcome on this board somebody should read rather than scroll past.
	"REJECTED":  {Word: "Rejected", Tone: dashboard.ToneWatch},
	"ABANDONED": {Word: "Abandoned"},
}

// vizWordedPoints runs a series query and words its labels through `words`.
//
// The VALUE is never touched, only the label a person reads. A dimension whose labels are already
// words (a branch code, a bucket name) does not come through here at all.
func vizWordedPoints(
	ctx context.Context, pool *pgxpool.Pool, sql string, words map[string]vizState, args ...any,
) ([]dashboard.Point, error) {
	points, err := vizPoints(ctx, pool, sql, args...)
	if err != nil {
		return nil, err
	}
	for i := range points {
		st := vizStateOf(points[i].Label, words)
		points[i].Label, points[i].Tone = st.Word, st.Tone
	}
	return points, nil
}

// vizStateOf is the house default arm, now for both halves. An unmapped value comes back as itself,
// lowercased with the underscores taken out, and NEUTRAL. A severity is never invented: a state
// added to a CHECK next year must read as strange rather than as an alarm nobody chose.
func vizStateOf(raw string, words map[string]vizState) vizState {
	if st, ok := words[raw]; ok {
		return st
	}
	return vizState{Word: vizWord(raw, nil)}
}

// vizWord is the house default arm: an unmapped value comes back as itself, lowercased with the
// underscores taken out, rather than as an invented phrase or a blank.
func vizWord(raw string, words map[string]string) string {
	if w, ok := words[raw]; ok {
		return w
	}
	return strings.ToLower(strings.ReplaceAll(raw, "_", " "))
}
