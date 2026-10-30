package billing

// credit_change_route_test.go pins the five credit change request doors in the served route table
// (internal/platform/apigen): request, list, get, approve, and reject. Every door is manager-gated
// on money-desk; role-specific approval rules and branch fencing run inside the service.

import "testing"

func TestCreditChangeRoutes_AreServedAndManagerGated(t *testing.T) {
	cases := []struct {
		method string
		path   string
		opID   string
		level  string
	}{
		{"POST", "/api/v1/ar/credit-changes", "billingRequestCreditChange", "manage"},
		{"GET", "/api/v1/ar/credit-changes", "billingListCreditChanges", "read"},
		{"GET", "/api/v1/ar/credit-changes/{id}", "billingGetCreditChange", "read"},
		{"POST", "/api/v1/ar/credit-changes/{id}/approve", "billingApproveCreditChange", "manage"},
		{"POST", "/api/v1/ar/credit-changes/{id}/reject", "billingRejectCreditChange", "manage"},
	}
	for _, tc := range cases {
		t.Run(tc.opID, func(t *testing.T) {
			found := findRoute(tc.method, tc.path)
			if found == nil {
				t.Fatalf("no route %s %s in the served route table: credit change door %s is missing", tc.method, tc.path, tc.opID)
			}
			if found.OperationID != tc.opID {
				t.Fatalf("%s %s operationId = %q, want %q", tc.method, tc.path, found.OperationID, tc.opID)
			}
			if found.XRole != "requireManager" {
				t.Fatalf("%s %s x-role = %q, want requireManager", tc.method, tc.path, found.XRole)
			}
			if found.Level != tc.level {
				t.Fatalf("%s %s level = %q, want %q", tc.method, tc.path, found.Level, tc.level)
			}
			hasMoneyDesk := false
			for _, s := range found.Surfaces {
				if s == "money-desk" {
					hasMoneyDesk = true
					break
				}
			}
			if !hasMoneyDesk {
				t.Fatalf("%s %s surfaces = %v, want money-desk", tc.method, tc.path, found.Surfaces)
			}
		})
	}
}
