// loadwriter.go is the customer module's write path for the load doors'
// customer and contact items (HH-LOAD-1 LD-6). The customers and
// customer_contacts tables are this module's (0006), so both writers
// live here and the load frame hands each item inside its own batch
// transaction, taken by constructor at the wiring. The staff create
// route (POST /api/v1/customers) is untouched: it stamps no org, sequence
// or key and answers 409 on a taken account number, which is why this
// path exists.
//
// A customer upserts by its account number (the table's own natural key,
// the item's legacy id unless the record names another), so a load item
// whose account number a staff member already made attaches to that row
// and stamps it the load's own. The billing address runs the same
// normalisation for the dealer's country the staff write runs; an address that fails is
// left out and the item's note says so, naming the field and the
// reason, never a value. The home branch and the reps arrive as
// references by legacy key, resolved by the frame before any write.
//
// A contact carries no org column of its own (0006, absent from 0213):
// its org rides through its customer, so the writer lands a contact only
// under a customer of the machine principal's org.
//
// The writers' rules, binding: they open no connection of their own, they
// never commit or roll back the transaction they are given (the frame's
// savepoint protocol owns that decision), and their errors wrap the
// statement's own (%w) so a Postgres deadlock stays recognisable at the
// batch boundary. Refusals come back as *load.Refusal and carry codes
// and fields, never a record value.
package customer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/localization"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ReasonAddressLeftOut is the note a landed customer carries when its
// billing address failed the dealer's country validation and was left out.
const ReasonAddressLeftOut = "address_left_out"

// customerSeqCTE draws the seq atomically in the same statement from the
// customers scope, the scope the module's own customers pull reads rows
// through.
const customerSeqCTE = `WITH cseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('customers')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// LockCustomersSeqTx takes the customers counter row WITHOUT advancing it, on the caller's transaction: the upsert writes the
// value it already holds. The load door's claim transaction holds this counter from an item's first draw to the
// batch commit and then wants item rows (LD-5, which closes batch against batch), so the counter comes BEFORE the
// rows for every writer of this table. A single row REST writer that relies on the sync seq trigger would take
// the row first and the counter after, and cross a batch holding the counter while it waits for that row (PR 556
// round 3e). Every trigger relying writer of this table takes this first; the catalogue's check (h) says so.
func LockCustomersSeqTx(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO scope_sequences (scope, next_value) VALUES ('customers', 0)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value`); err != nil {
		return fmt.Errorf("customer: lock the customers counter: %w", err)
	}
	return nil
}

// CustomerLoadWriter is the load doors' customer writer.
type CustomerLoadWriter struct {
	// Country is the dealer's country source; nil is the Canadian dealer.
	Country CountrySource
}

// country is the dealer's country for the address rules; a failed read surfaces, never a guess.
func (w CustomerLoadWriter) country(ctx context.Context) (string, error) {
	return dealerCountry(ctx, w.Country)
}

// deferredCustomerFields wait for the receivables load: a credit hold and
// tax exemption need their evidence, and neither lands here.
var deferredCustomerFields = []string{"on_hold", "tax_exempt"}

// Apply answers one customer item. The record names the name (required)
// and, named, the account number, the customer type, the terms in days,
// the credit limit, the active flag, and the billing address.
func (w CustomerLoadWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	for _, f := range deferredCustomerFields {
		if _, present := in.Record[f]; present {
			return "", &load.Refusal{Code: "deferred_field", Field: f,
				Detail: "this field waits for the receivables load; it is not loaded here"}
		}
	}
	name, _ := in.Record["name"].(string)
	if name == "" {
		return "", &load.Refusal{Code: "field_required", Field: "name",
			Detail: "the record names no customer name"}
	}
	// The account number the record names; empty when it names none. The
	// create arm falls back to the legacy id, the update arm keeps the
	// stored number, so a reload that stops naming it never rewrites it.
	accountNo, _ := in.Record["account_no"].(string)
	custType := "CONTRACTOR"
	if v, ok := in.Record["customer_type"]; ok && v != nil {
		s, _ := v.(string)
		if !validCustomerType(s) {
			return "", &load.Refusal{Code: "record_shape", Field: "customer_type",
				Detail: "the customer type is not one the customer table keeps"}
		}
		custType = s
	}
	terms := 30
	if v, ok := in.Record["payment_terms_days"]; ok && v != nil {
		n, whole := wholeNumber(v)
		if !whole || n < 0 {
			return "", &load.Refusal{Code: "record_shape", Field: "payment_terms_days",
				Detail: "the terms in days are not a whole number of zero or more"}
		}
		terms = int(n)
	}
	var limit int64
	if v, ok := in.Record["credit_limit_cents"]; ok && v != nil {
		n, whole := wholeNumber(v)
		if !whole || n < 0 {
			return "", &load.Refusal{Code: "record_shape", Field: "credit_limit_cents",
				Detail: "the credit limit is not a whole number of cents of zero or more"}
		}
		limit = n
	}
	active := true
	if v, ok := in.Record["is_active"].(bool); ok {
		active = v
	}
	homeBranch, reps, refused := customerRefs(in)
	if refused != nil {
		return "", refused
	}
	meta := map[string]any{}
	if len(reps) > 0 {
		meta["sales_rep_user_ids"] = reps
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("customer: load metadata encode: %w", err)
	}

	// The address: validated and normalised exactly as the staff write
	// does. A failing address is left out, never stored half validated.
	var addrJSON any // nil leaves the stored address as it stands
	if raw, ok := in.Record["billing_address"]; ok && raw != nil {
		obj, isObj := raw.(map[string]any)
		if !isObj {
			noteAddress(in, "the billing address is not an object")
		} else if country, cerr := w.country(ctx); cerr != nil {
			return "", fmt.Errorf("customer: load dealer country: %w", cerr)
		} else if norm, nerr := localization.NormalizeBillingAddressFor(country, obj); nerr != nil {
			noteAddress(in, "the billing address failed the address validation for the dealer's country")
		} else {
			b, merr := json.Marshal(orEmptyObject(norm))
			if merr != nil {
				return "", fmt.Errorf("customer: load address encode: %w", merr)
			}
			addrJSON = string(b)
		}
	}

	var id string
	if in.RowID == "" {
		if accountNo == "" {
			accountNo = in.LegacyID
		}
		// The create arm: insert, or attach the row the account number
		// already names (a staff made customer, or one with no org),
		// stamping it the load's. A row another org owns is never taken
		// over: the guard leaves it untouched and the item refuses. The
		// existing metadata keys are kept, ours set.
		var inserted bool
		// The customer's terms: the code the loaded days name, and the row that code names, so
		// the code a loaded customer carries always names a term that exists. The code and the
		// days are written together: a customer whose code and days disagreed would be dated by
		// one door and not the other.
		code, codeName := TermsForDays(terms)
		if err := ensureTermsRow(ctx, tx, code, codeName, terms); err != nil {
			return "", err
		}
		err = tx.QueryRow(ctx, customerSeqCTE+`
			INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days,
			                       credit_limit_cents, is_active, billing_address, metadata,
			                       org_id, seq, idempotency_key, terms_code)
			VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, $6, $7, COALESCE($8::jsonb, '{}'::jsonb), $9::jsonb,
			        $10::uuid, (SELECT next_value FROM cseq), NULLIF($11, '')::uuid, $12)
			ON CONFLICT (account_no) DO UPDATE SET
				name = EXCLUDED.name,
				customer_type = EXCLUDED.customer_type,
				home_branch_id = EXCLUDED.home_branch_id,
				payment_terms_days = EXCLUDED.payment_terms_days,
				terms_code = EXCLUDED.terms_code,
				credit_limit_cents = EXCLUDED.credit_limit_cents,
				is_active = EXCLUDED.is_active,
				billing_address = COALESCE($8::jsonb, customers.billing_address),
				metadata = (customers.metadata - 'sales_rep_user_ids') || EXCLUDED.metadata,
				org_id = EXCLUDED.org_id,
				seq = EXCLUDED.seq,
				idempotency_key = EXCLUDED.idempotency_key,
				updated_at = now()
			WHERE customers.org_id IS NULL OR customers.org_id = EXCLUDED.org_id
			RETURNING id::text, (xmax = 0)`,
			accountNo, name, custType, homeBranch, terms, limit, active, addrJSON, string(metaJSON),
			in.OrgID, in.DerivedKey, code).Scan(&id, &inserted)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", orgMismatch(in)
		}
		if err != nil {
			return "", fmt.Errorf("customer: load write: %w", err)
		}
		if !inserted && in.Attached != nil {
			in.Attached()
		}
		return id, nil
	}
	// The update arm. An account number another row holds is a key
	// conflict: the holder's id comes back as the row written, and the
	// frame refuses naming both legacy keys (or the staff row when no key
	// names the holder). A holder another org owns refuses as org_mismatch.
	if accountNo != "" {
		var holder string
		var holderOrg *string
		err = tx.QueryRow(ctx,
			`SELECT id::text, org_id::text FROM customers WHERE account_no = $1 AND id <> $2::uuid`,
			accountNo, in.RowID).Scan(&holder, &holderOrg)
		if err == nil {
			if holderOrg != nil && *holderOrg != in.OrgID {
				return "", orgMismatch(in)
			}
			return holder, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("customer: load account holder read: %w", err)
		}
	}
	// The account number is a key column. An UPDATE whose SET list names a key column takes the
	// row FOR UPDATE once the table carries a BEFORE UPDATE row trigger (the sync seq trigger,
	// 0422), even when the value is unchanged, and that blocks a referencing insert for the
	// whole batch. So it moves in its own statement, only when it changes; the content update
	// below names no key column and keeps FOR NO KEY UPDATE.
	if accountNo != "" {
		// The counter first (round 3e): this move relies on the sync seq trigger, which takes the row and then
		// the counter; the batch's order is the counter, then the rows.
		if err := LockCustomersSeqTx(ctx, tx); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE customers SET account_no = $2, updated_at = now()
			WHERE id = $1::uuid AND (org_id IS NULL OR org_id = $3::uuid) AND account_no IS DISTINCT FROM $2`,
			in.RowID, accountNo, in.OrgID); err != nil {
			return "", fmt.Errorf("customer: load update: %w", err)
		}
	}
	// The customer's terms: the code the loaded days name, and the row that code names. The
	// update arm writes the days the record carries, and the code beside them, so the two
	// cannot disagree: a customer whose code and days disagreed would be dated by one door and
	// not the other.
	code, codeName := TermsForDays(terms)
	if err := ensureTermsRow(ctx, tx, code, codeName, terms); err != nil {
		return "", err
	}
	err = tx.QueryRow(ctx, customerSeqCTE+`
		UPDATE customers SET
			name = $3, customer_type = $4,
			home_branch_id = NULLIF($5, '')::uuid, payment_terms_days = $6, credit_limit_cents = $7,
			is_active = $8, billing_address = COALESCE($9::jsonb, billing_address),
			metadata = (metadata - 'sales_rep_user_ids') || $10::jsonb,
			org_id = $11::uuid, seq = (SELECT next_value FROM cseq),
			idempotency_key = NULLIF($12, '')::uuid, terms_code = $13, updated_at = now()
		WHERE id = $1::uuid AND (org_id IS NULL OR org_id = $11::uuid)
		  AND account_no = COALESCE(NULLIF($2, ''), account_no)
		RETURNING id::text`,
		in.RowID, accountNo, name, custType, homeBranch, terms, limit, active, addrJSON, string(metaJSON),
		in.OrgID, in.DerivedKey, code).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Gone, or owned by another org: say which, never overwrite.
		var exists bool
		if qerr := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM customers WHERE id = $1::uuid)`, in.RowID).Scan(&exists); qerr != nil {
			return "", fmt.Errorf("customer: load update target read: %w", qerr)
		}
		if exists {
			return "", orgMismatch(in)
		}
		return "", &load.Refusal{Code: load.ReasonUpdateTargetMissing, Field: "legacy_id",
			Detail: "the name's row is gone; the update refuses rather than creating a second row"}
	}
	if err != nil {
		return "", fmt.Errorf("customer: load update: %w", err)
	}
	return id, nil
}

// orgMismatch is the refusal for a row another org owns: the load never
// re-stamps a row's org, so the item refuses naming the kind and the key.
func orgMismatch(in load.Write) *load.Refusal {
	return &load.Refusal{Code: "org_mismatch", Field: "legacy_id",
		Detail: fmt.Sprintf("customer legacy key %s names a row another org owns", in.LegacyID)}
}

// noteAddress leaves the failing address out and says so on the item's
// note: the field and the reason, never a value.
func noteAddress(in load.Write, detail string) {
	if in.Note != nil {
		in.Note(load.Refusal{Code: ReasonAddressLeftOut, Field: "billing_address", Detail: detail + "; the address was left out"})
	}
}

// customerRefs sorts the item's resolved references: at most one branch
// (the home branch) and any number of users (the reps). Any other kind
// refuses, naming the kind.
func customerRefs(in load.Write) (homeBranch string, reps []string, refused *load.Refusal) {
	for _, r := range in.Refs {
		switch r.Kind {
		case load.KindBranch:
			if homeBranch != "" {
				return "", nil, &load.Refusal{Code: load.ReasonWrongReference, Field: r.Kind,
					Detail: "a customer names one home branch"}
			}
			homeBranch = r.HHID
		case load.KindUser:
			reps = append(reps, r.HHID)
		default:
			return "", nil, &load.Refusal{Code: load.ReasonWrongReference, Field: r.Kind,
				Detail: "a customer record takes branch and user references only"}
		}
	}
	return homeBranch, reps, nil
}

// ContactLoadWriter is the load doors' contact writer: contacts under
// their customer by key.
type ContactLoadWriter struct{}

// Apply answers one contact item. The record names the contact's name
// (required) and, named, the email, the phone, the role, the primary
// flag and the active flag; the one reference of kind customer names the
// customer the contact belongs under. The row has no natural key of its
// own, so the registry's name is its identity: the create arm inserts,
// the update arm updates the named row.
func (ContactLoadWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	name, _ := in.Record["name"].(string)
	if name == "" {
		return "", &load.Refusal{Code: "field_required", Field: "name",
			Detail: "the record names no contact name"}
	}
	customerID, customerKey := "", ""
	for _, r := range in.Refs {
		if r.Kind != load.KindCustomer || customerID != "" {
			return "", &load.Refusal{Code: load.ReasonWrongReference, Field: r.Kind,
				Detail: "a contact names one customer reference and no other"}
		}
		customerID, customerKey = r.HHID, r.LegacyID
	}
	if customerID == "" {
		return "", &load.Refusal{Code: "field_required", Field: load.KindCustomer,
			Detail: "the item names no customer reference; a contact lands under its customer"}
	}
	// The org rides through the customer: the contact lands only under a
	// customer of the machine principal's org.
	if refused := customerOrgRefusal(ctx, tx, customerID, customerKey, in.OrgID); refused != nil {
		return "", refused
	}
	email, _ := in.Record["email"].(string)
	phone, _ := in.Record["phone"].(string)
	role, _ := in.Record["role"].(string)
	primary, _ := in.Record["is_primary"].(bool)
	active := true
	if v, ok := in.Record["is_active"].(bool); ok {
		active = v
	}
	var id string
	if in.RowID == "" {
		if err := tx.QueryRow(ctx, `
			INSERT INTO customer_contacts (customer_id, name, email, phone, role, is_primary, is_active)
			VALUES ($1::uuid, $2, NULLIF($3, ''), $4, $5, $6, $7)
			RETURNING id::text`,
			customerID, name, email, phone, role, primary, active).Scan(&id); err != nil {
			return "", fmt.Errorf("customer: contact load write: %w", err)
		}
		return id, nil
	}
	err := tx.QueryRow(ctx, `
		UPDATE customer_contacts SET customer_id = $2::uuid, name = $3, email = NULLIF($4, ''), phone = $5,
		       role = $6, is_primary = $7, is_active = $8, updated_at = now()
		WHERE id = $1::uuid
		RETURNING id::text`,
		in.RowID, customerID, name, email, phone, role, primary, active).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", &load.Refusal{Code: load.ReasonUpdateTargetMissing, Field: "legacy_id",
			Detail: "the name's row is gone; the update refuses rather than creating a second row"}
	}
	if err != nil {
		return "", fmt.Errorf("customer: contact load update: %w", err)
	}
	return id, nil
}

// customerOrgRefusal reads the referenced customer row's org on the frame's
// transaction and refuses with the true thing: a row that is gone is a
// missing reference, a row with no org is not loaded into this org, and a
// row another org owns is an org mismatch. Nil when the row is this org's.
func customerOrgRefusal(ctx context.Context, tx pgx.Tx, customerID, customerKey, orgID string) error {
	found, org, err := OrgReader{}.CustomerOrgTx(ctx, tx, customerID)
	if err != nil {
		return fmt.Errorf("customer: contact customer read: %w", err)
	}
	switch {
	case !found:
		return &load.Refusal{Code: load.ReasonMissingReference, Field: load.KindCustomer,
			Detail: fmt.Sprintf("the customer row for legacy key %s is gone", customerKey)}
	case org == "":
		return &load.Refusal{Code: load.ReasonMissingReference, Field: load.KindCustomer,
			Detail: fmt.Sprintf("the customer row for legacy key %s carries no org of this load", customerKey)}
	case org != orgID:
		return &load.Refusal{Code: "org_mismatch", Field: load.KindCustomer,
			Detail: fmt.Sprintf("customer legacy key %s names a row another org owns", customerKey)}
	}
	return nil
}

// wholeNumber reads a record number as a whole count: the wire decodes
// numbers as float64 and a Go caller may carry an int, and both land the
// same. A fraction or a type that names no number is not whole.
func wholeNumber(raw any) (int64, bool) {
	switch v := raw.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		if v != float64(int64(v)) {
			return 0, false
		}
		return int64(v), true
	default:
		return 0, false
	}
}

// Execer is the one statement runner EnsureAccount needs: a pgx pool, a
// connection or a transaction satisfies it.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PinnedAccount is one customer account written under a caller chosen id,
// for a provisioning program that must name the row elsewhere before it
// exists (the demo populate's contractor account, whose id is the tenant
// claim of the contractor tokens).
type PinnedAccount struct {
	ID               string
	AccountNo        string
	Name             string
	CustomerType     string
	HomeBranchID     string
	OrgID            string
	PaymentTermsDays int
	CreditLimitCents int64
}

// EnsureAccount writes the account under its id, once: a row already
// holding the id is left exactly as it is (created false), so a re-run of
// the provisioning program writes nothing. An account number held by a
// different id is the plan and the database disagreeing about the world,
// and surfaces as the unique violation it is. The row takes its seq from
// the customers scope the module's own pull reads through.
func EnsureAccount(ctx context.Context, db Execer, a PinnedAccount) (created bool, err error) {
	if a.ID == "" || a.AccountNo == "" || a.Name == "" {
		return false, errors.New("customer: a pinned account needs an id, an account number and a name")
	}
	if !validCustomerType(a.CustomerType) {
		return false, fmt.Errorf("customer: invalid customer_type %q", a.CustomerType)
	}
	// The sequence CTE draws its number whether or not the insert lands, so a
	// row that is already there is recognised first and a re-run draws nothing.
	var one int
	switch err := db.QueryRow(ctx, `SELECT 1 FROM customers WHERE id = $1::uuid`, a.ID).Scan(&one); {
	case err == nil:
		return false, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return false, fmt.Errorf("customer: pinned account read: %w", err)
	}
	// The customer's terms: the code the pinned days name, and the row that code names, so the
	// code a pinned customer carries always names a term that exists.
	code, codeName := TermsForDays(a.PaymentTermsDays)
	if err := ensureTermsRow(ctx, db, code, codeName, a.PaymentTermsDays); err != nil {
		return false, err
	}
	tag, err := db.Exec(ctx, customerSeqCTE+`
		INSERT INTO customers (id, account_no, name, customer_type, home_branch_id,
		                       payment_terms_days, credit_limit_cents, org_id, seq, terms_code)
		VALUES ($1::uuid, $2, $3, $4, NULLIF($5, '')::uuid, $6, $7, NULLIF($8, '')::uuid,
		        (SELECT next_value FROM cseq), $9)
		ON CONFLICT (id) DO NOTHING`,
		a.ID, a.AccountNo, a.Name, a.CustomerType, a.HomeBranchID,
		a.PaymentTermsDays, a.CreditLimitCents, a.OrgID, code)
	if err != nil {
		return false, fmt.Errorf("customer: pinned account write: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
