package purchasing

// t5018.go, the Financial Controller's subcontractor mark on a vendor, and the business number the
// CRA's Form T5018 asks the contractor for.
//
// WHAT THIS IS. The T5018 is what a subcontractor hands a payer: it names the contractor's
// business number and the amounts the payer paid that contractor during the calendar year. The
// dealer had nowhere to keep either half. This file is the write door for both, and the year's
// report (t5018_report.go) is the read door.
//
// WHO MAY SET IT. The Controller alone, and that wall is the role gate's, not this file's: the
// census row for the route names money-desk at admin, and the Financial Controller is the decided
// matrix's only admin seat on money-desk. The general manager, whose money-desk cell is manage, is
// refused with it. The proof is core/internal/platform/authz/rolegate_t5018_test.go, on the
// generated census entry rather than on a literal beside it.
//
// THE BRANCH RULES ARE THE VENDOR'S OWN EDIT'S. The vendor master is org wide: the flag write
// takes the caller's org, refuses a row stamped with another org with the same 404 an unknown id
// gets, and claims a carried row (org_id NULL, 0213) for the caller's org in the same statement,
// exactly as the vendor's own edit does. There is no branch fence on a vendor: a vendor serves the
// whole dealer, so its T5018 mark is the whole dealer's too.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// SetVendorT5018Request is the PUT /api/v1/purchasing/vendors/{vendorID}/t5018 body.
//
// IsSubcontractor is the checkbox itself and it is REQUIRED: the flag is what the year's report
// filters on, so a body that does not say what the flag is names nothing to set. BusinessNumber is
// the contractor's CRA business number; absent leaves it as it stands and an empty string clears
// it, the same set-what-the-body-says posture the vendor's own edit carries.
type SetVendorT5018Request struct {
	IsSubcontractor *bool   `json:"is_subcontractor"`
	BusinessNumber  *string `json:"business_number"`
}

// SetVendorT5018 writes the mark and the number on one vendor and answers the vendor as it now
// reads. The audit entry names what changed, because the flag is the fact a CRA form is built on
// and the who changed it and when is part of that record.
func (s *Service) SetVendorT5018(ctx context.Context, orgID, vendorID string, in SetVendorT5018Request) (VendorMaster, error) {
	if _, err := s.caller(ctx); err != nil {
		return VendorMaster{}, err
	}
	if in.IsSubcontractor == nil {
		return VendorMaster{}, badReq("purchasing: is_subcontractor is required on the T5018 record")
	}
	if !isUUIDString(vendorID) {
		return VendorMaster{}, badReq("purchasing: vendor id must be a uuid")
	}
	if !isUUIDString(orgID) {
		return VendorMaster{}, badReq("purchasing: the caller's org is not resolved, cannot stamp the vendor")
	}
	number, err := validBusinessNumber(in.BusinessNumber)
	if err != nil {
		return VendorMaster{}, err
	}
	v, err := s.repo.SetVendorT5018(ctx, orgID, vendorID, in.IsSubcontractor, number)
	if err != nil {
		return VendorMaster{}, err
	}
	s.record(ctx, "purchasing.vendor.t5018.set", v.ID, map[string]any{
		"is_subcontractor": v.IsT5018Subcontractor,
		"business_number":  v.BusinessNumber,
	})
	return v, nil
}

// validBusinessNumber reads the contractor's CRA business number: nine digits, optionally with the
// program account suffix (RC0001) after them. Spaces are stripped, because a dealer writes the
// number as it is printed on the form. An absent number is nil (the record keeps what it has); an
// empty one clears it.
func validBusinessNumber(in *string) (*string, error) {
	if in == nil {
		return nil, nil
	}
	clean := strings.ReplaceAll(strings.TrimSpace(*in), " ", "")
	if clean == "" {
		return &clean, nil
	}
	if !isCRABusinessNumber(clean) {
		return nil, badReq("purchasing: the business number must be nine digits, with RC0001 after them for a program account")
	}
	return &clean, nil
}

// isCRABusinessNumber answers whether the cleaned number is a CRA business number: the nine digits
// every business number is, and the program account suffix the form allows after them.
func isCRABusinessNumber(s string) bool {
	if len(s) != 9 && len(s) != 15 {
		return false
	}
	for i := 0; i < 9; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	if len(s) == 9 {
		return true
	}
	return s[9:] == "RC0001"
}

// SetVendorT5018 writes the mark and the number on one vendor in ONE statement, which also draws a
// fresh seq, so the write reaches a synced device.
//
// THE ORG TEST LIVES IN THE WHERE, not in a read before it, for the reason the vendor's own edit
// gives: a row stamped with another org matches nothing and answers ErrUnknownVendor, the same
// 404 an unknown id gets, and a carried row (org_id NULL, 0213) is claimed for the caller's org in
// the same statement, so two orgs marking one carried row cannot both win.
//
// The number arm is a CASE rather than a plain assignment because "absent" and "cleared" are
// different facts: absent leaves the row's number as it stands, cleared takes it away, and only
// one of them may be said in a press.
func (r *Repository) SetVendorT5018(ctx context.Context, orgID, vendorID string, isSubcontractor *bool, businessNumber *string) (VendorMaster, error) {
	if r.pool == nil {
		return VendorMaster{}, errNoDB
	}
	setNumber := businessNumber != nil
	number := ""
	if setNumber {
		number = *businessNumber
	}
	var v VendorMaster
	// The vendors counter first, without advancing it, then the row (a load batch holds the counter before
	// its item rows, LD-5; PR 556 round 3e).
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return VendorMaster{}, fmt.Errorf("purchasing: set vendor T5018: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := LockVendorsSeqTx(ctx, tx); err != nil {
		return VendorMaster{}, err
	}
	err = tx.QueryRow(ctx, `
		UPDATE vendors SET
			is_t5018_subcontractor = COALESCE($2::boolean, is_t5018_subcontractor),
			business_number = CASE
				WHEN NOT $3::boolean THEN business_number
				WHEN $4::text = '' THEN NULL
				ELSE $4::text
			END,
			org_id = COALESCE(org_id, $5::uuid),
			updated_at = now()
		WHERE id = $1::uuid AND (org_id IS NULL OR org_id = $5::uuid)
		RETURNING id::text, vendor_no::text, name, payment_terms_days, COALESCE(remit_address->>'email',''), is_active,
		          is_t5018_subcontractor, COALESCE(business_number, '')`,
		vendorID, isSubcontractor, setNumber, number, orgID).Scan(
		&v.ID, &v.VendorNo, &v.Name, &v.PaymentTermsDays, &v.OrderEmail, &v.IsActive,
		&v.IsT5018Subcontractor, &v.BusinessNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return VendorMaster{}, ErrUnknownVendor
	}
	if err != nil {
		return VendorMaster{}, fmt.Errorf("purchasing: set vendor T5018: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return VendorMaster{}, fmt.Errorf("purchasing: set vendor T5018: %w", err)
	}
	return v, nil
}
