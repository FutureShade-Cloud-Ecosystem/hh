package warranty

import (
	"context"
	"sort"
	"strings"
	"time"
)

// recovery.go, DIB-221 WTY-RECOVERY: the outstanding-money recovery worklist.
//
// THE PROBLEM IN ONE SENTENCE. Roughly $40k sits in live claims where a vendor AGREED to a credit that never
// landed, and until now agreeing and receiving were the same column.
//
// THE ARITHMETIC, ONCE.
//
//	outstanding = accrued − received
//
// accrued is what the settlement's VENDOR_RECOVERY path put on the books (0129, warranty_settlement_postings
// where accrues_recovery). received is the sum of RECEIVED rows in the chase log (0130). Both live in SQL, in
// the warranty_recovery_balances view, so there is exactly one definition and no cached balance to drift. The
// Go in this file does aging, grouping and the reconciliation RULES, never the subtraction twice.
//
// AGING IS MEASURED FROM THE ACCRUAL, NOT THE CLAIM. A claim opened in January and settled in June has been
// OWED since June. Aging the receivable from January would put every long-running claim in the 90+ bucket on
// the day it settled and make the worst bucket meaningless.
//
// RECEIVED RECONCILES AGAINST THE CLAIM. A receipt cannot exceed what was accrued. That is not fussiness: an
// over-receipt is either a keying error or a credit that belongs to a different claim, and silently accepting
// it produces a NEGATIVE outstanding that nets against another vendor's real balance in the roll-up and hides
// money the business is still owed.

// - DTOs -

// RecoveryRow is one claim's recovery position, straight off the 0130 view.
type RecoveryRow struct {
	ClaimID          string `json:"claim_id"`
	ClaimNo          string `json:"claim_no"`
	BranchID         string `json:"branch_id"`
	VendorID         string `json:"vendor_id,omitempty"`
	VendorName       string `json:"vendor_name,omitempty"`
	AccruedCents     int64  `json:"accrued_cents"`
	ReceivedCents    int64  `json:"received_cents"`
	OutstandingCents int64  `json:"outstanding_cents"`
	PromisedCents    int64  `json:"promised_cents"`
	AccruedAt        string `json:"accrued_at,omitempty"`
	LastActionAt     string `json:"last_action_at,omitempty"`
	ActionCount      int    `json:"action_count"`
	AgeDays          int    `json:"age_days"`
	Bucket           string `json:"bucket"` // 0-30 | 31-60 | 61-90 | 90+
	Reconciled       bool   `json:"reconciled"`
}

// RecoveryAction is one row of the chase log.
type RecoveryAction struct {
	ID           string `json:"id,omitempty"`
	ClaimID      string `json:"claim_id"`
	VendorID     string `json:"vendor_id,omitempty"`
	Action       string `json:"action"` // CONTACT_MADE | PROMISED | RECEIVED | DISPUTED | NOTE
	AmountCents  int64  `json:"amount_cents"`
	PromisedDate string `json:"promised_date,omitempty"`
	Reference    string `json:"reference,omitempty"`
	Note         string `json:"note"`
	ActorID      string `json:"actor_id,omitempty"`
	ActorName    string `json:"actor_name,omitempty"`
	OccurredAt   string `json:"occurred_at,omitempty"`
}

// RecoveryActionInput is the log-an-action payload.
type RecoveryActionInput struct {
	Action       string `json:"action"`
	AmountCents  int64  `json:"amount_cents"`
	PromisedDate string `json:"promised_date"`
	Reference    string `json:"reference"`
	Note         string `json:"note"`
	OccurredAt   string `json:"occurred_at"` // back-datable, symmetric with capture/advance (FR-9)
}

// VendorRecovery is one vendor's roll-up: the per-vendor outstanding balance with its aging split.
type VendorRecovery struct {
	VendorID         string           `json:"vendor_id"`
	VendorName       string           `json:"vendor_name"`
	ClaimCount       int              `json:"claim_count"`
	AccruedCents     int64            `json:"accrued_cents"`
	ReceivedCents    int64            `json:"received_cents"`
	OutstandingCents int64            `json:"outstanding_cents"`
	PromisedCents    int64            `json:"promised_cents"`
	Buckets          map[string]int64 `json:"buckets"` // bucket → outstanding cents
	OldestDays       int              `json:"oldest_days"`
	Claims           []RecoveryRow    `json:"claims"`
}

// RecoveryBoard is the whole WTY-RECOVERY payload the board reads in one call.
type RecoveryBoard struct {
	Vendors               []VendorRecovery `json:"vendors"`
	Buckets               map[string]int64 `json:"buckets"`
	TotalAccruedCents     int64            `json:"total_accrued_cents"`
	TotalReceivedCents    int64            `json:"total_received_cents"`
	TotalOutstandingCents int64            `json:"total_outstanding_cents"`
	TotalPromisedCents    int64            `json:"total_promised_cents"`
	OpenClaimCount        int              `json:"open_claim_count"`
	ReconciledClaimCount  int              `json:"reconciled_claim_count"`
	AsOf                  string           `json:"as_of"`
}

// RecoveryBucketOrder is the render order for the aging columns.
var RecoveryBucketOrder = []string{"0-30", "31-60", "61-90", "90+"}

// recoveryActions are the legal chase-log verbs (mirrors the 0130 CHECK).
const (
	ActionContactMade = "CONTACT_MADE"
	ActionPromised    = "PROMISED"
	ActionReceived    = "RECEIVED"
	ActionDisputed    = "DISPUTED"
	ActionNote        = "NOTE"
)

// moneyActions are the two verbs that must carry an amount (mirrors the 0130 CHECK).
func moneyAction(a string) bool { return a == ActionPromised || a == ActionReceived }

func knownRecoveryAction(a string) bool {
	switch a {
	case ActionContactMade, ActionPromised, ActionReceived, ActionDisputed, ActionNote:
		return true
	}
	return false
}

// - pure board math (DB-free, exhaustively unit-testable) -

// BuildRecoveryBoard turns the per-claim view rows into the board: aging each row from its ACCRUAL date, rolling
// up per vendor, and totalling. Kept pure so the arithmetic the client's $40k depends on is testable without a
// database and cannot quietly acquire a query.
func BuildRecoveryBoard(rows []RecoveryRow, now time.Time) RecoveryBoard {
	board := RecoveryBoard{
		Vendors: []VendorRecovery{},
		Buckets: map[string]int64{},
		AsOf:    now.UTC().Format(time.RFC3339),
	}
	for _, b := range RecoveryBucketOrder {
		board.Buckets[b] = 0
	}
	byVendor := map[string]*VendorRecovery{}
	for _, r := range rows {
		r.AgeDays = accrualAgeDays(r.AccruedAt, now)
		r.Bucket = ageBucket(r.AgeDays)
		r.Reconciled = r.OutstandingCents <= 0

		board.TotalAccruedCents += r.AccruedCents
		board.TotalReceivedCents += r.ReceivedCents
		board.TotalOutstandingCents += r.OutstandingCents
		board.TotalPromisedCents += r.PromisedCents
		if r.Reconciled {
			board.ReconciledClaimCount++
		} else {
			board.OpenClaimCount++
			// Only OUTSTANDING money ages. A fully-received claim contributes nothing to a bucket, the bucket
			// answers "how old is the money still owed", and a settled receipt is not owed.
			board.Buckets[r.Bucket] += r.OutstandingCents
		}

		key := r.VendorID
		if key == "" {
			key = "" // UNKNOWN vendor pending triage, grouped, never dropped: unattributed money is still money
		}
		v, ok := byVendor[key]
		if !ok {
			name := r.VendorName
			if name == "" {
				name = "(vendor not yet identified)"
			}
			v = &VendorRecovery{VendorID: r.VendorID, VendorName: name, Buckets: map[string]int64{}}
			for _, b := range RecoveryBucketOrder {
				v.Buckets[b] = 0
			}
			byVendor[key] = v
		}
		v.ClaimCount++
		v.AccruedCents += r.AccruedCents
		v.ReceivedCents += r.ReceivedCents
		v.OutstandingCents += r.OutstandingCents
		v.PromisedCents += r.PromisedCents
		if !r.Reconciled {
			v.Buckets[r.Bucket] += r.OutstandingCents
			if r.AgeDays > v.OldestDays {
				v.OldestDays = r.AgeDays
			}
		}
		v.Claims = append(v.Claims, r)
	}
	for _, v := range byVendor {
		sort.Slice(v.Claims, func(i, j int) bool {
			if v.Claims[i].AgeDays != v.Claims[j].AgeDays {
				return v.Claims[i].AgeDays > v.Claims[j].AgeDays // oldest first, that is the worklist order
			}
			return v.Claims[i].ClaimNo < v.Claims[j].ClaimNo
		})
		board.Vendors = append(board.Vendors, *v)
	}
	// Worklist order: biggest outstanding first, then oldest, then name, a rep works down this list.
	sort.Slice(board.Vendors, func(i, j int) bool {
		a, b := board.Vendors[i], board.Vendors[j]
		if a.OutstandingCents != b.OutstandingCents {
			return a.OutstandingCents > b.OutstandingCents
		}
		if a.OldestDays != b.OldestDays {
			return a.OldestDays > b.OldestDays
		}
		return a.VendorName < b.VendorName
	})
	return board
}

// accrualAgeDays is how long the money has been OWED: days since the accrual posted (never since the claim
// opened). A missing/unparseable accrual date ages as 0 rather than exploding a board read.
func accrualAgeDays(accruedAt string, now time.Time) int {
	if strings.TrimSpace(accruedAt) == "" {
		return 0
	}
	t, err := parseFlexTime(accruedAt)
	if err != nil {
		return 0
	}
	d := int(now.Sub(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// - service -

// RecoveryBoardView builds the WTY-RECOVERY board for the caller's reachable branches.
//
// SCOPE. A branch-scoped caller passes their branch; a GLOBAL caller may pass "" for everything. An explicit
// branch a scoped caller cannot reach is refused, the same horizontal-access rule every other warranty read
// obeys, applied here because outstanding vendor money is exactly the sort of thing worth iterating for.
func (s *Service) RecoveryBoardView(ctx context.Context, branchID, vendorID string, openOnly bool) (RecoveryBoard, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return RecoveryBoard{}, err
	}
	if branchID != "" && !caller.CanAccessBranch(branchID) {
		return RecoveryBoard{}, forbidden("warranty: caller cannot access branch %s", branchID)
	}
	rows, err := s.store.RecoveryRows(ctx, branchID, vendorID, openOnly)
	if err != nil {
		return RecoveryBoard{}, err
	}
	// A scoped caller who asked for "everything" gets everything THEY can reach, not everything that exists.
	if branchID == "" {
		kept := rows[:0]
		for _, r := range rows {
			if caller.CanAccessBranch(r.BranchID) {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	return BuildRecoveryBoard(rows, s.clock()), nil
}

// LogRecoveryAction records one chase-log entry against a claim and, for a RECEIVED action, reconciles it
// against what the claim actually accrued.
//
// THE RECONCILIATION RULE. received_total + this_receipt must not exceed accrued. Over-receipt is refused with
// the numbers in the message (what was accrued, what is already received, what is left) because the person
// keying it is holding a credit note and needs to know whether they are on the wrong claim.
func (s *Service) LogRecoveryAction(ctx context.Context, claimID string, in RecoveryActionInput) (RecoveryAction, error) {
	caller, claim, err := s.callerScopedClaim(ctx, claimID)
	if err != nil {
		return RecoveryAction{}, err
	}
	action := strings.ToUpper(strings.TrimSpace(in.Action))
	if !knownRecoveryAction(action) {
		return RecoveryAction{}, unprocessable("warranty: action must be one of CONTACT_MADE, PROMISED, RECEIVED, DISPUTED, NOTE")
	}
	if in.AmountCents < 0 {
		return RecoveryAction{}, unprocessable("warranty: amount_cents must be >= 0")
	}
	if moneyAction(action) && in.AmountCents <= 0 {
		return RecoveryAction{}, unprocessable("warranty: a %s action must carry a positive amount_cents", action)
	}
	if !moneyAction(action) && in.AmountCents != 0 {
		// The 0130 CHECK would refuse this anyway; a 422 with a sentence beats a constraint violation, and
		// silently zeroing it would look like a lost receipt.
		return RecoveryAction{}, unprocessable("warranty: a %s action cannot carry an amount, use RECEIVED or PROMISED", action)
	}

	bal, err := s.store.RecoveryBalanceForClaim(ctx, claimID)
	if err != nil {
		return RecoveryAction{}, err
	}
	if bal.ClaimID == "" {
		return RecoveryAction{}, unprocessable(
			"warranty: claim %s has no recovery accrual, only a claim settled to the vendor-recovery money path can be chased", claim.ClaimNo)
	}
	if action == ActionReceived {
		remaining := bal.AccruedCents - bal.ReceivedCents
		if in.AmountCents > remaining {
			return RecoveryAction{}, unprocessable(
				"warranty: receipt of %d cents exceeds the outstanding balance on claim %s (accrued %d, already received %d, outstanding %d)",
				in.AmountCents, claim.ClaimNo, bal.AccruedCents, bal.ReceivedCents, remaining)
		}
	}

	occurred := s.clock()
	if strings.TrimSpace(in.OccurredAt) != "" {
		t, perr := parseFlexTime(in.OccurredAt)
		if perr != nil {
			return RecoveryAction{}, unprocessable("warranty: occurred_at must be an ISO-8601 date/time")
		}
		if t.After(s.clock().Add(24 * time.Hour)) {
			return RecoveryAction{}, unprocessable("warranty: occurred_at cannot be in the future")
		}
		occurred = t
	}
	var promised string
	if strings.TrimSpace(in.PromisedDate) != "" {
		if _, perr := parseFlexTime(in.PromisedDate); perr != nil {
			return RecoveryAction{}, unprocessable("warranty: promised_date must be a YYYY-MM-DD date")
		}
		promised = strings.TrimSpace(in.PromisedDate)
	}

	a := RecoveryAction{
		ClaimID:      claimID,
		VendorID:     claim.VendorID,
		Action:       action,
		AmountCents:  in.AmountCents,
		PromisedDate: promised,
		Reference:    strings.TrimSpace(in.Reference),
		Note:         in.Note,
		ActorID:      caller.UserID,
		OccurredAt:   occurred.Format(time.RFC3339),
	}
	id, err := s.store.InsertRecoveryAction(ctx, a)
	if err != nil {
		return RecoveryAction{}, err
	}
	a.ID = id

	// Mirror the action onto the claim's audit spine as a NOTE so the claim timeline, the one place a person
	// looks to answer "what happened to this claim", does not silently omit the chase.
	if _, eerr := s.store.InsertEvent(ctx, ClaimEvent{
		ClaimID:   claimID,
		EventType: "NOTE",
		ActorID:   caller.UserID,
		Note:      recoveryEventNote(a),
		Detail: map[string]any{
			"recovery_action": action,
			"amount_cents":    in.AmountCents,
			"reference":       a.Reference,
		},
		OccurredAt: a.OccurredAt,
	}); eerr != nil {
		s.log.Warn("warranty: mirroring the recovery action to the claim timeline failed", "claim", claim.ClaimNo, "err", eerr)
	}
	return a, nil
}

// recoveryEventNote renders the timeline sentence for a chase-log action.
func recoveryEventNote(a RecoveryAction) string {
	switch a.Action {
	case ActionReceived:
		return "Recovery received: " + centsLabel(a.AmountCents) + refSuffix(a.Reference)
	case ActionPromised:
		return "Vendor promised " + centsLabel(a.AmountCents) + promisedSuffix(a.PromisedDate)
	case ActionContactMade:
		return "Recovery contact made" + noteSuffix(a.Note)
	case ActionDisputed:
		return "Vendor disputed the claim" + noteSuffix(a.Note)
	default:
		return "Recovery note" + noteSuffix(a.Note)
	}
}

func centsLabel(c int64) string {
	neg := ""
	if c < 0 {
		neg, c = "-", -c
	}
	return neg + "$" + itoaCents(c)
}

// itoaCents renders integer cents as dollars.cents WITHOUT float arithmetic, money never round-trips a float.
func itoaCents(c int64) string {
	d := c / 100
	r := c % 100
	tens := byte('0' + r/10)
	ones := byte('0' + r%10)
	return itoa64(d) + "." + string([]byte{tens, ones})
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func refSuffix(ref string) string {
	if ref == "" {
		return ""
	}
	return " (ref " + ref + ")"
}

func promisedSuffix(d string) string {
	if d == "" {
		return ""
	}
	return " by " + d
}

func noteSuffix(n string) string {
	n = strings.TrimSpace(n)
	if n == "" {
		return ""
	}
	return ": " + n
}

// RecoveryActions returns a claim's chase log, oldest first (branch-scoped like every other claim read).
func (s *Service) RecoveryActions(ctx context.Context, claimID string) ([]RecoveryAction, error) {
	if _, _, err := s.callerScopedClaim(ctx, claimID); err != nil {
		return nil, err
	}
	return s.store.RecoveryActionsForClaim(ctx, claimID)
}
