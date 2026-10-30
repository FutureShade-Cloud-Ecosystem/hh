package purchasing

// vendor_credit.go: THE SUPPLIER CREDIT NOTE (wave 3 item 0.8).
//
// A vendor ships goods back, doubles an invoice, or agrees a price adjustment after the bill
// posted: the supplier sends a credit note, and this file is its door. The note is entered against
// a vendor and a branch (the supplier's own credit number, a document date, an optional original
// bill or PO reference), posted DR 2100 / CR the lines' accounts through the GL seam its bill
// sibling posts through, and applied to that vendor's open bills in the same branch or held on
// account.
//
// THE JOURNAL IS THE BILL READ BACKWARDS. The DR 2100 leg resolves through the bill's own posting
// rule (vendor.bill.posted/ap, PayBill's resolution), never a literal code. Each line's account is
// resolved at ENTRY against the bill event's debit side roles (ap_accrual, the inventory clearing
// account, and price_variance, the expense): the credit can only land where a bill could have
// charged, and the resolved code is stored, so the journal posts what the clerk saw and no second
// copy of the rule exists. The GL source stays PO_RECEIPT: the CHECK set on gl_journals.source is
// closed, and a credit note is a purchasing AP document like the bill it reverses.
//
// THE APPLICATION MOVES NO MONEY. The credit's journal already debited 2100 and the bill's journal
// already credited it, so applying is a netting inside accounts payable: subledger opens only, no
// journal, no GL. That is why an unapplied credit shows in the AP aging as a negative item on the
// vendor's row and the aging still ties to GL 2100 to the cent.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5"
)

// The credit's document status, and the key namespaces. The post's journal key is idempotent on
// the credit id the way the bill post's is (purchasing.bill:<billID>); the application's replay key
// rides no journal (an application writes none), so it lives on the application row itself under
// its own namespace and is judged under the same advisory-lock discipline a payment key gets.
const (
	VendorCreditDraft  = "DRAFT"
	VendorCreditPosted = "POSTED"

	glVendorCreditKeyPrefix      = "purchasing.vendor_credit:"
	glVendorCreditApplyKeyPrefix = "purchasing.vendor_credit.apply:"
)

// - wire shapes -

// VendorCredit is a supplier credit note header projection.
type VendorCredit struct {
	ID         string `json:"id"`
	CreditNo   string `json:"credit_no"`
	VendorID   string `json:"vendor_id"`
	VendorName string `json:"vendor_name"`
	BranchID   string `json:"branch_id"`
	// The optional original bill or PO reference the clerk entered the note against.
	BillID string `json:"bill_id,omitempty"`
	BillNo string `json:"bill_no,omitempty"`
	POID   string `json:"po_id,omitempty"`
	PONo   string `json:"po_no,omitempty"`
	Status string `json:"status"`
	// CreditDate is YYYY-MM-DD, the document date the journal files under.
	CreditDate string `json:"credit_date"`
	// TotalCents is the positive magnitude the supplier wrote. AppliedCents is what its
	// applications have consumed; OpenCents is what is left to apply, held on account when
	// positive and never below zero.
	TotalCents   int64      `json:"total_cents"`
	AppliedCents int64      `json:"applied_cents"`
	OpenCents    int64      `json:"open_cents"`
	GLJournalID  string     `json:"gl_journal_id,omitempty"`
	PostedAt     *time.Time `json:"posted_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// VendorCreditLine is one line of the credit: the account the credit puts its cents back on
// (inventory clearing or expense, resolved against the posting rules) and the cents.
type VendorCreditLine struct {
	LineNo      int    `json:"line_no"`
	AccountCode string `json:"account_code"`
	Description string `json:"description"`
	AmountCents int64  `json:"amount_cents"`
}

// VendorCreditApplication is one netting of the credit against a bill. It carries no journal
// because it moves none.
type VendorCreditApplication struct {
	ID          string    `json:"id"`
	BillID      string    `json:"bill_id"`
	BillNo      string    `json:"bill_no"`
	AmountCents int64     `json:"amount_cents"`
	AppliedAt   time.Time `json:"applied_at"`
}

// VendorCreditDetail is a credit with its lines and applications.
type VendorCreditDetail struct {
	VendorCredit
	Lines        []VendorCreditLine        `json:"lines"`
	Applications []VendorCreditApplication `json:"applications"`
}

// VendorCreditPage is the contract's page envelope for the list door.
type VendorCreditPage struct {
	Items      []VendorCredit `json:"items"`
	NextCursor *string        `json:"next_cursor"`
	Limit      int32          `json:"limit"`
}

// - requests -

// CreateVendorCreditRequest is the POST /purchasing/vendor-credits body. The credit number is the
// SUPPLIER's, and is never minted here; lines carry the account each puts its cents back on, empty
// meaning the posting rules' inventory clearing account for the branch.
type CreateVendorCreditRequest struct {
	VendorID   string                   `json:"vendor_id"`
	BranchID   string                   `json:"branch_id"`
	CreditNo   string                   `json:"credit_no"`
	BillID     string                   `json:"bill_id"`
	POID       string                   `json:"po_id"`
	CreditDate string                   `json:"credit_date"`
	Lines      []VendorCreditLineParams `json:"lines"`
}

// VendorCreditLineParams is one line of the create body.
type VendorCreditLineParams struct {
	AccountCode string `json:"account_code"`
	Description string `json:"description"`
	AmountCents int64  `json:"amount_cents"`
}

// ApplyVendorCreditRequest is the POST /purchasing/vendor-credits/{id}/applications body. The
// replay key rides the Idempotency-Key header the way a bill payment's does: one key per press.
type ApplyVendorCreditRequest struct {
	BillID         string `json:"bill_id"`
	AmountCents    int64  `json:"amount_cents"`
	IdempotencyKey string `json:"-"`
}

// - service -

// CreateVendorCredit enters a supplier credit note against a vendor and a branch. The note is
// DRAFT: nothing has been posted and nothing is owed back until PostVendorCredit.
func (s *Service) CreateVendorCredit(ctx context.Context, in CreateVendorCreditRequest) (VendorCreditDetail, error) {
	caller, branchID, err := s.branchFence(ctx, in.BranchID)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	if branchID == "" {
		return VendorCreditDetail{}, badReq("purchasing: branch_id is required on a vendor credit")
	}
	if strings.TrimSpace(in.CreditNo) == "" {
		return VendorCreditDetail{}, badReq("purchasing: credit_no (the supplier's credit note number) is required")
	}
	if in.VendorID == "" {
		return VendorCreditDetail{}, badReq("purchasing: vendor_id is required")
	}
	if len(in.Lines) == 0 {
		return VendorCreditDetail{}, badReq("purchasing: a vendor credit needs at least one line")
	}
	if s.vendor == nil {
		return VendorCreditDetail{}, ErrVendorSeamUnwired
	}
	v, err := s.vendor.Vendor(ctx, in.VendorID)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	if !v.IsActive {
		return VendorCreditDetail{}, conflict("purchasing: vendor %s is not active", v.VendorNo)
	}

	// The dealer's day, decided BEFORE the transaction opens (a zone read must not hold a
	// connection beside the open transaction): the default document date.
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	creditDate := strings.TrimSpace(in.CreditDate)
	if creditDate == "" {
		creditDate = zone.Today(nil)
	}

	// The lines' accounts resolve at ENTRY against the bill event's debit side roles, so a credit
	// can only land where a bill could have charged, and the stored code is what the journal posts.
	clearing, expense, err := s.creditLineAccounts(ctx, branchID)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	var total int64
	resolved := make([]VendorCreditLine, 0, len(in.Lines))
	for i, ln := range in.Lines {
		if ln.AmountCents <= 0 {
			return VendorCreditDetail{}, badReq("purchasing: credit line %d must be a positive amount", i+1)
		}
		account := strings.TrimSpace(ln.AccountCode)
		switch account {
		case "":
			account = clearing
		case clearing, expense:
		default:
			return VendorCreditDetail{}, badReq(
				"purchasing: credit line %d names account %s, which the posting rules for this branch do not allow a credit on (want %s or %s)",
				i+1, account, clearing, expense)
		}
		total += ln.AmountCents
		resolved = append(resolved, VendorCreditLine{
			LineNo: i + 1, AccountCode: account, Description: ln.Description, AmountCents: ln.AmountCents,
		})
	}

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// An optional original bill or PO reference, a pointing hand for the clerk. A bill reference is
	// held to the credit's own vendor AND branch: a note the supplier sent for one yard's delivery
	// filed against another yard's bill is a misfiling, whatever the caller's reach.
	if ref := strings.TrimSpace(in.BillID); ref != "" {
		b, err := s.repo.BillForUpdate(ctx, tx, ref)
		if err != nil {
			return VendorCreditDetail{}, err
		}
		if b.VendorID != in.VendorID {
			return VendorCreditDetail{}, badReq("purchasing: the credited bill belongs to another vendor")
		}
		if b.BranchID != branchID {
			return VendorCreditDetail{}, badReq("purchasing: a credit against a bill is filed at the bill's branch")
		}
	}
	if ref := strings.TrimSpace(in.POID); ref != "" {
		po, err := s.repo.GetPOTx(ctx, tx, ref)
		if err != nil {
			return VendorCreditDetail{}, err
		}
		if po.VendorID != in.VendorID {
			return VendorCreditDetail{}, badReq("purchasing: the referenced purchase order belongs to another vendor")
		}
		if po.BranchID != branchID {
			return VendorCreditDetail{}, badReq("purchasing: a credit against a purchase order is filed at the order's branch")
		}
	}

	creditID, err := s.repo.InsertVendorCreditTx(ctx, tx, insertVendorCreditParams{
		CreditNo: strings.TrimSpace(in.CreditNo), VendorID: in.VendorID, BranchID: branchID,
		BillID: strings.TrimSpace(in.BillID), POID: strings.TrimSpace(in.POID),
		CreditDate: creditDate, Total: total, CreatedBy: caller.UserID,
	})
	if err != nil {
		return VendorCreditDetail{}, err
	}
	for _, ln := range resolved {
		if err := s.repo.InsertVendorCreditLineTx(ctx, tx, creditID, ln); err != nil {
			return VendorCreditDetail{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return VendorCreditDetail{}, fmt.Errorf("purchasing: commit vendor credit: %w", err)
	}
	s.log.Info("purchasing: vendor credit entered", "credit_no", in.CreditNo, "vendor", v.VendorNo,
		"total_cents", total)
	s.record(ctx, "purchasing.vendor_credit.created", creditID, map[string]any{
		"credit_no": in.CreditNo, "vendor_id": in.VendorID, "total_cents": total})
	return s.GetVendorCredit(ctx, creditID)
}

// creditLineAccounts resolves the two accounts a credit line may land on: the bill event's
// inventory clearing account (ap_accrual) and its expense (price_variance), for the branch. The
// rules are the one copy of the fact; with the rules unwired there is no account to name and the
// credit is refused, the bill post's own refusal.
func (s *Service) creditLineAccounts(ctx context.Context, branchID string) (clearing, expense string, err error) {
	if s.rules == nil {
		return "", "", ErrGLSeamUnwired
	}
	if clearing, err = s.rules.ResolveRule(ctx, eventBillPosted, roleAPAccrual, branchID); err != nil {
		return "", "", err
	}
	if expense, err = s.rules.ResolveRule(ctx, eventBillPosted, rolePriceVar, branchID); err != nil {
		return "", "", err
	}
	return clearing, expense, nil
}

// PostVendorCredit posts the credit's journal (DR 2100 / CR the lines' accounts) through the GL
// seam, on the credit's own transaction. Idempotent on the credit id at the GL, so a retry after a
// mid-flight failure re-posts the same (deduped) journal rather than a second payable relief.
func (s *Service) PostVendorCredit(ctx context.Context, creditID string) (VendorCreditDetail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	if s.gl == nil || s.rules == nil {
		return VendorCreditDetail{}, ErrGLSeamUnwired
	}
	// The dealer's day, decided BEFORE the transaction opens: the document date the journal files
	// under is the credit's own date, a calendar day in the house zone.
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return VendorCreditDetail{}, err
	}

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	credit, err := s.repo.VendorCreditForUpdate(ctx, tx, creditID)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	if !caller.CanAccessBranch(credit.BranchID) {
		return VendorCreditDetail{}, errNotFound
	}
	if credit.Status != VendorCreditDraft {
		return VendorCreditDetail{}, conflict("purchasing: vendor credit is already %s", credit.Status)
	}
	lines, err := s.repo.VendorCreditLinesTx(ctx, tx, credit.ID)
	if err != nil {
		return VendorCreditDetail{}, err
	}

	apCode, err := s.rules.ResolveRuleTx(ctx, tx, eventBillPosted, roleAP, credit.BranchID)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	glLines := make([]GLLine, 0, len(lines)+1)
	for _, ln := range lines {
		glLines = append(glLines, GLLine{AccountCode: ln.AccountCode, CreditCents: ln.AmountCents, Memo: creditMemoLine(ln)})
	}
	glLines = append(glLines, GLLine{AccountCode: apCode, DebitCents: credit.TotalCents, Memo: "vendor credit " + credit.CreditNo})

	var documentDate time.Time
	if d, err := zone.ParseDay(credit.CreditDate); err == nil {
		documentDate = d
	}
	journalID, err := s.gl.PostJournalTx(ctx, tx, GLJournal{
		Source:         glSourcePOReceipt,
		SourceRefID:    credit.ID,
		IdempotencyKey: glVendorCreditKeyPrefix + credit.ID,
		Memo:           "Vendor credit " + credit.CreditNo,
		BranchID:       credit.BranchID,
		Lines:          glLines,
		DocumentDate:   documentDate,
	})
	if err != nil {
		return VendorCreditDetail{}, err
	}
	if err := s.repo.SetVendorCreditPosted(ctx, tx, credit.ID, journalID); err != nil {
		return VendorCreditDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return VendorCreditDetail{}, fmt.Errorf("purchasing: commit post vendor credit: %w", err)
	}
	s.log.Info("purchasing: vendor credit posted", "credit_no", credit.CreditNo, "total_cents", credit.TotalCents,
		"gl_journal_id", journalID)
	s.record(ctx, "purchasing.vendor_credit.posted", credit.ID, map[string]any{
		"credit_no": credit.CreditNo, "total_cents": credit.TotalCents, "gl_journal_id": journalID})
	return s.GetVendorCredit(ctx, credit.ID)
}

func creditMemoLine(ln VendorCreditLine) string {
	if strings.TrimSpace(ln.Description) != "" {
		return "vendor credit line: " + ln.Description
	}
	return "vendor credit line " + fmt.Sprint(ln.LineNo)
}

// ApplyVendorCredit nets part of a POSTED credit against one of the same vendor's open POSTED bills
// in the same branch. An application writes no journal: the credit's journal already debited 2100
// and the bill's credited it, so this moves the subledger opens only.
//
// The replay judgement runs before any money moves: a re-press under the first press's key returns
// the first application and writes nothing. A key spent on another credit's application is a
// conflict, not a replay. The guards hold the amount to BOTH opens as they stand under the locks:
// the credit's open (total less every application) and the bill's open (total less its payments and
// its applications), so a credit can never be applied twice, nor above either open amount.
func (s *Service) ApplyVendorCredit(ctx context.Context, creditID string, in ApplyVendorCreditRequest) (VendorCreditDetail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		return VendorCreditDetail{}, badReq("purchasing: idempotency_key is required on a credit application (one key per press)")
	}
	if in.AmountCents <= 0 {
		return VendorCreditDetail{}, badReq("purchasing: application amount_cents must be positive")
	}
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	appliedAt := s.clock().In(zone.Location())

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// One key is one application across every credit: two presses under the same key serialize
	// here, so the second sees the first's row and is judged a replay or a key spent elsewhere,
	// never a second application.
	if err := s.repo.LockPaymentKeyTx(ctx, tx, glVendorCreditApplyKeyPrefix+key); err != nil {
		return VendorCreditDetail{}, err
	}

	credit, err := s.repo.VendorCreditForUpdate(ctx, tx, creditID)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	if !caller.CanAccessBranch(credit.BranchID) {
		return VendorCreditDetail{}, errNotFound
	}

	// The replay judgement, before anything is written. The key's own row names the credit and the
	// bill it was spent on: the same credit and bill is a replay, answered with the credit as the
	// first press left it; anything else is a key spent elsewhere, a conflict.
	keyCreditID, keyBillID, found, err := s.repo.VendorCreditApplicationByKeyTx(ctx, tx, glVendorCreditApplyKeyPrefix+key)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	if found {
		if keyCreditID != credit.ID {
			return VendorCreditDetail{}, conflict("purchasing: idempotency key already used for another credit's application")
		}
		if keyBillID != strings.TrimSpace(in.BillID) {
			return VendorCreditDetail{}, conflict("purchasing: idempotency key already used for another bill's application")
		}
		if err := tx.Commit(ctx); err != nil {
			return VendorCreditDetail{}, fmt.Errorf("purchasing: commit credit application replay: %w", err)
		}
		return s.GetVendorCredit(ctx, credit.ID)
	}

	if credit.Status != VendorCreditPosted {
		return VendorCreditDetail{}, conflict("purchasing: vendor credit is %s; only POSTED credits can be applied", credit.Status)
	}

	bill, err := s.repo.BillForUpdate(ctx, tx, strings.TrimSpace(in.BillID))
	if err != nil {
		return VendorCreditDetail{}, err
	}
	if !caller.CanAccessBranch(bill.BranchID) {
		return VendorCreditDetail{}, errNotFound
	}
	if bill.VendorID != credit.VendorID {
		return VendorCreditDetail{}, conflict("purchasing: the credit belongs to another vendor than the bill")
	}
	if bill.BranchID != credit.BranchID {
		return VendorCreditDetail{}, conflict("purchasing: a credit is applied to a bill in its own branch")
	}
	if bill.Status != BillPosted {
		return VendorCreditDetail{}, conflict("purchasing: bill %s is %s; a credit can only be applied to a POSTED bill", bill.BillNo, bill.Status)
	}

	// The credit's applied sum is read in its own statement, after the credit's row lock is held:
	// the locking read's snapshot was taken before it waited for the lock, so a press that waited
	// behind another would not see that press's application and could spend the credit twice
	// (S6 on 704, P1-1). A new statement sees every application committed before the lock.
	applied, err := s.repo.AppliedCreditCentsTx(ctx, tx, credit.ID)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	creditOpen := credit.TotalCents - applied
	if in.AmountCents > creditOpen {
		return VendorCreditDetail{}, conflict("purchasing: application of %d cents exceeds the credit's open %d cents on %s",
			in.AmountCents, creditOpen, credit.CreditNo)
	}
	paid, err := s.repo.PaidBillCentsTx(ctx, tx, bill.ID)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	credited, err := s.repo.CreditedBillCentsTx(ctx, tx, bill.ID)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	billOpen := bill.TotalCents - paid - credited
	if in.AmountCents > billOpen {
		return VendorCreditDetail{}, conflict("purchasing: application of %d cents exceeds the bill's open %d cents on %s",
			in.AmountCents, billOpen, bill.BillNo)
	}

	if _, err := s.repo.InsertVendorCreditApplicationTx(ctx, tx, credit.ID, bill.ID, in.AmountCents,
		glVendorCreditApplyKeyPrefix+key, caller.UserID, appliedAt); err != nil {
		return VendorCreditDetail{}, err
	}
	// The database's own guard rides the same lock: applied_cents (0468) moves by this
	// application on the credit's row, and its CHECK refuses an applied total above the credit's
	// total even if a path ever skipped the guards above (S6 on 704's P1-1, defence in depth).
	// The refusal names the credit, words a clerk can act on.
	if err := s.repo.AddVendorCreditAppliedTx(ctx, tx, credit.ID, credit.CreditNo, in.AmountCents); err != nil {
		return VendorCreditDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return VendorCreditDetail{}, fmt.Errorf("purchasing: commit credit application: %w", err)
	}
	s.log.Info("purchasing: vendor credit applied", "credit_no", credit.CreditNo, "bill_no", bill.BillNo,
		"amount_cents", in.AmountCents)
	s.record(ctx, "purchasing.vendor_credit.applied", credit.ID, map[string]any{
		"credit_no": credit.CreditNo, "bill_id": bill.ID, "amount_cents": in.AmountCents})
	return s.GetVendorCredit(ctx, credit.ID)
}

// GetVendorCredit returns a credit with its lines and applications, branch-fenced.
func (s *Service) GetVendorCredit(ctx context.Context, creditID string) (VendorCreditDetail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	credit, err := s.repo.GetVendorCredit(ctx, creditID)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	if !caller.CanAccessBranch(credit.BranchID) {
		return VendorCreditDetail{}, errNotFound
	}
	if names := s.vendorNames(ctx); names != nil {
		credit.VendorName = names[credit.VendorID]
	}
	return credit, nil
}

// ListVendorCredits lists branch-fenced vendor credits, optionally narrowed to one vendor and one
// status.
func (s *Service) ListVendorCredits(ctx context.Context, branchID, vendorID, status string) (VendorCreditPage, error) {
	_, branch, err := s.branchFence(ctx, branchID)
	if err != nil {
		return VendorCreditPage{}, err
	}
	items, err := s.repo.ListVendorCredits(ctx, branch, strings.TrimSpace(vendorID), strings.TrimSpace(status))
	if err != nil {
		return VendorCreditPage{}, err
	}
	if names := s.vendorNames(ctx); names != nil {
		for i := range items {
			items[i].VendorName = names[items[i].VendorID]
		}
	}
	if items == nil {
		items = []VendorCredit{}
	}
	return VendorCreditPage{Items: items}, nil
}

// - repository -

type insertVendorCreditParams struct {
	CreditNo   string
	VendorID   string
	BranchID   string
	BillID     string
	POID       string
	CreditDate string
	Total      int64
	CreatedBy  string
}

func (r *Repository) InsertVendorCreditTx(ctx context.Context, tx pgx.Tx, in insertVendorCreditParams) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	err := r.q(tx).QueryRow(ctx, `
		INSERT INTO purchasing_vendor_credits (credit_no, vendor_id, branch_id, bill_id, po_id, status,
		                                      credit_date, total_cents, created_by)
		VALUES ($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,'DRAFT',$6::date,$7,NULLIF($8,'')::uuid)
		RETURNING id::text`,
		in.CreditNo, in.VendorID, in.BranchID, in.BillID, in.POID, in.CreditDate, in.Total, in.CreatedBy,
	).Scan(&id)
	if isUniqueViolation(err) {
		return "", conflict("purchasing: credit note %q has already been entered for this vendor", in.CreditNo)
	}
	if err != nil {
		return "", fmt.Errorf("purchasing: insert vendor credit: %w", err)
	}
	return id, nil
}

func (r *Repository) InsertVendorCreditLineTx(ctx context.Context, tx pgx.Tx, creditID string, ln VendorCreditLine) error {
	if r.pool == nil {
		return errNoDB
	}
	if _, err := r.q(tx).Exec(ctx, `
		INSERT INTO purchasing_vendor_credit_lines (credit_id, line_no, account_code, description, amount_cents)
		VALUES ($1,$2,$3,$4,$5)`,
		creditID, ln.LineNo, ln.AccountCode, ln.Description, ln.AmountCents); err != nil {
		return fmt.Errorf("purchasing: insert vendor credit line: %w", err)
	}
	return nil
}

const vendorCreditCols = `
	  c.id::text, c.credit_no, c.vendor_id::text, c.branch_id::text,
	  COALESCE(c.bill_id::text,''), COALESCE(b.bill_no::text,''),
	  COALESCE(c.po_id::text,''), COALESCE(p.po_no::text,''),
	  c.status, to_char(c.credit_date,'YYYY-MM-DD'), c.total_cents,
	  COALESCE(c.gl_journal_id::text,''), c.posted_at, c.created_at,
	  COALESCE((SELECT SUM(a.amount_cents) FROM purchasing_vendor_credit_applications a WHERE a.credit_id = c.id), 0)`

func scanVendorCredit(row pgx.Row) (VendorCredit, error) {
	var c VendorCredit
	if err := row.Scan(&c.ID, &c.CreditNo, &c.VendorID, &c.BranchID,
		&c.BillID, &c.BillNo, &c.POID, &c.PONo,
		&c.Status, &c.CreditDate, &c.TotalCents,
		&c.GLJournalID, &c.PostedAt, &c.CreatedAt, &c.AppliedCents); err != nil {
		return VendorCredit{}, err
	}
	c.OpenCents = c.TotalCents - c.AppliedCents
	return c, nil
}

const vendorCreditFrom = `
	  FROM purchasing_vendor_credits c
	  LEFT JOIN purchasing_bills b ON b.id = c.bill_id
	  LEFT JOIN purchasing_pos p ON p.id = c.po_id`

// VendorCreditForUpdate reads one credit FOR UPDATE inside tx, the post's and the apply's lock.
func (r *Repository) VendorCreditForUpdate(ctx context.Context, tx pgx.Tx, id string) (VendorCredit, error) {
	if r.pool == nil {
		return VendorCredit{}, errNoDB
	}
	c, err := scanVendorCredit(r.q(tx).QueryRow(ctx, `SELECT `+vendorCreditCols+vendorCreditFrom+`
		 WHERE c.id = $1 FOR UPDATE OF c`, id))
	if err == pgx.ErrNoRows {
		return VendorCredit{}, errNotFound
	}
	if err != nil {
		return VendorCredit{}, fmt.Errorf("purchasing: vendor credit for update: %w", err)
	}
	return c, nil
}

// GetVendorCredit reads one credit with its lines and applications.
func (r *Repository) GetVendorCredit(ctx context.Context, id string) (VendorCreditDetail, error) {
	if r.pool == nil {
		return VendorCreditDetail{}, errNoDB
	}
	header, err := scanVendorCredit(r.pool.QueryRow(ctx, `SELECT `+vendorCreditCols+vendorCreditFrom+` WHERE c.id = $1`, id))
	if err == pgx.ErrNoRows {
		return VendorCreditDetail{}, errNotFound
	}
	if err != nil {
		return VendorCreditDetail{}, fmt.Errorf("purchasing: get vendor credit: %w", err)
	}
	detail := VendorCreditDetail{VendorCredit: header}
	detail.Lines, err = r.VendorCreditLinesTx(ctx, nil, id)
	if err != nil {
		return VendorCreditDetail{}, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT a.id::text, a.bill_id::text, COALESCE(b.bill_no::text,''), a.amount_cents, a.applied_at
		  FROM purchasing_vendor_credit_applications a
		  LEFT JOIN purchasing_bills b ON b.id = a.bill_id
		 WHERE a.credit_id = $1
		 ORDER BY a.applied_at, a.id`, id)
	if err != nil {
		return VendorCreditDetail{}, fmt.Errorf("purchasing: credit applications: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var app VendorCreditApplication
		if err := rows.Scan(&app.ID, &app.BillID, &app.BillNo, &app.AmountCents, &app.AppliedAt); err != nil {
			return VendorCreditDetail{}, fmt.Errorf("purchasing: scan credit application: %w", err)
		}
		detail.Applications = append(detail.Applications, app)
	}
	if err := rows.Err(); err != nil {
		return VendorCreditDetail{}, fmt.Errorf("purchasing: credit applications rows: %w", err)
	}
	return detail, nil
}

// VendorCreditLinesTx reads a credit's lines in order.
func (r *Repository) VendorCreditLinesTx(ctx context.Context, tx pgx.Tx, creditID string) ([]VendorCreditLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.q(tx).Query(ctx, `
		SELECT line_no, account_code, description, amount_cents
		  FROM purchasing_vendor_credit_lines WHERE credit_id = $1 ORDER BY line_no`, creditID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: credit lines: %w", err)
	}
	defer rows.Close()
	out := []VendorCreditLine{}
	for rows.Next() {
		var ln VendorCreditLine
		if err := rows.Scan(&ln.LineNo, &ln.AccountCode, &ln.Description, &ln.AmountCents); err != nil {
			return nil, fmt.Errorf("purchasing: scan credit line: %w", err)
		}
		out = append(out, ln)
	}
	return out, rows.Err()
}

func (r *Repository) SetVendorCreditPosted(ctx context.Context, tx pgx.Tx, id, journalID string) error {
	if r.pool == nil {
		return errNoDB
	}
	if _, err := r.q(tx).Exec(ctx, `
		UPDATE purchasing_vendor_credits SET status = 'POSTED', gl_journal_id = $2, posted_at = now(), updated_at = now()
		 WHERE id = $1 AND status = 'DRAFT'`, id, journalID); err != nil {
		return fmt.Errorf("purchasing: set vendor credit posted: %w", err)
	}
	return nil
}

// ListVendorCredits lists credits of one branch (empty branch = every branch the caller's reach
// named), narrowed by vendor and status when named.
func (r *Repository) ListVendorCredits(ctx context.Context, branchID, vendorID, status string) ([]VendorCredit, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+vendorCreditCols+vendorCreditFrom+`
		 WHERE ($1::uuid IS NULL OR c.branch_id = $1::uuid)
		   AND ($2::uuid IS NULL OR c.vendor_id = $2::uuid)
		   AND ($3::text IS NULL OR c.status = $3)
		 ORDER BY c.created_at DESC, c.id ASC`, nullStr(branchID), nullStr(vendorID), nullStr(status))
	if err != nil {
		return nil, fmt.Errorf("purchasing: list vendor credits: %w", err)
	}
	defer rows.Close()
	out := []VendorCredit{}
	for rows.Next() {
		c, err := scanVendorCredit(rows)
		if err != nil {
			return nil, fmt.Errorf("purchasing: scan vendor credit: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// InsertVendorCreditApplicationTx records one netting of a credit against a bill.
func (r *Repository) InsertVendorCreditApplicationTx(ctx context.Context, tx pgx.Tx, creditID, billID string, amountCents int64, key, appliedBy string, appliedAt time.Time) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	err := r.q(tx).QueryRow(ctx, `
		INSERT INTO purchasing_vendor_credit_applications (credit_id, bill_id, amount_cents, idempotency_key, applied_by, applied_at)
		VALUES ($1,$2,$3,$4,NULLIF($5,'')::uuid,$6)
		RETURNING id::text`,
		creditID, billID, amountCents, key, appliedBy, appliedAt).Scan(&id)
	if isUniqueViolation(err) {
		return "", conflict("purchasing: this credit application has already been recorded")
	}
	if err != nil {
		return "", fmt.Errorf("purchasing: insert credit application: %w", err)
	}
	return id, nil
}

// VendorCreditApplicationByKeyTx names the credit and bill an application key was spent on.
func (r *Repository) VendorCreditApplicationByKeyTx(ctx context.Context, tx pgx.Tx, key string) (creditID, billID string, found bool, err error) {
	if r.pool == nil {
		return "", "", false, errNoDB
	}
	err = r.q(tx).QueryRow(ctx, `
		SELECT credit_id::text, bill_id::text
		  FROM purchasing_vendor_credit_applications WHERE idempotency_key = $1`, key).
		Scan(&creditID, &billID)
	if err == pgx.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("purchasing: read the application this key named: %w", err)
	}
	return creditID, billID, true, nil
}

// AppliedCreditCentsTx returns the sum of a credit's applications within tx, read in its own
// statement so it sees every application committed before the caller's lock.
func (r *Repository) AppliedCreditCentsTx(ctx context.Context, tx pgx.Tx, creditID string) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var sum int64
	if err := r.q(tx).QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0)
		  FROM purchasing_vendor_credit_applications
		 WHERE credit_id = $1`, creditID,
	).Scan(&sum); err != nil {
		return 0, fmt.Errorf("purchasing: sum applied credit cents: %w", err)
	}
	return sum, nil
}

// AddVendorCreditAppliedTx moves the credit's applied_cents column by cents inside tx, under the
// row lock ApplyVendorCredit already holds. The column is the writer's tally, not a second copy
// the reads trust (the open guard still sums the application rows in its own statement); its
// CHECK, applied_cents <= total_cents (0468), is the database's own backstop. A path that somehow
// passed the open guard cannot commit an over application: this update fails the CHECK and the
// whole transaction rolls back, application row and all.
func (r *Repository) AddVendorCreditAppliedTx(ctx context.Context, tx pgx.Tx, creditID, creditNo string, cents int64) error {
	if r.pool == nil {
		return errNoDB
	}
	if _, err := r.q(tx).Exec(ctx, `
		UPDATE purchasing_vendor_credits
		   SET applied_cents = applied_cents + $2, updated_at = now()
		 WHERE id = $1`, creditID, cents); err != nil {
		if isCheckViolation(err) {
			return conflict("purchasing: the database refused the application: credit note %s's applied total would pass its total", creditNo)
		}
		return fmt.Errorf("purchasing: move the applied total on credit note %s: %w", creditNo, err)
	}
	return nil
}

// CreditedBillCentsTx returns the sum of every application's cents on billID within tx, the part of
// the bill's open the vendor's credits have consumed. PayBill's overpay guard reads it beside the
// paid instalments: a bill whose open a credit has covered must not be paid again on top.
func (r *Repository) CreditedBillCentsTx(ctx context.Context, tx pgx.Tx, billID string) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var sum int64
	if err := r.q(tx).QueryRow(ctx, `
		SELECT COALESCE(SUM(a.amount_cents), 0)
		  FROM purchasing_vendor_credit_applications a
		  JOIN purchasing_vendor_credits c ON c.id = a.credit_id
		 WHERE a.bill_id = $1 AND c.status = 'POSTED'`, billID,
	).Scan(&sum); err != nil {
		return 0, fmt.Errorf("purchasing: sum credited bill cents: %w", err)
	}
	return sum, nil
}

// - the aging's own reads -

// apCreditCandidate is one posted vendor credit as the aging reads it: its open is what the aging
// shows as the vendor's negative item, and its applications are what have already moved onto bills.
type apCreditCandidate struct {
	CreditID    string
	CreditNo    string
	VendorID    string
	VendorNo    string
	VendorName  string
	BranchID    string
	CreditDate  string
	TotalCents  int64
	GLJournalID string
	// AppliedBills, AppliedAts and AppliedAmounts are the credit's applications, aligned by index.
	// Which count as of an instant is each application's own applied_at, and the credit counts from
	// its posting journal's effective instant, the bills' own rule.
	AppliedBills   []string
	AppliedAts     []time.Time
	AppliedAmounts []int64
}

// OpenVendorCreditsAsOf reads posted vendor credits in reach with their applications. Like
// OpenBillsAsOf, the query carries no as-of predicate of its own: the caller's steps decide what
// counts as of the instant.
func (r *Repository) OpenVendorCreditsAsOf(ctx context.Context, reach BranchReach) ([]apCreditCandidate, error) {
	if r == nil || r.pool == nil {
		return nil, errNoDB
	}
	if !reach.Global && len(reach.BranchIDs) == 0 {
		return []apCreditCandidate{}, nil
	}
	branchIDs := reach.BranchIDs
	if branchIDs == nil {
		branchIDs = []string{}
	}

	rows, err := r.pool.Query(ctx, `
		SELECT c.id::text, c.credit_no, c.vendor_id::text,
		       v.vendor_no::text, v.name, c.branch_id::text,
		       to_char(c.credit_date, 'YYYY-MM-DD'), c.total_cents,
		       COALESCE(c.gl_journal_id::text, ''),
		       COALESCE((SELECT array_agg(a.bill_id::text ORDER BY a.applied_at, a.id)
		           FROM purchasing_vendor_credit_applications a
		          WHERE a.credit_id = c.id), '{}'),
		       COALESCE((SELECT array_agg(a.applied_at ORDER BY a.applied_at, a.id)
		           FROM purchasing_vendor_credit_applications a
		          WHERE a.credit_id = c.id), '{}'),
		       COALESCE((SELECT array_agg(a.amount_cents ORDER BY a.applied_at, a.id)
		           FROM purchasing_vendor_credit_applications a
		          WHERE a.credit_id = c.id), '{}')
		  FROM purchasing_vendor_credits c
		  JOIN vendors v ON v.id = c.vendor_id
		 WHERE c.status = 'POSTED'
		   AND ($1::boolean OR c.branch_id = ANY($2::uuid[]))
		 ORDER BY v.name ASC, v.id ASC, c.credit_date ASC, c.credit_no ASC, c.id ASC`,
		reach.Global, branchIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("purchasing: open vendor credits as of: %w", err)
	}
	defer rows.Close()

	out := make([]apCreditCandidate, 0)
	for rows.Next() {
		var c apCreditCandidate
		if err := rows.Scan(
			&c.CreditID, &c.CreditNo, &c.VendorID,
			&c.VendorNo, &c.VendorName, &c.BranchID,
			&c.CreditDate, &c.TotalCents, &c.GLJournalID,
			&c.AppliedBills, &c.AppliedAts, &c.AppliedAmounts,
		); err != nil {
			return nil, fmt.Errorf("purchasing: scan open vendor credit as of: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("purchasing: open vendor credits rows: %w", err)
	}
	return out, nil
}
