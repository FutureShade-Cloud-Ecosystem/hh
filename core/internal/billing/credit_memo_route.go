package billing

// credit_memo_route.go: THE ROUTE THAT ISSUES A CREDIT MEMO, for finance.
//
// -- WHAT THIS IS, IN ONE PARAGRAPH ------------------------------------------------------------------
// A credit memo is money leaving the company. Until this route existed the only way to raise one was
// the warranty adapter (core/cmd/core/tops_adapters.go:1183), so a credit a manager agreed to give a
// customer had no door of its own: the only records of it were a customer's good memory and the
// absence of a document. The audit called this out as AR-05, "No waiver or reversal of a posted
// finance charge", with the evidence "credit_memo.go:201 has one caller".
//
// -- THE RULES, AND WHY EACH IS HERE -----------------------------------------------------------------
//  1. MANAGER OR CONTROLLER ONLY. A credit is a management act, not a counter act. It is money the
//     dealer chose to give back, and the person who gives it away should be somebody who can be asked
//     about it. RequireManager answers exactly that: a tier 1 or tier 2 role passes, and the Financial
//     Controller is tier 1, so "manager or Controller" is one gate and not two.
//  2. A REASON, ALWAYS. A credit with no stated reason is a number that cannot be explained to the
//     customer, to the Controller, or to whoever reads the file in a year. The reason rides the memo's
//     own description line, the AR subledger row's memo, and the audit row, so there is one phrase and
//     it appears everywhere it is read.
//  3. A GL ACCOUNT THE POSTING RULES ALLOW. The credit books against the revenue account the posting
//     rules resolve for that branch (gl's posting_rules, branch override first). The caller may name
//     the account, and when they do it must be the one the rules allow, because a hard-coded account
//     in a request body is a second, stale copy of a configurable fact, and a credit that lands in an
//     account nobody chose is worse than no credit at all.
//  4. IDEMPOTENT BY KEY. A retry that mints a second credit is a real loss, not an untidy table. The
//     writer's own rule (billing/credit_memo.go, an empty key is refused) stands behind this one, and
//     the route derives a key when the caller supplies none, so a double click on the Money Desk
//     button raises ONE credit.
//
// -- THE APPROVAL RAIL ABOVE THE THRESHOLD ----------------------------------------------------------
// gl_policy.je_approval_threshold_cents is the branch's maker-checker threshold, and gl's own rule is
// the one this route copies: BELOW the threshold the entry posts, AT OR ABOVE it a second person has
// to sign it (gl/journal.go, CreateJournal).
//
// A parked credit memo moves NO money at all. The memo document is written as a DRAFT (the AR balance
// only counts FINALIZED and PARTIALLY_PAID rows), its reversing journal is written as a DRAFT journal,
// and an approval request is filed against that parked journal in the approvals queue that already
// exists (permission_approval_requests, the same queue and the same JE_OVER_THRESHOLD policy the
// ledger's own parked journals use). The memo document, the parked journal and the approval request
// are written in ONE transaction, so a half-parked credit cannot exist.
//
// When the Controller approves the parked journal, the memo document is finalized and the journal is
// posted on that decision's own transaction, so the credit reaches the subledger and the ledger
// together or not at all.
//
// The threshold of ZERO means NO RAIL, not "park everything": gl's derivedStatus reads a zero
// threshold as "no approval rail at all" (gl/journal.go), and this route reads it the same way, so an
// unconfigured branch credits the way it did yesterday instead of freezing on a number nobody set.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/moneyutil"
)

// -- the seams --------------------------------------------------------------------------------------

// CreditMemoJournalKeyPrefix marks the reversing journal of a credit memo.
//
// It is the one thing the composition root can read to tell a parked credit memo's journal from any
// other journal: the key is the memo's own, so the prefix and the memo's id travel together. The
// approvals rail hands the decider a journal id and nothing else, so without this the approve half
// would not know it had a memo to finalize.
const CreditMemoJournalKeyPrefix = "ar.credit_memo.issued:"

// CreditMemoThresholdReader reads the branch's maker-checker threshold. It is gl's policy read
// (gl_policy.je_approval_threshold_cents, the org default with a branch override), bound by the
// composition root. Nil means NO RAIL: every credit posts, which is what a deployment that has not
// wired the policy yet should do, and the opposite of silently parking everything.
type CreditMemoThresholdReader interface {
	CreditMemoThresholdCents(ctx context.Context, branchID string) (int64, error)
}

// CreditMemoPostingRules resolves the accounts a credit memo may book against at a branch, keyed by
// the roles billing names (ar, revenue, tax). It is gl's posting_rules read, bound by the composition
// root. Nil is refused: a credit with no account is not a credit.
type CreditMemoPostingRules interface {
	CreditMemoAccounts(ctx context.Context, branchID string) (map[string]string, error)
}

// CreditMemoBranchOrg is the branches module's own read: does this branch belong to this org. The
// fence holds the filing branch and the customer's home branch to the caller's org through it, so no
// billing SQL names the locations table.
type CreditMemoBranchOrg interface {
	BranchInOrg(ctx context.Context, orgID, branchID string) (bool, error)
}

// CreditMemoCustomerHome is the customer module's own read: the account's home branch, and whether
// the account exists. The org the account belongs to is its home branch's, through the branches read
// above; no billing SQL names the customers table.
//
// The read is asked for the account's home branch only, and the caller holds it to their own reach
// (THE CUSTOMER WALL, below). The account itself is read by the customer module's own by-id read, the
// one the pos and AR doors use.
type CreditMemoCustomerHome interface {
	CustomerHomeBranch(ctx context.Context, customerID string) (string, bool, error)
}

// CreditMemoParkRequest is everything the park needs, already decided by billing.
type CreditMemoParkRequest struct {
	// Memo is the writer's own input, so the parked document is written by the same code that
	// writes a posted one.
	Memo CreditMemoInput
	// AmountCents is the credit's total, the figure the approver reads.
	AmountCents int64
	// RequestedBy is the person who asked for the credit. They cannot approve it: four eyes.
	RequestedBy string
	// Reason is the phrase the approver reads.
	Reason string
}

// CreditMemoParked names what a park produced.
type CreditMemoParked struct {
	CreditMemoID string
	CreditMemoNo string
	JournalID    string
	ApprovalID   string
}

// CreditMemoParker holds an over-threshold credit memo: the memo document, its parked reversing
// journal and the approval request, in ONE transaction. The composition root implements it over the
// three modules that own those rows, because no module may write another's table.
type CreditMemoParker interface {
	ParkCreditMemo(ctx context.Context, in CreditMemoParkRequest) (CreditMemoParked, error)
}

// -- the request and the answer ----------------------------------------------------------------------

// CreditMemoRouteRequest is the body of POST /api/v1/ar/credit-memos.
//
// AmountCents is a POSITIVE magnitude; the sign is billing's business, never the caller's (the same
// rule billing/credit_memo.go states). Against an invoice, zero means "the whole of what is left".
type CreditMemoRouteRequest struct {
	CustomerID string `json:"customer_id"`
	BranchID   string `json:"branch_id"`
	// InvoiceID names the one invoice this credit is against. Empty is a STANDALONE credit: the
	// customer gets credit on the account and it is not tied to a document.
	InvoiceID   string `json:"invoice_id"`
	AmountCents int64  `json:"amount_cents"`
	Reason      string `json:"reason"`
	// GLAccount is the account the credit books against. Empty means the posting rules' own revenue
	// account for that branch; naming one that the rules do not allow is refused.
	GLAccount string `json:"gl_account"`
	// IdempotencyKey makes a retry safe. Empty means the route derives one from the request, so a
	// double click raises one credit and not two.
	IdempotencyKey string `json:"idempotency_key"`
	// JobID is the job account the credit is opened on (JA-2), optional. Another customer's job, a
	// CLOSED job and a job outside the caller's reach are refused in words before anything moves.
	JobID string `json:"job_id"`
}

// The states a credit can be in when the route answers.
const (
	// CreditMemoIssued means the credit is a document and its journal is posted.
	CreditMemoIssued = "ISSUED"
	// CreditMemoAwaitingApproval means the credit is parked: no money has moved and a second
	// person has to sign it.
	CreditMemoAwaitingApproval = "AWAITING_APPROVAL"
)

// CreditMemoRouteResult is what the route answers with.
type CreditMemoRouteResult struct {
	Status        string `json:"status"`
	CreditMemoID  string `json:"credit_memo_id"`
	CreditMemoNo  string `json:"credit_memo_no"`
	CustomerID    string `json:"customer_id"`
	BranchID      string `json:"branch_id"`
	InvoiceID     string `json:"invoice_id,omitempty"`
	SubtotalCents int64  `json:"subtotal_cents"`
	TaxCents      int64  `json:"tax_cents"`
	TotalCents    int64  `json:"total_cents"`
	GLAccount     string `json:"gl_account"`
	Reason        string `json:"reason"`
	// ApprovalID names the approval request when the credit is parked.
	ApprovalID string `json:"approval_id,omitempty"`
	// GLJournalID is the journal that carries the credit into the ledger. It is empty while the
	// credit is parked, and the message says so in words.
	GLJournalID string `json:"gl_journal_id,omitempty"`
	// Reused is true when the idempotency key named a credit that already exists.
	Reused  bool   `json:"reused"`
	Message string `json:"message"`
}

// -- the service -------------------------------------------------------------------------------------

// ActCreditMemoIssue is the audit verb for a credit memo this route raises. audit_log has no CHECK on
// action, so this is free to add, and it is a constant so a typo cannot split one act into two in the
// trail.
const ActCreditMemoIssue = "CREDIT_MEMO_ISSUE"

// CreditMemoJobFence is the customer module's job read a direct document is opened against (JA-2):
// the money desk's credit names a job of the customer it credits, and the customer module refuses
// another customer's job, a CLOSED job and a job outside the caller's reach, in words. The same
// narrow-seam shape as CreditMemoCustomerHome above it.
type CreditMemoJobFence interface {
	JobForNewDocument(ctx context.Context, customerID, jobID string) error
}

// CreditMemoService issues credit memos for finance.
type CreditMemoService struct {
	w            *InvoiceWriter
	threshold    CreditMemoThresholdReader
	rules        CreditMemoPostingRules
	parker       CreditMemoParker
	branchOrgs   CreditMemoBranchOrg
	customerHoms CreditMemoCustomerHome
	jobFence     CreditMemoJobFence
	audit        audit.Recorder
	log          *slog.Logger
	now          func() time.Time
}

// NewCreditMemoService builds the service over the invoice writer, which owns the pool, the tax
// assessor, the poster and the clock the writer's own path uses.
func NewCreditMemoService(w *InvoiceWriter, log *slog.Logger) *CreditMemoService {
	if log == nil {
		log = slog.Default()
	}
	return &CreditMemoService{w: w, log: log, now: time.Now}
}

// WithThreshold binds the threshold reader. Nil means no rail: every credit posts.
func (s *CreditMemoService) WithThreshold(r CreditMemoThresholdReader) *CreditMemoService {
	s.threshold = r
	return s
}

// WithPostingRules binds the posting-rules reader. Nil is refused at the door.
func (s *CreditMemoService) WithPostingRules(r CreditMemoPostingRules) *CreditMemoService {
	s.rules = r
	return s
}

// WithParker binds the park seam. Nil means an over-threshold credit cannot be parked and is
// refused, never issued without its approval.
func (s *CreditMemoService) WithParker(p CreditMemoParker) *CreditMemoService {
	s.parker = p
	return s
}

// WithBranchOrg binds the branches module's org read. Nil is refused at the door: the fence fails
// closed, it never guesses an org.
func (s *CreditMemoService) WithBranchOrg(r CreditMemoBranchOrg) *CreditMemoService {
	s.branchOrgs = r
	return s
}

// WithJobFence binds the customer module's job fence. Nil is refused at the door when a job is
// named, the same fail-closed rule the wire's other optional seams carry.
func (s *CreditMemoService) WithJobFence(f CreditMemoJobFence) *CreditMemoService {
	s.jobFence = f
	return s
}

// WithCustomerHome binds the customer module's home-branch read. Nil is refused at the door.
func (s *CreditMemoService) WithCustomerHome(r CreditMemoCustomerHome) *CreditMemoService {
	s.customerHoms = r
	return s
}

// WithAudit binds the recorder. Every credit this route raises is stamped.
func (s *CreditMemoService) WithAudit(r audit.Recorder) *CreditMemoService {
	s.audit = r
	return s
}

// WithClock overrides the clock (tests).
func (s *CreditMemoService) WithClock(now func() time.Time) *CreditMemoService {
	if now != nil {
		s.now = now
	}
	return s
}

// CreditMemoCaller is the fence's view of the caller, exactly what the guard resolved: the org the
// request is signed into (authz.StaffScope), and the resolved authority's scope and branch grants
// (identity.StaffAuthority). It arrives as an argument, never from a token or the context, the same
// rule the guards state for the handler itself.
type CreditMemoCaller struct {
	OrgID    string
	Scope    identity.Scope
	Branches map[string]bool
}

// reaches says whether the caller holds the branch: a GLOBAL caller reaches every branch it can name,
// a branch scoped caller only its grants.
func (c CreditMemoCaller) reaches(branchID string) bool {
	if c.Scope == identity.ScopeGlobal {
		return true
	}
	return c.Branches[branchID]
}

// creditFence is what the fence resolved: the account to credit, and the branch to file at.
type creditFence struct {
	customerID string
	branchID   string
	// jobID is the fenced job the credit is opened on (JA-2), "" when none was named.
	jobID string
}

// fence holds a credit to the caller's org, to the caller's reach, and to the account the caller may
// credit. It is THE FENCE for both credit doors, the quote and the issue, in the same order, so a
// quote and the credit that follows it are fenced alike and neither is an existence oracle.
//
// The order of the checks is the order a reader should expect: the account, the branch, the amount,
// the org, the reach, and only then the money.
func (s *CreditMemoService) fence(ctx context.Context, req CreditMemoRouteRequest, caller CreditMemoCaller) (creditFence, error) {
	if s.w == nil || s.w.pool == nil {
		return creditFence{}, errNoDB
	}
	customerID := strings.TrimSpace(req.CustomerID)
	if customerID == "" {
		return creditFence{}, httpx.BadRequestf("a credit needs a customer")
	}
	branchID := strings.TrimSpace(req.BranchID)
	if branchID == "" {
		return creditFence{}, httpx.BadRequestf("a credit needs a branch")
	}
	if req.AmountCents < 0 {
		return creditFence{}, httpx.BadRequestf("a credit amount is a positive magnitude; the sign is billing's")
	}
	// THE FENCE. A credit is filed at a branch the caller reaches, in the org the caller is signed
	// into; a GLOBAL caller is bounded to its org (UX-81). Fail closed on a caller with no org at all,
	// and on a seam that is not wired: the fence never guesses an org.
	if caller.OrgID == "" {
		return creditFence{}, httpx.BadRequestf("the caller's organization is missing, so no credit can be issued")
	}
	if s.branchOrgs == nil || s.customerHoms == nil {
		return creditFence{}, httpx.BadRequestf("the credit's organization fence is not wired, so no credit can be issued")
	}
	if !caller.reaches(branchID) {
		return creditFence{}, httpx.BadRequestf("that branch is not yours to credit")
	}
	// Against an invoice, the invoice names the facts (billing's own table): a body branch that
	// differs from the invoice's is refused, and an invoice at a branch the caller does not reach, or
	// of another org, answers exactly as a missing invoice answers, so a caller cannot read a ledger's
	// shape out of the refusals. A missing row falls through to the plan, which refuses in the same
	// words.
	reversedJob := ""
	if invoiceID := strings.TrimSpace(req.InvoiceID); invoiceID != "" {
		var invBranch, invOrg *string
		if err := s.w.pool.QueryRow(ctx,
			`SELECT branch_id::text, org_id::text, COALESCE(job_id::text, '') FROM invoices WHERE id = $1::uuid AND doc_type = 'INVOICE'`,
			invoiceID).Scan(&invBranch, &invOrg, &reversedJob); err == nil {
			if invOrg == nil || *invOrg != caller.OrgID || !caller.reaches(*invBranch) {
				return creditFence{}, httpx.BadRequestf("no such invoice to reverse")
			}
			if branchID != *invBranch {
				return creditFence{}, httpx.BadRequestf("a credit against an invoice is filed at the invoice's branch")
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return creditFence{}, err
		}
	}
	// The branch and the customer are held to the caller's org through the owning modules' own reads:
	// the branches module answers whether the filing branch is the org's, and the customer module
	// answers the account's home branch, whose org is the account's.
	inOrg, err := s.branchOrgs.BranchInOrg(ctx, caller.OrgID, branchID)
	if err != nil {
		return creditFence{}, err
	}
	if !inOrg {
		return creditFence{}, httpx.BadRequestf("that branch is not in your organization")
	}
	// THE CUSTOMER WALL (UX-89). The account is read through the customer module's own seam and held
	// to the caller's reach the way every other customer door holds it: the account's home branch
	// must be a branch the caller reaches, not only a branch of the caller's org. An account homed
	// outside the caller's reach, an account of another org, and an id that is no account answer
	// EXACTLY alike, 404 "customer not found", so this door is no existence oracle for another
	// branch's accounts. The words are the house's: the answer never says which of the three it was.
	//
	// The id is parsed BEFORE the seam reads it: a malformed id can never be an account, so it
	// answers as one that is not there instead of reaching the database as a cast error.
	if _, perr := uuid.Parse(customerID); perr != nil {
		return creditFence{}, errCreditMemoCustomerNotFound
	}
	home, found, err := s.customerHoms.CustomerHomeBranch(ctx, customerID)
	if err != nil {
		return creditFence{}, err
	}
	if !found {
		return creditFence{}, errCreditMemoCustomerNotFound
	}
	if home == "" {
		// Fail closed, in words: an account with no home branch has no org to be checked against, and
		// a credit with no fence is not a credit. (Such accounts are cutover loads; they cannot be
		// credited until they carry a home branch.)
		return creditFence{}, httpx.BadRequestf("that customer has no home branch, so its organization cannot be checked")
	}
	if !caller.reaches(home) {
		return creditFence{}, errCreditMemoCustomerNotFound
	}
	custInOrg, err := s.branchOrgs.BranchInOrg(ctx, caller.OrgID, home)
	if err != nil {
		return creditFence{}, err
	}
	if !custInOrg {
		return creditFence{}, errCreditMemoCustomerNotFound
	}
	// JA-2: the job the credit is opened on, fenced last so the three job refusals answer with
	// the caller already held to the account. The id is parsed before the seam reads it, the same
	// rule the customer wall states above it.
	//
	// A REVERSAL is a mirror, not a fresh document (the review of PR 747, P2-2): the memo that
	// reverses an invoice on a job is on THAT job, so by job the account nets to zero. The request
	// naming no job inherits the invoice's job; one naming the same job is the same thing; one naming a
	// different job is refused in words. Neither of those two goes through the open rule, because
	// crediting a closed job's invoice is ordinary. Only a job newly named on an invoice that has none
	// is a fresh choice, and that one is fenced as any new document's job is.
	jobID := strings.ToLower(strings.TrimSpace(req.JobID))
	if jobID != "" {
		if _, perr := uuid.Parse(jobID); perr != nil {
			return creditFence{}, httpx.BadRequestf("job_id must be a job id, pick a job from the customer's jobs")
		}
	}
	if reversedJob != "" {
		if jobID != "" && jobID != reversedJob {
			return creditFence{}, httpx.BadRequestf("that credit reverses an invoice on another job, so it takes that job; leave the job out or name the invoice's own job")
		}
		return creditFence{customerID: customerID, branchID: branchID, jobID: reversedJob}, nil
	}
	if jobID != "" {
		if s.jobFence == nil {
			return creditFence{}, httpx.BadRequestf("the job fence is not wired, so no credit can name a job here")
		}
		if err := s.jobFence.JobForNewDocument(ctx, customerID, jobID); err != nil {
			return creditFence{}, err
		}
	}
	return creditFence{customerID: customerID, branchID: branchID, jobID: jobID}, nil
}

// CreditMemoQuoteResult is what a credit against an invoice would come to, and how much room that
// invoice has left to credit. The figures are the core's: the desk asks, the core answers, and the
// desk never works out a tax.
//
// The room is the cap the core holds the amount to (SF-2e): what the invoice has left to credit
// BEFORE tax, and the same room WITH tax. The credit is what the amount comes to: before tax, the
// tax that is pro-rated from the room, and the total the customer is credited. A paid invoice still
// has a room: what has been paid is not what has been credited.
type CreditMemoQuoteResult struct {
	InvoiceID              string `json:"invoice_id"`
	RemainingSubtotalCents int64  `json:"remaining_subtotal_cents"`
	RemainingTaxCents      int64  `json:"remaining_tax_cents"`
	RemainingTotalCents    int64  `json:"remaining_total_cents"`
	SubtotalCents          int64  `json:"subtotal_cents"`
	TaxCents               int64  `json:"tax_cents"`
	TotalCents             int64  `json:"total_cents"`
	Message                string `json:"message"`
}

// Quote answers what a credit against an invoice would come to, and how much room that invoice has
// left, WITHOUT raising anything: no document, no journal, no audit row, no approval.
//
// It is the same fence and the same plan the issue door uses, so a quote and the credit that follows
// it are the same credit. The amount over the room is refused in the plan's own words, which name
// both figures ("$X before tax ($Y with tax) is left"), so the reader learns the cap from the core
// and not from a second rule somewhere else.
func (s *CreditMemoService) Quote(ctx context.Context, req CreditMemoRouteRequest, caller CreditMemoCaller) (CreditMemoQuoteResult, error) {
	if s.w == nil || s.w.pool == nil {
		return CreditMemoQuoteResult{}, errNoDB
	}
	f, err := s.fence(ctx, req, caller)
	if err != nil {
		return CreditMemoQuoteResult{}, err
	}
	invoiceID := strings.TrimSpace(req.InvoiceID)
	if invoiceID == "" {
		// A quote is about a room, and a standalone credit has no room: its tax is re-assessed, not
		// pro-rated from what an invoice has left.
		return CreditMemoQuoteResult{}, httpx.BadRequestf("a quote is against one invoice: name the invoice")
	}
	plan, err := s.w.planCreditMemo(ctx, s.w.pool, CreditMemoInput{
		CustomerID:        f.customerID,
		BranchID:          f.branchID,
		ReversesInvoiceID: invoiceID,
		AmountCents:       req.AmountCents,
	})
	if err != nil {
		return CreditMemoQuoteResult{}, err
	}
	return CreditMemoQuoteResult{
		InvoiceID:              invoiceID,
		RemainingSubtotalCents: plan.roomSubtotal,
		RemainingTaxCents:      plan.roomTax,
		RemainingTotalCents:    plan.roomSubtotal + plan.roomTax,
		SubtotalCents:          plan.subtotal,
		TaxCents:               plan.tax,
		TotalCents:             plan.subtotal + plan.tax,
		Message:                creditQuoteWords(plan.subtotal, plan.tax),
	}, nil
}

// creditQuoteWords is what a person reads under the amount they are typing: what the credit comes
// to, tax included.
func creditQuoteWords(subtotal, tax int64) string {
	return "This credit of " + Money(subtotal) + " comes to " + Money(subtotal+tax) + " with tax."
}

// Issue raises a credit memo: standalone against the account, or against one invoice, capped at what
// that invoice has left (the SF-2e cap the writer enforces).
//
// The order of the checks matters and is the order a reader should expect: the reason, the customer,
// the branch, the amount, the account, and only then the money. A caller who sent a request with no
// reason learns that before anything else, because a reason is the one field whose absence makes the
// credit unexplainable afterwards.
func (s *CreditMemoService) Issue(ctx context.Context, req CreditMemoRouteRequest, caller CreditMemoCaller) (CreditMemoRouteResult, error) {
	if s.w == nil || s.w.pool == nil {
		return CreditMemoRouteResult{}, errNoDB
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return CreditMemoRouteResult{}, httpx.BadRequestf("a credit needs a reason")
	}
	f, err := s.fence(ctx, req, caller)
	if err != nil {
		return CreditMemoRouteResult{}, err
	}
	customerID, branchID := f.customerID, f.branchID
	if s.rules == nil {
		return CreditMemoRouteResult{}, httpx.BadRequestf("the posting rules are not wired; no credit can be booked")
	}
	accounts, err := s.rules.CreditMemoAccounts(ctx, branchID)
	if err != nil {
		return CreditMemoRouteResult{}, err
	}
	revenue := accounts[RoleRevenue]
	if revenue == "" {
		// Refused, not guessed. A credit posted to an account nobody chose is worse than no credit:
		// the books look complete and are wrong.
		return CreditMemoRouteResult{}, httpx.BadRequestf("the posting rules for this branch have no revenue account, so a credit cannot be booked")
	}
	named := strings.TrimSpace(req.GLAccount)
	if named != "" && named != revenue {
		return CreditMemoRouteResult{}, httpx.BadRequestf("the posting rules for this branch credit account %s, not %s", revenue, named)
	}

	in := CreditMemoInput{
		CustomerID:        customerID,
		BranchID:          branchID,
		ReversesInvoiceID: strings.TrimSpace(req.InvoiceID),
		AmountCents:       req.AmountCents,
		Memo:              reason,
		IdempotencyKey:    s.creditMemoKey(ctx, req, reason, f.jobID),
		SourceKind:        SourceOrder,
		JobID:             f.jobID,
	}

	// THE IDEMPOTENCY KEY, CHECKED FIRST AND BOUND TO ITS REQUEST. A retry is answered with the credit
	// the key already named, before anything else is read, because the plan's own cap would otherwise
	// refuse the retry: the first attempt already took the room the second was going to plan against.
	// But the answer comes only when the body is the same credit (the customer, the branch, the
	// invoice and the amount); a key reused with a different body is a 409 conflict, never somebody
	// else's credit read back as issued. The lookup is bounded to the caller's org, so a key from
	// another org's memo discloses nothing. (A zero amount means the whole of what is left, so it is
	// answered with whatever credit the key named.)
	existing, found, err := s.w.creditMemoByKey(ctx, in.IdempotencyKey, caller.OrgID)
	if err != nil {
		return CreditMemoRouteResult{}, err
	}
	if found {
		if existing.CustomerID != customerID ||
			existing.BranchID != branchID ||
			existing.ReversesInvoiceID != in.ReversesInvoiceID ||
			existing.jobID != in.JobID ||
			(req.AmountCents != 0 && -existing.SubtotalCents != req.AmountCents) {
			return CreditMemoRouteResult{}, &creditMemoKeyConflict{msg: "this idempotency key already named a different credit; a key belongs to the credit that first used it"}
		}
		return s.reusedResult(existing, revenue, reason), nil
	}

	// The plan is resolved BEFORE the threshold is read, because the threshold is read against the
	// credit's TOTAL, tax included, and the total is what the plan holds.
	plan, err := s.w.planCreditMemo(ctx, s.w.pool, in)
	if err != nil {
		return CreditMemoRouteResult{}, err
	}
	if plan.subtotal == 0 && plan.tax == 0 {
		return CreditMemoRouteResult{}, ErrNothingToInvoice
	}
	total := plan.subtotal + plan.tax

	// THE THRESHOLD. Read the branch's own row, the org default under it, exactly as gl reads it.
	threshold := int64(0)
	if s.threshold != nil {
		threshold, err = s.threshold.CreditMemoThresholdCents(ctx, branchID)
		if err != nil {
			return CreditMemoRouteResult{}, err
		}
	}
	if threshold > 0 && total >= threshold {
		return s.park(ctx, in, plan, accounts, total, reason)
	}

	doc, err := s.w.IssueCreditMemo(ctx, in)
	if err != nil {
		return CreditMemoRouteResult{}, err
	}
	s.record(ctx, doc, in, total, revenue, reason)
	return CreditMemoRouteResult{
		Status:        CreditMemoIssued,
		CreditMemoID:  doc.InvoiceID,
		CreditMemoNo:  doc.InvoiceNo,
		CustomerID:    doc.CustomerID,
		BranchID:      doc.BranchID,
		InvoiceID:     doc.ReversesInvoiceID,
		SubtotalCents: -plan.subtotal,
		TaxCents:      -plan.tax,
		TotalCents:    -total,
		GLAccount:     revenue,
		Reason:        reason,
		GLJournalID:   doc.GLJournalID,
		Reused:        doc.Reused,
		Message:       creditIssuedWords(doc.InvoiceNo, total),
	}, nil
}

// park holds an over-threshold credit: the memo document, the parked reversing journal and the
// approval request, all in one transaction, and NOTHING that moves money.
func (s *CreditMemoService) park(ctx context.Context, in CreditMemoInput, plan creditPlan, accounts map[string]string, total int64, reason string) (CreditMemoRouteResult, error) {
	if s.parker == nil {
		// Fail closed. An over-threshold credit with nowhere to park is NOT a credit, and issuing
		// it anyway would move money past the rail the dealer set.
		return CreditMemoRouteResult{}, httpx.BadRequestf("this credit is over the approval threshold and the approval rail is not wired, so nothing was issued")
	}
	parked, err := s.parker.ParkCreditMemo(ctx, CreditMemoParkRequest{
		Memo:        in,
		AmountCents: total,
		RequestedBy: identity.StaffUserIDFromContext(ctx),
		Reason:      reason,
	})
	if err != nil {
		return CreditMemoRouteResult{}, err
	}
	s.record(ctx, InvoiceDoc{InvoiceID: parked.CreditMemoID, InvoiceNo: parked.CreditMemoNo, CustomerID: in.CustomerID, BranchID: in.BranchID}, in, total, accounts[RoleRevenue], reason)
	return CreditMemoRouteResult{
		Status:        CreditMemoAwaitingApproval,
		CreditMemoID:  parked.CreditMemoID,
		CreditMemoNo:  parked.CreditMemoNo,
		CustomerID:    in.CustomerID,
		BranchID:      in.BranchID,
		InvoiceID:     in.ReversesInvoiceID,
		SubtotalCents: -plan.subtotal,
		TaxCents:      -plan.tax,
		TotalCents:    -total,
		GLAccount:     accounts[RoleRevenue],
		Reason:        reason,
		ApprovalID:    parked.ApprovalID,
		Message:       creditAwaitingWords(total),
	}, nil
}

// CreditMemoJournalLine is one resolved leg of the reversing entry: an account code and a positive
// magnitude on one side. Billing resolves the roles into codes (see CreditMemoJournalLines), so the
// adapter that parks the journal never decides an account.
type CreditMemoJournalLine struct {
	AccountCode string
	DebitCents  int64
	CreditCents int64
	Memo        string
}

// reusedResult answers a retry with the credit the key already named.
func (s *CreditMemoService) reusedResult(doc InvoiceDoc, account, reason string) CreditMemoRouteResult {
	status := CreditMemoIssued
	message := creditIssuedWords(doc.InvoiceNo, -doc.TotalCents)
	if doc.Status == "DRAFT" {
		// A parked credit: it is still waiting for its approval, and the retry must say so rather
		// than claim a credit that has not moved.
		status = CreditMemoAwaitingApproval
		message = creditAwaitingWords(-doc.TotalCents)
	}
	return CreditMemoRouteResult{
		Status:        status,
		CreditMemoID:  doc.InvoiceID,
		CreditMemoNo:  doc.InvoiceNo,
		CustomerID:    doc.CustomerID,
		BranchID:      doc.BranchID,
		InvoiceID:     doc.ReversesInvoiceID,
		SubtotalCents: doc.SubtotalCents,
		TaxCents:      doc.TaxCents,
		TotalCents:    doc.TotalCents,
		GLAccount:     account,
		Reason:        reason,
		GLJournalID:   doc.GLJournalID,
		Reused:        true,
		Message:       message,
	}
}

// creditMemoKeyConflict is the 409 for an idempotency key that already named a different credit.
type creditMemoKeyConflict struct{ msg string }

func (e *creditMemoKeyConflict) Error() string { return e.msg }

// errCreditMemoCustomerNotFound is THE CUSTOMER ANSWER (UX-89): 404 "customer not found". An account
// the caller cannot reach, an account of another org, and an id that is no account answer exactly
// alike, so the door never says which of the three it was and is no existence oracle for another
// branch's accounts. The words are the house's, the same body the pos and AR doors write.
var errCreditMemoCustomerNotFound = &creditMemoCustomerNotFound{}

// creditMemoCustomerNotFound carries the one customer answer to the door.
type creditMemoCustomerNotFound struct{}

func (e *creditMemoCustomerNotFound) Error() string { return "customer not found" }

// creditMemoByKey finds the credit an idempotency key already raised, bounded to one org: a key from
// another org's memo answers as not found, so a key discloses nothing across orgs.
func (w *InvoiceWriter) creditMemoByKey(ctx context.Context, key, orgID string) (InvoiceDoc, bool, error) {
	if key == "" {
		return InvoiceDoc{}, false, nil
	}
	var (
		doc      InvoiceDoc
		reverses string
	)
	err := w.pool.QueryRow(ctx, `
		SELECT id::text, invoice_no::text, customer_id::text, branch_id::text, status,
		       subtotal_cents, tax_cents, total_cents, balance_cents,
		       COALESCE(reverses_invoice_id::text,''), COALESCE(job_id::text,'')
		  FROM invoices WHERE idempotency_key = $1 AND doc_type = 'CREDIT_MEMO' AND org_id = $2::uuid`, key, orgID).
		Scan(&doc.InvoiceID, &doc.InvoiceNo, &doc.CustomerID, &doc.BranchID, &doc.Status,
			&doc.SubtotalCents, &doc.TaxCents, &doc.TotalCents, &doc.BalanceCents, &reverses, &doc.jobID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return InvoiceDoc{}, false, nil
		}
		return InvoiceDoc{}, false, fmt.Errorf("billing: read the credit memo this key named: %w", err)
	}
	doc.DocType = DocCreditMemo
	doc.ReversesInvoiceID = reverses
	return doc, true, nil
}

// CreditMemoJournalLines resolves the reversing entry's ROLES into account codes, using the accounts
// the posting rules resolved for that branch.
//
// A role with no account is REFUSED, never guessed. The reversing entry's whole discipline is that
// the sides swap and the amounts stay positive; an entry with a leg missing is not a smaller credit,
// it is a wrong one.
func CreditMemoJournalLines(j CreditMemoJournal, accounts map[string]string) ([]CreditMemoJournalLine, error) {
	legs := j.ReversingLegs()
	out := make([]CreditMemoJournalLine, 0, len(legs))
	for _, leg := range legs {
		code := accounts[leg.Role]
		if code == "" {
			return nil, httpx.BadRequestf("the posting rules for this branch have no %q account, so this credit cannot be booked", leg.Role)
		}
		out = append(out, CreditMemoJournalLine{
			AccountCode: code,
			DebitCents:  leg.DebitCents,
			CreditCents: leg.CreditCents,
			Memo:        leg.Memo,
		})
	}
	return out, nil
}

// creditMemoKey derives the idempotency key when the caller supplies none, so a double click on the
// Money Desk button raises ONE credit.
//
// The key is built from the request's own facts and the dealer's house day: the same credit, asked
// for twice in one day, is one credit. A different amount, a different invoice or a different reason
// is a different credit, and gets its own key. The caller may always supply its own key, which is
// what a form that holds one across a retry does.
func (s *CreditMemoService) creditMemoKey(ctx context.Context, req CreditMemoRouteRequest, reason, jobID string) string {
	if k := strings.TrimSpace(req.IdempotencyKey); k != "" {
		return k
	}
	day := s.houseDay(ctx)
	seed := strings.Join([]string{
		day, req.BranchID, req.CustomerID, strings.TrimSpace(req.InvoiceID),
		fmt.Sprintf("%d", req.AmountCents), reason, jobID,
	}, "|")
	sum := sha256.Sum256([]byte(seed))
	return "credit-memo:" + hex.EncodeToString(sum[:])
}

// houseDay is the dealer's own day, the same zone the writer dates its documents in. A credit asked
// for at eleven at night and one asked for the next morning are different credits; two asked for in
// the same minute are the same one. A dealer whose zone cannot be read falls back to the house's
// default zone, never to the server's or to Greenwich.
func (s *CreditMemoService) houseDay(ctx context.Context) string {
	zone, err := houseday.Resolve(ctx, s.w.zone)
	if err != nil {
		return houseday.DefaultZone().Today(s.now)
	}
	return zone.Today(s.now)
}

// record stamps the trail. A credit with no audit row is a credit nobody can be asked about, which
// is the whole reason the route is manager-gated in the first place.
func (s *CreditMemoService) record(ctx context.Context, doc InvoiceDoc, in CreditMemoInput, total int64, account, reason string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, audit.SensitiveAccess{
		Action:     ActCreditMemoIssue,
		EntityType: "credit_memo",
		EntityID:   doc.InvoiceID,
		ActorID:    identity.StaffUserIDFromContext(ctx),
		BranchID:   in.BranchID,
		Purpose:    "CREDIT_MEMO",
		Reason:     reason,
		Extra: map[string]any{
			"customer_id": in.CustomerID,
			"invoice_id":  in.ReversesInvoiceID,
			"total_cents": total,
			"gl_account":  account,
			"credit_memo": doc.InvoiceNo,
		},
	})
}

// creditIssuedWords is what a person reads when the credit is a document.
func creditIssuedWords(no string, total int64) string {
	return "Credit memo " + no + " for " + Money(total) + " is issued."
}

// creditAwaitingWords is what a person reads when the credit is parked.
func creditAwaitingWords(total int64) string {
	return "This credit of " + Money(total) + " is over the approval threshold. Nothing has moved yet: a second person has to approve it before the customer is credited."
}

// -- the handler --------------------------------------------------------------------------------------

// CreditMemoRouteHandler serves the credit memo route.
type CreditMemoRouteHandler struct {
	svc      *CreditMemoService
	userLink identity.UserIDLinker
}

// NewCreditMemoRouteHandler wires the handler.
func NewCreditMemoRouteHandler(svc *CreditMemoService) *CreditMemoRouteHandler {
	return &CreditMemoRouteHandler{svc: svc}
}

// WithUserLinker sets the users.id linker the stamp carries. Nil is legal: the stamp is
// authority-only.
func (h *CreditMemoRouteHandler) WithUserLinker(link identity.UserIDLinker) *CreditMemoRouteHandler {
	h.userLink = link
	return h
}

// RegisterRoutes self-registers the credit memo route.
//
// The gate is RequireManager, and it is the gate the plan named: a manager or the Controller, and
// nobody else. A counter clerk cannot credit an account, and a Controller can.
func (h *CreditMemoRouteHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/ar/credit-memos", g.RequireManager(h.staff(h.issue)))
	r.Handle("POST /api/v1/ar/credit-memos/quote", g.RequireManager(h.staff(h.quote)))
}

// staff stamps the bridged staff context onto the request, the same bridge every other billing
// handler uses.
func (h *CreditMemoRouteHandler) staff(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		next(w, identity.StampRequest(r, caller, h.userLink), caller)
	}
}

// issue is the route body.
func (h *CreditMemoRouteHandler) issue(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var body CreditMemoRouteRequest
	if err := decode(r, &body); err != nil {
		h.writeErr(w, r, err)
		return
	}
	branches := make(map[string]bool, len(caller.Branches))
	for _, b := range caller.Branches {
		branches[b] = true
	}
	res, err := h.svc.Issue(r.Context(), body, CreditMemoCaller{
		OrgID:    caller.OrgID,
		Scope:    identity.ScopeFromCanonical(caller.Scope),
		Branches: branches,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if res.Status == CreditMemoAwaitingApproval {
		httpx.WriteJSON(w, http.StatusAccepted, res)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

// quote is the quote door's body: what the credit would come to, and what the invoice has left.
func (h *CreditMemoRouteHandler) quote(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var body CreditMemoRouteRequest
	if err := decode(r, &body); err != nil {
		h.writeErr(w, r, err)
		return
	}
	branches := make(map[string]bool, len(caller.Branches))
	for _, b := range caller.Branches {
		branches[b] = true
	}
	res, err := h.svc.Quote(r.Context(), body, CreditMemoCaller{
		OrgID:    caller.OrgID,
		Scope:    identity.ScopeFromCanonical(caller.Scope),
		Branches: branches,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// writeErr maps a refusal to its status, the same taxonomy the register's door uses.
func (h *CreditMemoRouteHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var bad httpx.BadRequestError
	var notFoundWords httpx.NotFoundError
	var sd *SupplierDepositNotCreditable
	var kc *creditMemoKeyConflict
	var cnf *creditMemoCustomerNotFound
	switch {
	case errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, bad.Error())
	case errors.As(err, &notFoundWords):
		// A named job that is no job (JA-2): the customer module's words behind a 404.
		httpx.Error(w, http.StatusNotFound, notFoundWords.Error())
	case errors.As(err, &cnf):
		// THE CUSTOMER ANSWER: 404 "customer not found", the house's body, byte for byte the one the
		// pos and AR doors write for an account the caller cannot reach.
		httpx.Error(w, http.StatusNotFound, cnf.Error())
	case errors.As(err, &sd):
		httpx.ErrorWith(w, http.StatusConflict, SupplierDepositNotCreditableWords,
			map[string]any{"code": SupplierDepositNotCreditableCode})
	case errors.As(err, &kc):
		httpx.Error(w, http.StatusConflict, kc.Error())
	case errors.Is(err, ErrNothingToInvoice):
		httpx.Error(w, http.StatusBadRequest, "there is nothing to credit")
	case errors.Is(err, errNoDB):
		httpx.Error(w, http.StatusServiceUnavailable, "the database is not configured")
	default:
		httpx.Error(w, http.StatusInternalServerError, err.Error())
	}
}

// -- the parked document's own writer ----------------------------------------------------------------

// ParkCreditMemoTx writes a parked credit memo on the CALLER's transaction: the header and the
// negated lines, as a DRAFT, with NO AR subledger row and NO journal.
//
// The status is the point. The AR balance counts only FINALIZED and PARTIALLY_PAID rows
// (billing/gl_seam.go), so a DRAFT memo moves no money: the customer's balance does not change, the
// aging does not move, and the ledger is not touched. The memo becomes money only when it is
// finalized, which happens on the approval's own transaction.
//
// Its balance is zero while it waits, not its negative total: a credit that is not yet a credit has
// no balance to collect.
//
// The plan is resolved HERE, on the caller's transaction, under the SF-2e locks the posted writer
// takes (lockForCreditPlan): the invoice's remainder is read under the reversed invoice's lock, so a
// parked credit and a posted credit of one invoice cannot together pass the cap, and the parked
// credit's room is held from the park to the approval. The reversing journal comes back with the
// document, so the composition root parks both without doing any arithmetic.
func (w *InvoiceWriter) ParkCreditMemoTx(ctx context.Context, tx pgx.Tx, in CreditMemoInput) (InvoiceDoc, CreditMemoJournal, error) {
	if tx == nil {
		return InvoiceDoc{}, CreditMemoJournal{}, errNoDB
	}
	if err := lockForCreditPlan(ctx, tx, in); err != nil {
		return InvoiceDoc{}, CreditMemoJournal{}, err
	}
	plan, err := w.planCreditMemo(ctx, tx, in)
	if err != nil {
		return InvoiceDoc{}, CreditMemoJournal{}, err
	}
	customerID, branchID := in.CustomerID, in.BranchID
	if customerID == "" || branchID == "" {
		if err := tx.QueryRow(ctx,
			`SELECT customer_id::text, branch_id::text FROM invoices WHERE id = $1`,
			in.ReversesInvoiceID).Scan(&customerID, &branchID); err != nil {
			return InvoiceDoc{}, CreditMemoJournal{}, httpx.BadRequestf("a credit memo needs a customer and a branch")
		}
	}
	sourceKind := in.SourceKind
	if sourceKind == "" {
		sourceKind = SourceOrder
	}
	zone, err := houseday.Resolve(ctx, w.zone)
	if err != nil {
		return InvoiceDoc{}, CreditMemoJournal{}, err
	}
	issueDate := zone.Today(w.now)
	total := plan.subtotal + plan.tax
	orgID, oerr := branchOrgID(ctx, tx, branchID)
	if oerr != nil {
		return InvoiceDoc{}, CreditMemoJournal{}, oerr
	}
	var id, no string
	err = tx.QueryRow(ctx, `
	INSERT INTO invoices (invoice_no, customer_id, order_id, job_id, branch_id, status, issue_date, due_date,
	                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at,
	                      doc_type, source_kind, idempotency_key, closes_order, reverses_invoice_id, org_id)
	VALUES ('#CM-' || nextval('invoice_no_seq'), $1, $2, $12, $3, 'DRAFT', $4, $4,
	        $5, $6, $7, 0, NULL,
	        'CREDIT_MEMO', $8, $9, FALSE, $10,
	        $11)
	RETURNING id::text, invoice_no`,
		customerID, nullUUID(in.OrderID), branchID, issueDate,
		-plan.subtotal, -plan.tax, -total,
		sourceKind, in.IdempotencyKey, nullUUID(in.ReversesInvoiceID), orgID, nullUUID(in.JobID),
	).Scan(&id, &no)
	if err != nil {
		return InvoiceDoc{}, CreditMemoJournal{}, fmt.Errorf("insert parked credit memo: %w", err)
	}
	perLineTax := make([]int64, len(plan.lines))
	if plan.tax != 0 {
		weights := make([]int64, len(plan.lines))
		for i, l := range plan.lines {
			weights[i] = l.LineTotalCents
		}
		perLineTax = moneyutil.AllocateLargestRemainder(plan.tax, weights)
	}
	for i, l := range plan.lines {
		if _, err := tx.Exec(ctx, `
		INSERT INTO invoice_lines (invoice_id, line_no, order_line_id, product_id, description, qty_entered,
		                           uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, tax_cents)
		VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8::numeric, $9, $10, $11, $12)`,
			id, i+1, nullUUID(l.OrderLineID), nullUUID(l.ProductID), l.Description,
			l.QtyEntered, l.UOMEntered, l.QtyBase,
			l.UnitPriceCents, l.PriceUOM, -l.LineTotalCents, -perLineTax[i]); err != nil {
			return InvoiceDoc{}, CreditMemoJournal{}, fmt.Errorf("insert parked credit memo line %d: %w", i+1, err)
		}
	}
	doc := InvoiceDoc{
		InvoiceID: id, InvoiceNo: no, DocType: DocCreditMemo,
		CustomerID: customerID, OrderID: in.OrderID, BranchID: branchID,
		Status: "DRAFT", DueDate: issueDate,
		SubtotalCents: -plan.subtotal, TaxCents: -plan.tax,
		TotalCents: -total, BalanceCents: 0,
		ReversesInvoiceID: in.ReversesInvoiceID, ClosesOrder: false,
	}
	journal := CreditMemoJournal{
		CreditMemoID:   id,
		CreditMemoNo:   no,
		BranchID:       branchID,
		IdempotencyKey: creditMemoJournalKey(id),
		Memo:           "Credit memo awaiting approval: " + in.Memo,
		OccurredAt:     w.now(),
		ARCents:        total,
		RevenueCents:   plan.subtotal,
		TaxCents:       plan.tax,
	}
	return doc, journal, nil
}

// FinalizeParkedCreditMemoTx turns a parked credit memo into money, on the CALLER's transaction: the
// memo is FINALIZED and its AR subledger row is written.
//
// It is the approve half of the rail: the decision that approves the parked journal also finalizes
// the memo, on that decision's own transaction, so the customer is credited and the ledger is posted
// together or not at all.
//
// A memo that is not parked (already finalized, or void) is refused, so a double approval cannot
// credit the customer twice.
func (w *InvoiceWriter) FinalizeParkedCreditMemoTx(ctx context.Context, tx pgx.Tx, memoID string) error {
	if tx == nil {
		return errNoDB
	}
	var (
		no            string
		total         int64
		subtotal      int64
		tax           int64
		customerID    string
		branchID      string
		reverses      string
		sourceKind    string
		orderID       string
		currentStatus string
	)
	err := tx.QueryRow(ctx, `
	SELECT invoice_no::text, -total_cents, -subtotal_cents, -tax_cents,
	       customer_id::text, branch_id::text, COALESCE(reverses_invoice_id::text,''),
	       source_kind, COALESCE(order_id::text,''), status
	  FROM invoices WHERE id = $1 AND doc_type = 'CREDIT_MEMO'`, memoID).
		Scan(&no, &total, &subtotal, &tax, &customerID, &branchID, &reverses, &sourceKind, &orderID, &currentStatus)
	if err != nil {
		if err == pgx.ErrNoRows {
			return httpx.BadRequestf("no such credit memo to finalize")
		}
		return fmt.Errorf("billing: read parked credit memo: %w", err)
	}
	if currentStatus != "DRAFT" {
		return httpx.BadRequestf("credit memo %s is %s, not a parked credit waiting for approval", no, currentStatus)
	}
	// THE CAP, RE-READ AT APPROVAL TIME under the reversed invoice's lock, the SF-2e way: the room is
	// the original's subtotal and tax less every memo that is not VOID, and this memo's own take is
	// added back, because the draft counts in that sum. A credit whose room another document took
	// while it waited is refused here, and the approver can decline it instead of crediting past the
	// invoice. (The old re-check read the original's balance, which a memo never changes: it refused
	// every over-threshold credit of a paid invoice for good, and caught no over-credit at all.)
	if reverses != "" {
		if _, err := tx.Exec(ctx,
			`SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, reverses); err != nil {
			return fmt.Errorf("billing: lock the reversed invoice at the approval: %w", err)
		}
		var (
			origNo           string
			origSub, origTax int64
			prevSub, prevTax int64
		)
		if err := tx.QueryRow(ctx,
			`SELECT invoice_no::text, subtotal_cents, tax_cents FROM invoices WHERE id = $1::uuid`, reverses).
			Scan(&origNo, &origSub, &origTax); err != nil {
			if err == pgx.ErrNoRows {
				return httpx.BadRequestf("the invoice this credit was planned against is gone")
			}
			return fmt.Errorf("billing: read the reversed invoice at the approval: %w", err)
		}
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(-SUM(subtotal_cents), 0), COALESCE(-SUM(tax_cents), 0)
			  FROM invoices WHERE reverses_invoice_id = $1::uuid AND doc_type = 'CREDIT_MEMO' AND status <> 'VOID'`,
			reverses).Scan(&prevSub, &prevTax); err != nil {
			return fmt.Errorf("billing: read what is already credited at the approval: %w", err)
		}
		leftSub, leftTax := origSub-prevSub+subtotal, origTax-prevTax+tax
		if subtotal > leftSub || tax > leftTax {
			return httpx.BadRequestf("the credits already issued against invoice %s leave %s before tax (%s with tax); this credit is %s, so it cannot be approved",
				origNo, Money(leftSub-subtotal), Money(leftSub-subtotal+leftTax-tax), Money(total))
		}
	}
	if _, err := tx.Exec(ctx, `
	UPDATE invoices SET status = 'FINALIZED', balance_cents = $2, finalized_at = now()
	 WHERE id = $1 AND status = 'DRAFT'`, memoID, -total); err != nil {
		return fmt.Errorf("billing: finalize parked credit memo: %w", err)
	}
	var open int64
	if err := tx.QueryRow(ctx, `
	SELECT COALESCE(SUM(balance_cents),0) FROM invoices
	  WHERE customer_id = $1 AND status IN ('FINALIZED','PARTIALLY_PAID')`, customerID).Scan(&open); err != nil {
		return fmt.Errorf("billing: read open balance: %w", err)
	}
	// source_type='invoice' is LOAD-BEARING, the same rule the writer states: a memo tagged anything
	// else would be counted twice by the 1100 tie-out, once as the document's negative balance and
	// once as a standalone adjustment.
	if _, err := tx.Exec(ctx, `
	INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type,
	                                   source_ref_id, branch_id, memo, occurred_at)
	VALUES ($1, 'CREDIT_MEMO', $2, $3, 'invoice', $4, $5, $6, clock_timestamp())`,
		customerID, -total, open, memoID, branchID, "Credit memo "+no); err != nil {
		return fmt.Errorf("billing: insert AR subledger row for the parked credit memo: %w", err)
	}
	return nil
}

// VoidParkedCreditMemoTx voids a parked credit memo's DRAFT document on the CALLER's transaction: the
// reject and withdraw arms of the approval rail. The SF-2e cap counts every memo that is not VOID, so
// a refused or withdrawn park would hold the invoice's room for good; the void is what frees it. A
// memo that is not a DRAFT is left as it stands: already finalized is live money the customer is owed
// the credit on, already void needs nothing.
func (w *InvoiceWriter) VoidParkedCreditMemoTx(ctx context.Context, tx pgx.Tx, memoID string) error {
	if tx == nil {
		return errNoDB
	}
	if _, err := tx.Exec(ctx, `
		UPDATE invoices SET status = 'VOID'
		 WHERE id = $1::uuid AND doc_type = 'CREDIT_MEMO' AND status = 'DRAFT'`, memoID); err != nil {
		return fmt.Errorf("billing: void the parked credit memo: %w", err)
	}
	return nil
}
