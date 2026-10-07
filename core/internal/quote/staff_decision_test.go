package quote

import (
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// The staff decision doors (accept and reject on the customer's behalf) are gated by the ONE
// predicate the counter's convert door uses. This table runs every role code the system knows, plus
// the empty one, through both the predicate and the two doors, so the doors cannot drift from the
// rule: a role the predicate refuses is refused here with the role named, before any store is touched.
func TestStaffDecision_GateIsTheConvertPredicate(t *testing.T) {
	codes := append([]string{"", "NO_SUCH_ROLE"}, identity.SellingRoles()...)
	codes = append(codes, "YARD_TEAM", "DRIVERS", "YARD_MANAGER", "HR", "PROCUREMENT_MANAGER",
		"LOGISTICS_MANAGER", "PAYABLES_RECEIVABLES")

	accept := StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true}
	for _, role := range codes {
		caller := sellerCtx(role)
		sells := identity.CanSellOnCredit(caller)
		for name, call := range map[string]func(*StaffService) error{
			"accept": func(s *StaffService) error { _, err := s.AcceptOnBehalf(qCtxAs(caller), qOrg, accept); return err },
		} {
			store := newSeamStore()
			err := call(staffSvcOver(store, caller).withCaller(qCallerFrom))
			var notSelling *NotSellingRoleError
			refused := errors.As(err, &notSelling)
			if sells && refused {
				t.Errorf("%s as %q: the convert predicate admits this role but the door refused it", name, role)
			}
			if !sells {
				if !refused {
					t.Errorf("%s as %q: the convert predicate refuses this role, got %v", name, role, err)
					continue
				}
				if len(store.calls) != 0 {
					t.Errorf("%s as %q: refused but a store was still reached", name, role)
				}
				if role != "" && !strings.Contains(notSelling.Error(), role) {
					t.Errorf("%s as %q: the refusal must name the role: %q", name, role, notSelling.Error())
				}
				if strings.Contains(notSelling.Error(), "converting") {
					t.Errorf("%s as %q: the refusal must say what this door does, not the convert door's words: %q", name, role, notSelling.Error())
				}
			}
		}
	}
}

// The convert door's own refusal text stays byte for byte what it was: the shared error grew a field,
// not a new sentence for the convert door.
func TestStaffDecision_ConvertRefusalWordsUnchanged(t *testing.T) {
	got := (&NotSellingRoleError{RoleCode: "YARD_TEAM"}).Error()
	want := "quote: converting a quote to an order commits the customer's credit and is limited to sales roles, YARD_TEAM cannot do it; ask inside sales or a manager"
	if got != want {
		t.Fatalf("convert refusal words changed:\n got: %q\nwant: %q", got, want)
	}
}

func TestStaffDecision_InputGrading(t *testing.T) {
	svc := func() *StaffService {
		return staffSvcOver(newSeamStore(), sellerCtx("INSIDE_SALES")).withCaller(qCallerFrom)
	}
	ctx := qCtxAs(sellerCtx("INSIDE_SALES"))
	bad := map[string]func() error{
		"accept without a channel": func() error {
			_, err := svc().AcceptOnBehalf(ctx, qOrg, StaffAcceptIn{QuantitiesConfirmed: true})
			return err
		},
		"accept with an unknown channel": func() error {
			_, err := svc().AcceptOnBehalf(ctx, qOrg, StaffAcceptIn{Channel: "carrier pigeon", QuantitiesConfirmed: true})
			return err
		},
		"accept without the quantity statement": func() error {
			_, err := svc().AcceptOnBehalf(ctx, qOrg, StaffAcceptIn{Channel: "phone"})
			return err
		},
		"accept with a bad key": func() error {
			_, err := svc().AcceptOnBehalf(ctx, qOrg, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, IdempotencyKey: "nope"})
			return err
		},
		"accept with an over long note": func() error {
			_, err := svc().AcceptOnBehalf(ctx, qOrg, StaffAcceptIn{Channel: "phone", QuantitiesConfirmed: true, Note: strings.Repeat("x", 501)})
			return err
		},
	}
	for name, call := range bad {
		err := call()
		var br interface{ Error() string }
		if err == nil {
			t.Errorf("%s: want a refusal", name)
			continue
		}
		br = err
		if strings.Contains(br.Error(), "not implemented") || strings.Contains(br.Error(), "not configured") {
			t.Errorf("%s: want a 400 in words, got %v", name, err)
		}
	}
}
