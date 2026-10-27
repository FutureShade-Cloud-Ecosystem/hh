package gl

// store.go, the persistence seam the Service depends on (mirrors the internal/till tillStore /
// internal/pricing pricingStore pattern): the service holds an interface so unit tests inject a DB-free fake
// and the integration suite injects the real pgx *Repository. Every method reads/writes ONLY GL-owned tables
// (the 0007 block + the 0032 additions) and the 0034 balance views.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type glStore interface {
	// - policy -
	Policy(ctx context.Context, branchID string) (Policy, error)

	// - posting rules -
	PostingAccounts(ctx context.Context, eventType, branchID string) (map[string]string, error) // line_role → account_code
	// PostingAccountsTx is PostingAccounts read through the caller's transaction (no second pool connection).
	PostingAccountsTx(ctx context.Context, tx pgx.Tx, eventType, branchID string) (map[string]string, error)
	ListActiveRules(ctx context.Context) ([]RuleRef, error)

	// - chart of accounts -
	ListAccounts(ctx context.Context) ([]Account, error)
	AccountByCode(ctx context.Context, code string) (Account, bool, error)
	InsertAccount(ctx context.Context, in CreateAccountInput) (Account, error)
	DraftLineCountForAccountCode(ctx context.Context, code string) (int, error)
	SetAccountActive(ctx context.Context, code string, active bool) (Account, error)

	// - journals (the shared persist path) -
	InsertJournal(ctx context.Context, d journalDraft) (id string, created bool, err error)
	// InsertJournalTx is InsertJournal inside the caller's transaction, under a savepoint (see the repository).
	InsertJournalTx(ctx context.Context, tx pgx.Tx, d journalDraft) (id string, created bool, err error)
	// InsertParkedJournal inserts a DRAFT and runs file on the SAME transaction before committing: if file
	// fails, the journal is not saved. file is how a parked journal's approval request is filed atomically.
	InsertParkedJournal(ctx context.Context, d journalDraft, file func(ctx context.Context, tx pgx.Tx, journalID string) error) (string, error)
	GetJournal(ctx context.Context, id string) (Journal, error)
	// JournalIsReversed says whether another journal names this one in reverses_journal_id (the GL keeps the original
	// POSTED and adds a reversing journal).
	JournalIsReversed(ctx context.Context, id string) (bool, error)
	ListJournals(ctx context.Context, f JournalFilter) ([]Journal, error)
	ApproveJournal(ctx context.Context, id, approverID string) (Journal, error)
	// ApproveJournalTx posts a parked DRAFT within the caller's transaction (approvals-rail atomic
	// decide, 0075), DRAFT-state + four-eyes enforced in one guarded UPDATE so the ledger post shares
	// the approval-decision transaction.
	ApproveJournalTx(ctx context.Context, tx pgx.Tx, id, approverID string) error
	VoidJournal(ctx context.Context, id string) (Journal, error)
	// VoidJournalTx voids a parked DRAFT journal on the caller's transaction (the rail's reject arm,
	// FW1-12a's core round): a refused or withdrawn credit memo's journal must not hold its room as a
	// draft forever. A journal that is not a DRAFT is left as it stands (idempotent).
	VoidJournalTx(ctx context.Context, tx pgx.Tx, id string) error

	// - periods -
	ListPeriods(ctx context.Context) ([]Period, error)
	GetPeriod(ctx context.Context, id string) (Period, error)
	ClosePeriod(ctx context.Context, id, actorID, reason string) (Period, error)
	ReopenPeriod(ctx context.Context, id, actorID, reason string) (Period, error)
	ClosePeriodsForYear(ctx context.Context, year int, actorID, reason string) error

	// - balances (0034 views) -
	AccountBalance(ctx context.Context, code string, year, month int, branchID string) (netDebit int64, normalBalance string, err error)
	// AccountBalanceAsOf is the INCEPTION-TO-DATE net-debit of an account across ALL fiscal years, bounded by
	// posted date <= asOf (the point-in-time basis the AR↔GL tie-out needs so a year-boundary carry-forward
	// does not read as a spurious discrepancy). branchID "" is consolidated.
	AccountBalanceAsOf(ctx context.Context, code string, asOf time.Time, branchID string) (netDebit int64, normalBalance string, err error)
	// TrialBalanceAsOf is the INCEPTION-TO-DATE closing trial balance (one row per account with POSTED activity
	// through asOf, across ALL fiscal years), the whole-chart analog of AccountBalanceAsOf and the basis the
	// BisTrack CLOSING-balance tie-out reconciles against. TrialBalance below is per-period MOVEMENT and must
	// NOT be used to tie out a cumulative closing export. branchID "" is consolidated.
	TrialBalanceAsOf(ctx context.Context, asOf time.Time, branchID string) ([]TrialBalanceRow, error)
	TrialBalance(ctx context.Context, year, month int, branchID string) ([]TrialBalanceRow, error)
	PLBalances(ctx context.Context, year int) ([]TrialBalanceRow, error)

	// - tie-out state (first-seen age) -
	UpsertTieout(ctx context.Context, subledger, accountCode, branchID string, subledgerCents, glCents int64, at time.Time) (TieoutState, error)

	// - recurring -
	ListRecurring(ctx context.Context) ([]RecurringTemplate, error)
	GetRecurring(ctx context.Context, id string) (RecurringTemplate, error)

	// - GP-parity export -
	InsertExportBatch(ctx context.Context, year, month int, format, actorID string, rowCount int) (string, error)
	ExportRows(ctx context.Context, year, month int, branchID string) ([]ExportRow, error)
}
