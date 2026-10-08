package staffcontext

// rbac_convert_pay_test.go, the order payment door's one predicate after the yard manager ruling
// (decision log row 1156, reversing the admission row 370's cells carried): the yard manager loses
// the Counter Till and the Quote Desk, so the combined convert and pay, a payment route of the
// till, is the selling roles alone, and no predicate may name the yard manager for a payment.

import "testing"

func TestCanConvertAndPayIsTheSellingRolesAlone(t *testing.T) {
	active := func(code string) StaffContext {
		return StaffContext{RoleCode: code, Status: StatusActive}
	}
	for _, code := range []string{"INSIDE_SALES", "OUTSIDE_SALES", "SALES_MANAGER", "BRANCH_MANAGER", "GENERAL_MANAGER", "FINANCIAL_CONTROLLER", RoleSuperAdmin} {
		if !CanConvertAndPay(active(code)) {
			t.Errorf("%s is a selling role and must take the order payment", code)
		}
	}
	for _, code := range []string{"YARD_MANAGER", "YARD_TEAM", "DRIVERS", "LOGISTICS_MANAGER", "HR", "PAYABLES_RECEIVABLES", "PROCUREMENT_MANAGER"} {
		if CanConvertAndPay(active(code)) {
			t.Errorf("%s must not take the order payment (the yard manager's till and desk cells are removed)", code)
		}
	}
	// Status is checked inside, as CanSellOnCredit does: an inactive seller is not a seller.
	if CanConvertAndPay(StaffContext{RoleCode: "INSIDE_SALES", Status: "SUSPENDED"}) {
		t.Error("an inactive seller must not take the order payment")
	}
	// The two platform roles that are not selling roles stay refused here, so the grant read
	// says false for them whatever the gate levels they pass.
	for _, code := range []string{"DEALER_ADMIN", "FB_OPERATOR"} {
		if CanConvertAndPay(active(code)) {
			t.Errorf("%s is not a selling role and must not take the order payment", code)
		}
	}
}
