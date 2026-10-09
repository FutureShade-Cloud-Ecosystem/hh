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
//     org owns is refused rather than re-stamped, exactly the load's org_mismatch posture.
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

// UpdateVendor corrects the fields the body names on one vendor, or refuses: unknown vendor (404),
// another org's row (403), a body that names nothing (400).
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

	// The org check reads before the write, and separately from it: an edit that matched no row is
	// a 404 and an edit that matched another org's row is a refusal, and folding both into one
	// UPDATE ... RETURNING would answer two different questions identically.
	owner, err := s.repo.VendorOrg(ctx, vendorID)
	if err != nil {
		return VendorMaster{}, err
	}
	if owner != "" && owner != orgID {
		return VendorMaster{}, errForbidden
	}

	v, err := s.repo.UpdateVendorMaster(ctx, vendorID, name, terms, email, in.IsActive)
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

// isUUIDString is the shape check the create's org stamp and the edit's path parameter share: a
// loose one (length and alphabet), because the database's uuid cast is the authority on shape, and
// this guard exists so a nonsense string becomes a 400 here rather than a 500 out of the driver.
func isUUIDString(s string) bool {
	if len(s) < 32 || len(s) > 36 {
		return false
	}
	return strings.Trim(s, "0123456789abcdefABCDEF-") == ""
}
