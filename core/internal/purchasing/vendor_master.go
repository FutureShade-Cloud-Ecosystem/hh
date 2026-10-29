package purchasing

// vendor_master.go, the staff writer for the vendor record: the create and edit route pair the
// roles roadmap's Buying and Stock core slice names as missing ("no vendor create or edit",
// section 1.5). Until this file the vendors table had exactly one writer, the load doors' supplier
// upsert (vendorload.go); everything a person could do was read.
//
// THE CONVENTIONS ARE THE LOAD WRITER'S, READ AT SOURCE, NOT INVENTED HERE:
//
//   - the vendor NUMBER is minted server side (next_vendor_no, migration 0386) and never accepted
//     from the caller, the rule the PO number already states: a caller supplied number is a
//     collision waiting for the next load;
//   - the seq is drawn from the vendors scope inside the same statement (the vendorSeqCTE shape),
//     so a created or edited row reaches a synced device on its next pull, and the edit of a
//     carried row (seq NULL, 0213's "carried rows keep seq NULL") stamps it live;
//   - the org stamp is the caller's (the guard resolved scope, never a header), and a row another
//     org owns is refused (as a 404) rather than re-stamped, the load's org_mismatch posture; a
//     carried row (org NULL) is stamped by the first edit.
//
// WHAT AN EDIT MAY TOUCH, deliberately narrow: the name, the terms, the ordering email that rides
// the record as the remit fallback (the address book the untagged send reads), and the active
// switch. The vendor NUMBER is not editable (it is the table's natural key and the load's join),
// and the tagged ordering desk keeps its own existing route (PUT /vendors/{id}/contact), which is
// the door a buyer already tags a desk through; two doors for one fact is how they drift apart.
//
// THE ACTIVE SWITCH HAS TEETH ELSEWHERE: CreatePO refuses an inactive vendor ("a terminated supply
// relationship quietly resumes", service.go), so deactivating is the honest end of a vendor, and
// nothing here needs a second rule. Open POs against a deactivated vendor stay open: is_active
// gates NEW orders, not the receiving and billing the old ones still owe.
//
// NO BRANCH FENCE, ON PURPOSE: a vendor is org wide (no branch column exists, 0011), the same
// reach ListVendors already answers with. The role wall is the route gate's (buying-and-stock at
// manage, the procurement manager alone in the decided matrix), not a branch predicate.

import (
	"context"
	"net/mail"
	"strings"
)

// VendorMaster is the created or edited record as the routes answer it: the directory projection
// plus the ordering email the write just planted, so a buyer sees in the response what they typed.
type VendorMaster struct {
	ID               string `json:"id"`
	VendorNo         string `json:"vendor_no"`
	Name             string `json:"name"`
	PaymentTermsDays int    `json:"payment_terms_days"`
	// OrderEmail is the remit fallback address (remit_address->email), the one free form place the
	// outbound send's address book reads. Empty is a real answer: no email on file.
	OrderEmail string `json:"order_email"`
	IsActive   bool   `json:"is_active"`
	// IsT5018Subcontractor is the Financial Controller's mark on the vendor: the flag the year's
	// T5018 report filters on. False is the default for every vendor that has never been marked.
	IsT5018Subcontractor bool `json:"is_t5018_subcontractor"`
	// BusinessNumber is the contractor's CRA business number, the other half of the T5018 form.
	// Empty is a real answer: the contractor has not given one.
	BusinessNumber string `json:"business_number"`
}

// CreateVendorRequest is the POST /purchasing/vendors body. PaymentTermsDays nil means the 30 day
// default; zero is legal (pay on delivery) and distinct from absent, which is why it is a pointer.
type CreateVendorRequest struct {
	Name             string `json:"name"`
	PaymentTermsDays *int   `json:"payment_terms_days"`
	OrderEmail       string `json:"order_email"`
}

// UpdateVendorRequest is the PUT /purchasing/vendors/{vendorID} body, the same
// set-what-the-body-says posture the expected-date and delivery-method corrections carry: every
// field is optional, absent means unchanged, and an email may be cleared by sending an empty one.
type UpdateVendorRequest struct {
	Name             *string `json:"name"`
	PaymentTermsDays *int    `json:"payment_terms_days"`
	OrderEmail       *string `json:"order_email"`
	IsActive         *bool   `json:"is_active"`
}

// CreateVendor mints the number, plants the record active under the caller's org, and answers it.
func (s *Service) CreateVendor(ctx context.Context, orgID string, in CreateVendorRequest) (VendorMaster, error) {
	if _, err := s.caller(ctx); err != nil {
		return VendorMaster{}, err
	}
	name, err := validVendorName(in.Name)
	if err != nil {
		return VendorMaster{}, err
	}
	email, err := validOrderEmail(in.OrderEmail)
	if err != nil {
		return VendorMaster{}, err
	}
	terms, err := validTerms(in.PaymentTermsDays, 30)
	if err != nil {
		return VendorMaster{}, err
	}
	if !isUUIDString(orgID) {
		return VendorMaster{}, badReq("purchasing: the caller's org is not resolved, cannot stamp the vendor")
	}
	v, err := s.repo.InsertVendorMaster(ctx, orgID, name, terms, email)
	if err != nil {
		return VendorMaster{}, err
	}
	s.record(ctx, "purchasing.vendor.created", v.ID,
		map[string]any{"vendor_no": v.VendorNo, "name": v.Name, "payment_terms_days": v.PaymentTermsDays})
	return v, nil
}

// UpdateVendor corrects the fields the body names on one vendor, or refuses: unknown vendor or another org's row (one 404, so
// the status says nothing about which ids exist), a body that names nothing (400). A carried vendor
// (org NULL: the seed and the loads predate tenancy) is claimed for the caller's org by its first
// edit, in the same statement as the write: refusing it would leave every carried supplier
// uneditable, and the WHERE's org test is what keeps a second org out afterwards.
func (s *Service) UpdateVendor(ctx context.Context, orgID, vendorID string, in UpdateVendorRequest) (VendorMaster, error) {
	if _, err := s.caller(ctx); err != nil {
		return VendorMaster{}, err
	}
	if !isUUIDString(vendorID) {
		return VendorMaster{}, badReq("purchasing: vendor id must be a uuid")
	}
	if !isUUIDString(orgID) {
		return VendorMaster{}, badReq("purchasing: the caller's org is not resolved, cannot stamp the vendor")
	}
	var name *string
	if in.Name != nil {
		n, err := validVendorName(*in.Name)
		if err != nil {
			return VendorMaster{}, err
		}
		name = &n
	}
	var email *string
	if in.OrderEmail != nil {
		e, err := validOrderEmail(*in.OrderEmail)
		if err != nil {
			return VendorMaster{}, err
		}
		email = &e
	}
	var terms *int
	if in.PaymentTermsDays != nil {
		t, err := validTerms(in.PaymentTermsDays, 0)
		if err != nil {
			return VendorMaster{}, err
		}
		terms = &t
	}
	if name == nil && email == nil && terms == nil && in.IsActive == nil {
		return VendorMaster{}, badReq("purchasing: the body names nothing to change")
	}

	v, err := s.repo.UpdateVendorMaster(ctx, orgID, vendorID, name, terms, email, in.IsActive)
	if err != nil {
		return VendorMaster{}, err
	}
	s.record(ctx, "purchasing.vendor.updated", v.ID,
		map[string]any{"vendor_no": v.VendorNo, "name": v.Name, "payment_terms_days": v.PaymentTermsDays,
			"order_email": v.OrderEmail, "is_active": v.IsActive})
	return v, nil
}

// - the validators, pure so a unit test can state them without a database -

func validVendorName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", badReq("purchasing: vendor name is required")
	}
	if len(name) > 200 {
		return "", badReq("purchasing: vendor name is too long (200 characters at most)")
	}
	return name, nil
}

func validOrderEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", nil
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", badReq("purchasing: order_email must be one email address")
	}
	return email, nil
}

func validTerms(days *int, def int) (int, error) {
	if days == nil {
		return def, nil
	}
	if *days < 0 || *days > 365 {
		return 0, badReq("purchasing: payment_terms_days must be between 0 and 365")
	}
	return *days, nil
}

// isUUIDString is the shape check the line doors, the vendor edit's path parameter and the create's
// org stamp share, and it is EXACT: the 8-4-4-4-12 form or 32 hex digits, nothing else, because
// anything looser (the first version took any 32 to 36 hex digits and dashes) passes strings the
// database's uuid cast refuses, and the refusal comes back as a 500.
func isUUIDString(s string) bool {
	switch len(s) {
	case 36:
		for i := 0; i < len(s); i++ {
			if i == 8 || i == 13 || i == 18 || i == 23 {
				if s[i] != '-' {
					return false
				}
			} else if !isHexDigit(s[i]) {
				return false
			}
		}
		return true
	case 32:
		for i := 0; i < len(s); i++ {
			if !isHexDigit(s[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
