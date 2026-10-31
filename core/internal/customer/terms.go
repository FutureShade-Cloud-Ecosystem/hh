package customer

// terms.go, the TERMS MASTER (wave 3, TM-1).
//
// The dealer sells on terms: a code, a name, the net days an invoice is due in, and the early
// payment discount's rate and window. The master is this module's table (migration 0466) and a
// customer names the term it is sold on by its code.
//
// The six standard terms are seeded by the migration: RECEIPT (due on receipt), NET15, NET30,
// NET45, 1-10-NET30 (one percent off within ten days) and 2-10-NET30 (two percent off within
// ten days).
//
// A customer whose own days match no standard term keeps those days as a CUSTOM NET, named in
// words ("Net 60"), never a silent default of some other term. The days stay the flat mirror
// every reader that counts days still reads; the code is the term, and the code is what the
// invoice is dated by.
//
// TermsFor is the read the later waves use: the terms one customer is sold on. A job's own terms
// (JA-1) join through this same read, so a job that carries terms of its own is dated by them
// and a job that carries none is dated by its customer's.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Terms is one row of the terms master.
type Terms struct {
	// Code is the stable name the customer and the invoice carry.
	Code string `json:"code"`
	// Name is what the staff reads.
	Name string `json:"name"`
	// NetDays is the number of house days the invoice is due in. 0 is due on receipt.
	NetDays int `json:"net_days"`
	// DiscountBps is the early payment discount's rate in basis points (100 is one percent).
	// 0 is no discount.
	DiscountBps int `json:"discount_bps"`
	// DiscountDays is the window the discount runs for, in house days from the invoice's own
	// issue day. 0 is no window.
	DiscountDays int `json:"discount_days"`
}

// The codes the master is seeded with.
const (
	TermsReceipt   = "RECEIPT"
	TermsNet15     = "NET15"
	TermsNet30     = "NET30"
	TermsNet45     = "NET45"
	Terms1PctNet30 = "1-10-NET30"
	Terms2PctNet30 = "2-10-NET30"
)

// customTermsPrefix is the prefix a custom net's code carries. A customer on days no standard
// term names keeps its days and takes a code of its own.
const customTermsPrefix = "CUSTOM-NET-"

// standardTerms is the dealer's standard terms: the code, the name the master carries for it,
// and the net days. The names here are the names migration 0466 seeds, and
// TestIntegration_TermsFor_TheSeededMasterAndTheMappingAgree pins the two together.
var standardTerms = []struct {
	Code string
	Name string
	Days int
}{
	{TermsReceipt, "Due on receipt", 0},
	{TermsNet15, "Net 15", 15},
	{TermsNet30, "Net 30", 30},
	{TermsNet45, "Net 45", 45},
	{Terms1PctNet30, "1/10 net 30", 30},
	{Terms2PctNet30, "2/10 net 30", 30},
}

// netCodeForDays maps a customer's own days to the standard term for those days. Only the four
// terms whose days are their own are reachable this way: the two discount terms share net 30
// with NET30 and are reached by their code, never by their days.
var netCodeForDays = map[int]string{
	0:  TermsReceipt,
	15: TermsNet15,
	30: TermsNet30,
	45: TermsNet45,
}

// TermsForDays is the mapping from a customer's own days to the term they carry: the standard
// term when the days name one, and a custom net in words when they do not.
func TermsForDays(days int) (code string, name string) {
	if c, ok := netCodeForDays[days]; ok {
		return c, termName(c)
	}
	return CustomTermsCode(days), CustomTermsName(days)
}

// CustomTermsCode is the code a customer's own days take when no standard term names them.
func CustomTermsCode(days int) string { return fmt.Sprintf("%s%d", customTermsPrefix, days) }

// CustomTermsName is the name that code carries: the days in words, never another term's name.
func CustomTermsName(days int) string { return fmt.Sprintf("Net %d", days) }

// termName is the name the master carries for a standard code.
func termName(code string) string {
	for _, t := range standardTerms {
		if t.Code == code {
			return t.Name
		}
	}
	return code
}

// termsNameFor is the name the master carries for a code: a standard term's own name, and a
// custom net's name in words.
func termsNameFor(code string, days int) string {
	if strings.HasPrefix(code, customTermsPrefix) {
		return CustomTermsName(days)
	}
	return termName(code)
}

// termsReader is the terms seam of this module: the terms a customer is sold on, and one row of
// the master by its code.
//
// It is a SEPARATE, optional interface rather than a widened accountStore on purpose:
// accountStore is implemented outside this package (internal/portal composes the Service with
// its own fake store) and widening it would be a breaking change to a module this swarm does
// not own. *Repository implements it, so the real deployment always takes the master's path.
type termsReader interface {
	TermsFor(ctx context.Context, customerID string) (Terms, error)
	TermsByCode(ctx context.Context, code string) (Terms, error)
}

// termsWriter is the optional write seam for the customer's terms code. It is separate from
// customerWriter for the same reason termsReader is separate from accountStore.
type termsWriter interface {
	SetTerms(ctx context.Context, customerID string, t Terms) (Account, error)
}

// TermsFor returns the terms one customer is sold on: the row the customer's terms code names,
// and the customer's own days as the fallback for a customer that carries no code.
//
// It is the read the later waves use. The invoice writer dates a document by these terms, and a
// job's own terms (JA-1) join through this same read.
func (s *Service) TermsFor(ctx context.Context, customerID string) (Terms, error) {
	if customerID == "" {
		return Terms{}, httpx.BadRequestf("customer id is required")
	}
	r, ok := s.store.(termsReader)
	if !ok {
		return Terms{}, httpx.BadRequestf("customer terms not configured")
	}
	return r.TermsFor(ctx, customerID)
}

// TermsByCode returns one row of the master by its code. ErrNotFound when no term carries it.
func (s *Service) TermsByCode(ctx context.Context, code string) (Terms, error) {
	if code == "" {
		return Terms{}, httpx.BadRequestf("terms code is required")
	}
	r, ok := s.store.(termsReader)
	if !ok {
		return Terms{}, httpx.BadRequestf("customer terms not configured")
	}
	return r.TermsByCode(ctx, code)
}

// SetTerms puts a customer on a terms code of the master.
//
// The code must name a term that exists: a customer is never put on a code that points nowhere,
// and the flat days mirror follows the term's own net days, so the readers that still count days
// and the readers that read the terms cannot disagree.
//
// An unknown code is refused in words rather than stored, because a customer on a code nobody
// can read is a customer whose due date is a guess.
func (s *Service) SetTerms(ctx context.Context, customerID string, code string) (Account, error) {
	if s.writer == nil {
		return Account{}, httpx.BadRequestf("customer write path not configured")
	}
	if customerID == "" {
		return Account{}, httpx.BadRequestf("customer id is required")
	}
	if code == "" {
		return Account{}, httpx.BadRequestf("terms code is required")
	}
	w, ok := s.writer.(termsWriter)
	if !ok {
		return Account{}, httpx.BadRequestf("customer terms write not configured")
	}
	t, err := s.TermsByCode(ctx, code)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Account{}, httpx.BadRequestf("unknown terms code %q", code)
		}
		return Account{}, err
	}
	return w.SetTerms(ctx, customerID, t)
}

// TermsFor reads the terms one customer is sold on.
//
// The customer's terms code names the row in the master. A customer that carries no code on
// standard days joins the standard master row for those days; with no code and non-standard
// days, TermsFor answers the days with no code. TermsFor stays a read and never writes.
func (r *Repository) TermsFor(ctx context.Context, customerID string) (Terms, error) {
	// A caller inside its own transaction (an invoice written in the caller's one transaction,
	// QD5) carries it with WithCallerTx, and the read rides it: a second pool connection would
	// wait on the caller's own, and on a pool of one it never comes.
	var q interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	}
	if tx := callerTxFrom(ctx); tx != nil {
		q = tx
	} else if r.pool != nil {
		q = r.pool
	} else {
		return Terms{}, errNoDB
	}
	var t Terms
	err := q.QueryRow(ctx, `
		SELECT COALESCE(t.code, ''), COALESCE(t.name, ''), COALESCE(t.net_days, c.payment_terms_days),
		       COALESCE(t.discount_bps, 0), COALESCE(t.discount_days, 0)
		  FROM customers c
		  LEFT JOIN payment_terms t
		    ON t.code = COALESCE(c.terms_code, CASE c.payment_terms_days
		                  WHEN 0  THEN 'RECEIPT'
		                  WHEN 15 THEN 'NET15'
		                  WHEN 30 THEN 'NET30'
		                  WHEN 45 THEN 'NET45'
		                END)
		 WHERE c.id = $1`,
		customerID).Scan(&t.Code, &t.Name, &t.NetDays, &t.DiscountBps, &t.DiscountDays)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Terms{}, ErrNotFound
		}
		return Terms{}, fmt.Errorf("read the terms of customer %q: %w", customerID, err)
	}
	return t, nil
}

// TermsByCode reads one row of the master by its code. A caller inside its own transaction (a cash
// application discounting inside its one transaction, TM-2) carries it with WithCallerTx and the
// read rides it, the same discipline TermsFor rides (QD5: a pool read beside a held transaction
// waits on a second connection, which a full pool of such callers never frees).
func (r *Repository) TermsByCode(ctx context.Context, code string) (Terms, error) {
	var q interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	}
	if tx := callerTxFrom(ctx); tx != nil {
		q = tx
	} else if r.pool != nil {
		q = r.pool
	} else {
		return Terms{}, errNoDB
	}
	var t Terms
	err := q.QueryRow(ctx, `
		SELECT code, name, net_days, discount_bps, discount_days
		  FROM payment_terms
		 WHERE code = $1`,
		code).Scan(&t.Code, &t.Name, &t.NetDays, &t.DiscountBps, &t.DiscountDays)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Terms{}, ErrNotFound
		}
		return Terms{}, fmt.Errorf("read the terms %q: %w", code, err)
	}
	return t, nil
}

// SetTerms stores the customer's terms code and keeps the flat days mirror in step with the
// term's own net days.
func (r *Repository) SetTerms(ctx context.Context, customerID string, t Terms) (Account, error) {
	if r.pool == nil {
		return Account{}, errNoDB
	}
	// The customers counter first, without advancing it, then the row (a load batch holds the
	// counter before its item rows, LD-5; PR 556 round 3e). The same discipline the address
	// update takes, because it is the same table and the same rule.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Account{}, fmt.Errorf("set the terms of customer %q: %w", customerID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := LockCustomersSeqTx(ctx, tx); err != nil {
		return Account{}, err
	}
	var id string
	err = tx.QueryRow(ctx, `
		UPDATE customers
		   SET terms_code = $2, payment_terms_days = $3, updated_at = now()
		 WHERE id = $1
		RETURNING id::text`,
		customerID, t.Code, t.NetDays).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Account{}, ErrNotFound
		}
		return Account{}, fmt.Errorf("set the terms of customer %q: %w", customerID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Account{}, fmt.Errorf("set the terms of customer %q: %w", customerID, err)
	}
	return r.ResolveAccount(ctx, id)
}

// ensureTermsRow makes sure the term the customer's code names is a row in the master, so the
// code a customer carries always names a term that exists.
//
// It runs on the caller's own transaction when there is one: the code is written beside the
// days in the same statement that writes the customer, and the foreign key on that column is
// checked as the statement lands, so the row has to be there first.
func ensureTermsRow(ctx context.Context, e termsExecer, code, name string, days int) error {
	if _, err := e.Exec(ctx, `
		INSERT INTO payment_terms (code, name, net_days)
		VALUES ($1, $2, $3)
		ON CONFLICT (code) DO NOTHING`,
		code, name, days); err != nil {
		return fmt.Errorf("ensure the terms row %q: %w", code, err)
	}
	return nil
}

// termsExecer is what a terms row is written through: the pool, or a caller's transaction.
type termsExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}
