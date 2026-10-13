package billing

// statements.go, DIB-166: month-end statement mass-send + on-demand interim statements.
//
// -- A STATEMENT IS AN ARTEFACT, NOT A QUERY ------------------------------------------------------
// The one decision everything else follows from: a statement row stores the RENDERED HTML and the
// aging figures as they stood at as_of_date. It is not a saved query re-run on demand.
//
// A statement is a claim we made to a customer on a date. Re-deriving it later from live tables would
// silently rewrite history the moment an invoice is voided, a payment is reversed (DIB-170 makes that
// routine) or the aging policy changes. When a contractor calls in March about the January statement,
// the answer must be the January statement, not what January would look like if it happened today.
// It costs disk and it buys the ability to be held to your word.
//
// -- SENDING IS ENQUEUE-ONLY, AND SHIPS WITH NOTHING WIRED ----------------------------------------
// GenerateRun produces statements and marks the ones worth sending QUEUED. It does not deliver. With
// no outbox wired (the shipped default) a month-end run produces a full set of statements and sends
// nothing, which is the correct behaviour for a system that does not hold a verified email or mobile
// for most accounts. The alternative, sending on generate, makes the first accidental run an
// incident with customers in it.
//
// -- ZERO AND CREDIT BALANCES ARE GENERATED BUT NOT QUEUED ----------------------------------------
// Nobody wants a statement telling them they owe nothing, and a credit-balance statement is a
// conversation the office should choose to have rather than have automatically. Both are still
// GENERATED, because "we produced a statement for every account" is what makes a run auditable, and
// the run's generated/queued counters make the difference visible instead of looking like a bug.

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/dealer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Statement run modes (0105 CHECK).
const (
	RunModeMass    = "MASS"
	RunModeInterim = "INTERIM"
)

// Delivery statuses (0105 CHECK).
const (
	DeliveryNotQueued = "NOT_QUEUED"
	DeliveryQueued    = "QUEUED"
	DeliverySent      = "SENT"
	DeliveryFailed    = "FAILED"
)

// ActStatementRun is the audit verb for a statement run.
const ActStatementRun = "AR_STATEMENT_RUN"

// Statement is one ar_statements row.
type Statement struct {
	ID              string    `json:"id"`
	RunID           string    `json:"run_id"`
	CustomerID      string    `json:"customer_id"`
	CustomerName    string    `json:"customer_name,omitempty"`
	AsOfDate        time.Time `json:"as_of_date"`
	CurrentCents    int64     `json:"current_cents"`
	Days30Cents     int64     `json:"days_30_cents"`
	Days60Cents     int64     `json:"days_60_cents"`
	Days90PlusCents int64     `json:"days_90_plus_cents"`
	ClosingCents    int64     `json:"closing_balance_cents"`
	HTML            string    `json:"html,omitempty"`
	DeliveryStatus  string    `json:"delivery_status"`
	CreatedAt       time.Time `json:"created_at"`
}

// StatementRun is one ar_statement_runs row.
type StatementRun struct {
	ID             string     `json:"id"`
	Mode           string     `json:"mode"`
	BranchID       string     `json:"branch_id,omitempty"`
	AsOfDate       time.Time  `json:"as_of_date"`
	Status         string     `json:"status"`
	GeneratedCount int        `json:"generated_count"`
	QueuedCount    int        `json:"queued_count"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`

	// -- W6-C / 0148: the NOTICE accounting --
	//
	// The only question an operator actually asks before flipping HOS_STATEMENT_NOTICES for the first
	// time is "if I turn this on tonight, how many text messages go out?". QueuedCount does not answer
	// it: that counts statements that were SENDABLE, and with the flag off it is identical to the
	// number of notices that did not happen. A screen showing only QueuedCount cannot distinguish
	// "the wiring works and is switched off" from "the wiring is broken".
	//
	//	NoticesEnabled          was the outbox bound for THIS run? Stamped at run time, never re-read
	//	                        from the environment at display time, a run is a historical record,
	//	                        and re-reading the flag later would rewrite what happened.
	//	NoticeQueuedCount       notices actually handed to the SMS outbox. Flag off ⇒ a MEASURED zero.
	//	NoticeSuppressedCount   statements that were sendable and produced no notice because the outbox
	//	                        was unbound. THIS is the number the operator is asking for: it is
	//	                        exactly how many messages the flip would have sent for this run.
	//
	// Invariant, asserted in FinishRun's caller and stated in 0148:
	//   NoticeQueuedCount + NoticeSuppressedCount = QueuedCount
	NoticesEnabled        bool `json:"notices_enabled"`
	NoticeQueuedCount     int  `json:"notice_queued_count"`
	NoticeSuppressedCount int  `json:"notice_suppressed_count"`
	// NoticeAccountingRecorded is the DERIVED honesty flag, see NoticeAccountingHolds. False means
	// "this run predates the accounting, or its stamp did not land"; the surface shows a dash rather
	// than a confident zero, because a wrong zero is the one answer that would make an operator flip
	// the flag believing nothing would send.
	NoticeAccountingRecorded bool `json:"notice_accounting_recorded"`
}

// NoticeAccountingHolds reports whether 0148's invariant is satisfied for this run:
//
//	notice_queued_count + notice_suppressed_count = queued_count
//
// A run generated before 0148, or one whose non-fatal accounting stamp failed, has (false, 0, 0)
// against a non-zero queued_count and fails this test. A run with nothing sendable has 0 = 0 and
// passes, which is right: there was genuinely nothing to say.
func (r StatementRun) NoticeAccountingHolds() bool {
	return r.NoticeQueuedCount+r.NoticeSuppressedCount == r.QueuedCount
}

// StatementNotice is what a queued statement hands to the notification seam. It carries NO address:
// the recipient is resolved at delivery time by whatever provider is wired, exactly as the dispatch
// notice does. A notice that carried an address would be a place for a stale one to hide.
type StatementNotice struct {
	StatementID  string
	CustomerID   string
	AsOfDate     string
	BalanceCents int64
}

// StatementOutbox is the flag-off notification seam. The shipped default is nil, a run enqueues its
// statements in ar_statements (delivery_status QUEUED) and hands nothing to any provider.
type StatementOutbox interface {
	EnqueueStatementTx(ctx context.Context, tx pgx.Tx, n StatementNotice) error
}

// statementStore is the persistence the StatementService needs.
type statementStore interface {
	CreditPolicy(ctx context.Context, branchID string) (CreditPolicy, error)
	AgingRowsIn(ctx context.Context, asOf time.Time, bucketDays int, branchID string, reach BranchReach) ([]AgingRow, error)
	StatementLines(ctx context.Context, customerID string, asOf time.Time, reach BranchReach) ([]StatementLine, error)
	BranchHeader(ctx context.Context, branchID string) (StatementHeader, error)
	CreateRun(ctx context.Context, mode, branchID string, asOf time.Time, actorID string) (string, error)
	SaveStatement(ctx context.Context, runID string, s Statement, notice *StatementNotice, outbox StatementOutbox) (string, error)
	FinishRun(ctx context.Context, runID, status string, generated, queued int, runErr string) error
	GetRun(ctx context.Context, runID string) (StatementRun, []Statement, error)
	LatestStatement(ctx context.Context, customerID string) (Statement, bool, error)
	LatestStatementIn(ctx context.Context, customerID string, reach BranchReach) (Statement, bool, error)
}

// statementNoticeAccountant is the OPTIONAL store capability that records 0148's notice counts.
//
// Optional, and asserted rather than added to statementStore, on purpose: widening statementStore
// would break every unit fake in the package for a fact none of them models, and the run itself must
// keep working when the store predates the columns. A store that cannot count notices simply does not,
// and the run is otherwise identical, the shipStore precedent, applied to an accounting detail.
type statementNoticeAccountant interface {
	RecordRunNoticeCounts(ctx context.Context, runID string, enabled bool, queued, suppressed int) error
}

// StatementLine is one activity row on a statement: an invoice or a payment.
type StatementLine struct {
	Date        time.Time
	DocNo       string
	Description string
	ChargeCents int64
	CreditCents int64
	BalanceRun  int64
}

// StatementHeader is the branch's letterhead.
type StatementHeader struct {
	BranchName string
	Address    string
	Phone      string
}

// StatementService generates and (flag-off) enqueues statements.
type StatementService struct {
	store  statementStore
	outbox StatementOutbox
	audit  audit.Recorder
	clock  func() time.Time
	zone   houseday.Source // the dealer's business day zone; nil = the Ontario dealer's
}

// WithZone binds the dealer's business day zone source: a statement run is cut at the dealer's own
// calendar day. Nil keeps the Ontario dealer's.
func (s *StatementService) WithZone(src houseday.Source) *StatementService { s.zone = src; return s }

// NewStatementService builds the service.
func NewStatementService(store statementStore) *StatementService {
	return &StatementService{store: store, clock: time.Now}
}

// WithOutbox wires the notification seam. Nil (the default) means generate-and-queue, send nothing.
func (s *StatementService) WithOutbox(o StatementOutbox) *StatementService { s.outbox = o; return s }

// WithAudit attaches the audit recorder.
func (s *StatementService) WithAudit(r audit.Recorder) *StatementService { s.audit = r; return s }

// WithClock overrides the clock (tests).
func (s *StatementService) WithClock(now func() time.Time) *StatementService {
	if now != nil {
		s.clock = now
	}
	return s
}

// GenerateRun produces statements for every account with an open balance at a branch ("" = all).
//
// mode MASS is the month-end sweep; INTERIM is the on-demand single-account pull (pass exactly one
// customer in `only`). The two share every line of logic below and differ only in scope and in what
// the run is called, because "is this the June run, or did somebody pull an interim?" is a question
// the office asks, and answering it should not require inferring intent from a timestamp.
func (s *StatementService) GenerateRun(ctx context.Context, mode, branchID string, asOf time.Time, actorID string, only ...string) (StatementRun, []Statement, error) {
	if mode != RunModeMass && mode != RunModeInterim {
		return StatementRun{}, nil, fmt.Errorf("billing: unknown statement mode %q", mode)
	}
	if mode == RunModeInterim && len(only) != 1 {
		return StatementRun{}, nil, fmt.Errorf("billing: an INTERIM run is for exactly one account")
	}
	if asOf.IsZero() {
		asOf = s.clock()
	}
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return StatementRun{}, nil, err
	}
	asOf = houseClockIn(zone, asOf)

	// The caller's branch reach (branch_reach.go). A caller who reaches one branch files its run under
	// that branch, so the run can be read back by the same reach and no other.
	reach, err := reachFromContext(ctx).narrow(branchID)
	if err != nil {
		return StatementRun{}, nil, err
	}
	if !reach.Global && branchID == "" {
		switch len(reach.IDs) {
		case 0:
			return StatementRun{}, nil, ErrBranchForbidden
		case 1:
			branchID = reach.IDs[0]
		default:
			return StatementRun{}, nil, httpx.BadRequestf("name the branch this statement run is for")
		}
	}
	pol, err := s.store.CreditPolicy(ctx, branchID)
	if err != nil {
		return StatementRun{}, nil, err
	}
	rows, err := s.store.AgingRowsIn(ctx, asOf, pol.AgingBucketDays, branchID, reach)
	if err != nil {
		return StatementRun{}, nil, err
	}
	if len(only) > 0 {
		wanted := map[string]bool{}
		for _, id := range only {
			wanted[id] = true
		}
		filtered := rows[:0:0]
		for _, r := range rows {
			if wanted[r.CustomerID] {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	runID, err := s.store.CreateRun(ctx, mode, branchID, asOf, actorID)
	if err != nil {
		return StatementRun{}, nil, err
	}

	header, err := s.store.BranchHeader(ctx, branchID)
	if err != nil {
		// A missing letterhead must not fail a month end. The statement renders with a plain header,
		// which is worse-looking and entirely correct.
		header = StatementHeader{BranchName: dealer.Or("Your dealer")}
	}

	var generated, queued int
	out := make([]Statement, 0, len(rows))
	for _, r := range rows {
		lines, err := s.store.StatementLines(ctx, r.CustomerID, asOf, reach)
		if err != nil {
			_ = s.store.FinishRun(ctx, runID, "FAILED", generated, queued, err.Error())
			return StatementRun{}, nil, err
		}
		st := Statement{
			RunID: runID, CustomerID: r.CustomerID, CustomerName: r.CustomerName, AsOfDate: asOf,
			CurrentCents: r.CurrentCents, Days30Cents: r.Days30Cents, Days60Cents: r.Days60Cents,
			Days90PlusCents: r.Days90PlusCents, ClosingCents: r.TotalCents,
			DeliveryStatus: DeliveryNotQueued,
		}
		st.HTML = RenderStatementHTML(header, r, lines, asOf)

		// Only a positive balance is worth sending. See the header for why the others are still
		// generated rather than skipped.
		var notice *StatementNotice
		if r.TotalCents > 0 {
			st.DeliveryStatus = DeliveryQueued
			notice = &StatementNotice{CustomerID: r.CustomerID, AsOfDate: asOf.Format("2006-01-02"), BalanceCents: r.TotalCents}
		}
		id, err := s.store.SaveStatement(ctx, runID, st, notice, s.outbox)
		if err != nil {
			_ = s.store.FinishRun(ctx, runID, "FAILED", generated, queued, err.Error())
			return StatementRun{}, nil, err
		}
		st.ID = id
		generated++
		if notice != nil {
			queued++
		}
		out = append(out, st)
	}

	if err := s.store.FinishRun(ctx, runID, "COMPLETED", generated, queued, ""); err != nil {
		return StatementRun{}, nil, err
	}

	// 0148's notice accounting, stamped once at the end of the run.
	//
	// The split is decided by the SEAM, not by the environment: s.outbox is the binding that actually
	// governed this run's behaviour, and reading a flag here instead would let a run's record disagree
	// with what the run did. And because a bound outbox FAILS the whole run on an enqueue error (see
	// statement_outbox.go, a run that silently half-notified is worse than one that fails and is
	// re-run), reaching this line means every sendable statement was in fact enqueued. So the split is
	// total, in both directions, and the 0148 invariant holds by construction.
	noticesEnabled := s.outbox != nil
	noticeQueued, noticeSuppressed := 0, queued
	if noticesEnabled {
		noticeQueued, noticeSuppressed = queued, 0
	}
	if acct, ok := s.store.(statementNoticeAccountant); ok {
		// NON-FATAL, and the reason it is safe to be non-fatal is the invariant.
		//
		// The run is already COMPLETED, the statements exist, the print jobs exist and every customer's
		// document is correct. Failing the close over a reporting column would be a metadata detail
		// taking down a month end, and re-running is not a cheap remedy here, because a second run
		// generates a second set of statements.
		//
		// Swallowing it is only defensible because the loss is DETECTABLE rather than silent: the
		// columns default to (false, 0, 0), so a run whose accounting did not record shows
		// notice_queued + notice_suppressed = 0 against a non-zero queued_count, which violates the
		// 0148 invariant and is exactly what the run-detail surface checks before it shows a number.
		// An operator sees "not recorded", never a confident wrong zero.
		_ = acct.RecordRunNoticeCounts(ctx, runID, noticesEnabled, noticeQueued, noticeSuppressed)
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActStatementRun, EntityType: "ar_statement_run", EntityID: runID,
			ActorID: actorID, BranchID: branchID, Purpose: mode,
			Extra: map[string]any{"as_of": asOf.Format("2006-01-02"), "generated": generated, "queued": queued},
		})
	}
	run, statements, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return StatementRun{}, out, nil
	}
	return run, statements, nil
}

// GetRun reads a completed run with its statements. A caller who does not reach every branch reads
// only a run filed under a branch it reaches: a run of the whole book is not found for it.
func (s *StatementService) GetRun(ctx context.Context, runID string) (StatementRun, []Statement, error) {
	run, sts, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return StatementRun{}, nil, err
	}
	if reach := reachFromContext(ctx); !reach.Global && (run.BranchID == "" || !reach.Allows(run.BranchID)) {
		return StatementRun{}, nil, ErrNotFound
	}
	return run, sts, nil
}

// LatestStatement returns the most recent statement for an account (the portal's "your statement")
// that the caller's branch reach may read: for a caller who does not reach every branch, the newest
// statement of a run filed under a branch it reaches. A statement made for the whole book carries the
// whole account's lines and is not its to read.
func (s *StatementService) LatestStatement(ctx context.Context, customerID string) (Statement, bool, error) {
	return s.store.LatestStatementIn(ctx, customerID, reachFromContext(ctx))
}

// -- rendering -----------------------------------------------------------------------------------

// RenderStatementHTML renders one self-contained statement.
//
// SELF-CONTAINED IS THE REQUIREMENT: inline styles, no external stylesheet, no images, no fonts. This
// document has to render identically in a print dialog today, in an email body next week, and out of
// an archive in five years when the CSS file it might have referenced is long gone. Every external
// reference is a way for a stored artefact to stop looking like what we sent.
//
// Everything customer-supplied is escaped. A customer name is attacker-controllable in exactly the
// same way a product description is, and this HTML is stored and later re-served.
func RenderStatementHTML(h StatementHeader, r AgingRow, lines []StatementLine, asOf time.Time) string {
	esc := html.EscapeString
	var b strings.Builder
	b.Grow(2048 + len(lines)*160)

	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<title>Statement, ` + esc(r.CustomerName) + `</title></head>`)
	b.WriteString(`<body style="font:14px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:#1a1a1a;max-width:760px;margin:0 auto;padding:32px">`)

	// Letterhead
	b.WriteString(`<header style="display:flex;justify-content:space-between;align-items:flex-start;border-bottom:2px solid #1a1a1a;padding-bottom:16px;margin-bottom:24px">`)
	b.WriteString(`<div><h1 style="margin:0;font-size:20px">` + esc(h.BranchName) + `</h1>`)
	if h.Address != "" {
		b.WriteString(`<div style="color:#555;font-size:12px">` + esc(h.Address) + `</div>`)
	}
	if h.Phone != "" {
		b.WriteString(`<div style="color:#555;font-size:12px">` + esc(h.Phone) + `</div>`)
	}
	b.WriteString(`</div><div style="text-align:right"><h2 style="margin:0;font-size:18px">STATEMENT</h2>`)
	b.WriteString(`<div style="color:#555;font-size:12px">As of ` + asOf.Format("2 January 2006") + `</div></div></header>`)

	// Account block
	b.WriteString(`<section style="margin-bottom:24px"><div style="font-weight:600">` + esc(r.CustomerName) + `</div>`)
	b.WriteString(`<div style="color:#555;font-size:12px">Account ` + esc(r.AccountNo) + `</div></section>`)

	// Activity
	b.WriteString(`<table style="width:100%;border-collapse:collapse;margin-bottom:24px"><thead><tr style="text-align:left;border-bottom:1px solid #ccc">`)
	for _, th := range []string{"Date", "Document", "Description", "Charges", "Payments", "Balance"} {
		align := "left"
		if th == "Charges" || th == "Payments" || th == "Balance" {
			align = "right"
		}
		b.WriteString(`<th style="padding:6px 4px;font-size:12px;text-align:` + align + `">` + th + `</th>`)
	}
	b.WriteString(`</tr></thead><tbody>`)
	if len(lines) == 0 {
		b.WriteString(`<tr><td colspan="6" style="padding:12px 4px;color:#555">No activity in this period.</td></tr>`)
	}
	for _, ln := range lines {
		b.WriteString(`<tr style="border-bottom:1px solid #eee">`)
		b.WriteString(`<td style="padding:6px 4px">` + ln.Date.Format("2006-01-02") + `</td>`)
		b.WriteString(`<td style="padding:6px 4px">` + esc(ln.DocNo) + `</td>`)
		b.WriteString(`<td style="padding:6px 4px">` + esc(ln.Description) + `</td>`)
		b.WriteString(`<td style="padding:6px 4px;text-align:right">` + moneyOrBlank(ln.ChargeCents) + `</td>`)
		b.WriteString(`<td style="padding:6px 4px;text-align:right">` + moneyOrBlank(ln.CreditCents) + `</td>`)
		b.WriteString(`<td style="padding:6px 4px;text-align:right">` + Money(ln.BalanceRun) + `</td>`)
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)

	// Aging summary, the part the office actually reads.
	b.WriteString(`<table style="width:100%;border-collapse:collapse;background:#f6f6f6"><thead><tr>`)
	for _, th := range []string{"Current", "1-30 days", "31-60 days", "60+ days", "Total due"} {
		b.WriteString(`<th style="padding:8px 4px;font-size:12px;text-align:right;border-bottom:1px solid #ccc">` + th + `</th>`)
	}
	b.WriteString(`</tr></thead><tbody><tr>`)
	for _, v := range []int64{r.CurrentCents, r.Days30Cents, r.Days60Cents, r.Days90PlusCents} {
		b.WriteString(`<td style="padding:10px 4px;text-align:right">` + Money(v) + `</td>`)
	}
	b.WriteString(`<td style="padding:10px 4px;text-align:right;font-weight:700">` + Money(r.TotalCents) + `</td>`)
	b.WriteString(`</tr></tbody></table>`)

	if r.Days90PlusCents > 0 {
		b.WriteString(`<p style="margin-top:20px;padding:12px;background:#fff4f4;border-left:3px solid #b00">` +
			`<strong>` + Money(r.Days90PlusCents) + `</strong> of this balance is more than 60 days past due. ` +
			`Please contact our office to arrange payment.</p>`)
	}
	b.WriteString(`<footer style="margin-top:32px;color:#777;font-size:11px">Statement generated ` +
		asOf.Format("2006-01-02") + `. Please contact the branch with any discrepancy.</footer>`)
	b.WriteString(`</body></html>`)
	return b.String()
}

// moneyOrBlank renders a zero as an empty cell. A statement column full of "$0.00" is noise that
// makes the numbers that matter harder to find.
func moneyOrBlank(cents int64) string {
	if cents == 0 {
		return ""
	}
	return Money(cents)
}
