package warranty

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// handler_agreement_test.go, the walk change 6 vendor agreement HTTP surface over httptest, the
// policy handler tests' road: the handler bodies are driven the way h.staff delivers them (the
// request stamped with the guard-resolved StaffContext), the GUARDS themselves stay the xrole suite's
// assertion, and this file pins the shapes and the refusals in the core's own words.

// agreementHandler wires the production default caller resolution over the fake store seeded with the
// four vendor roster rows and the recording audit, exactly what policyHandler does for the threshold.
func agreementHandler(t *testing.T) (*Handler, *fakeStore, *recordingAudit) {
	t.Helper()
	f := newFakeStore()
	f.vendorRoster = map[string]VendorAgreement{}
	for _, v := range agreementVendors {
		f.vendorRoster[v.id] = VendorAgreement{VendorID: v.id, VendorNo: v.no, VendorName: v.name}
	}
	rec := &recordingAudit{}
	svc := NewService(nil).withStore(f).WithAudit(rec)
	return NewHandler(svc, nil), f, rec
}

func TestAgreementHTTP_ListAnswersTheVendorRows(t *testing.T) {
	h, _, _ := agreementHandler(t)
	rec := serveStaff(h.listAgreements, httptest.NewRequest("GET", "/api/v1/warranty/agreements", nil), identityAdmin())
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/warranty/agreements answered %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	items, _ := body["items"].([]any)
	if len(items) != len(agreementVendors) {
		t.Fatalf("the list answered %d items, want %d", len(items), len(agreementVendors))
	}
	first, _ := items[0].(map[string]any)
	if first["agreement_kind"] != AgreementKindClaimByClaim {
		t.Fatalf("the first row answers %v, want the default %q", first["agreement_kind"], AgreementKindClaimByClaim)
	}
	if _, ok := body["updated_by"]; ok {
		t.Fatalf("updated_by rode the wire: %s; a person id never rides it, the audit spine carries who", rec.Body.String())
	}
}

func TestAgreementHTTP_PutStoresAndAnswersTheAgreement(t *testing.T) {
	h, fs, rec2 := agreementHandler(t)
	body, _ := json.Marshal(AgreementInput{AgreementKind: AgreementKindFixedAllowance, Period: AgreementPeriodQuarterly, AllowanceBP: 100})
	req := httptest.NewRequest("PUT", "/api/v1/warranty/agreements/"+agreementVendors[2].id, bytes.NewReader(body))
	req.SetPathValue("vendorID", agreementVendors[2].id)
	res := serveStaff(h.putAgreement, req, identityAdmin())
	if res.Code != http.StatusOK {
		t.Fatalf("PUT /api/v1/warranty/agreements/{vendorID} answered %d, want 200 (%s)", res.Code, res.Body.String())
	}
	var saved map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &saved)
	if saved["agreement_kind"] != AgreementKindFixedAllowance || saved["allowance_bp"] != float64(100) {
		t.Fatalf("the answer carries %s, want the stored fixed allowance at 100 bp", res.Body.String())
	}
	if fs.agreements[agreementVendors[2].id].Period != AgreementPeriodQuarterly {
		t.Fatalf("the store holds period %q, want quarterly", fs.agreements[agreementVendors[2].id].Period)
	}
	if rec2.count() == 0 {
		t.Fatal("a moved agreement filed no audit rows")
	}
}

func TestAgreementHTTP_PutRefusesInTheCoresOwnWords(t *testing.T) {
	h, fs, rec2 := agreementHandler(t)
	body, _ := json.Marshal(AgreementInput{AgreementKind: AgreementKindPeriodicCreditNote})
	req := httptest.NewRequest("PUT", "/api/v1/warranty/agreements/"+agreementVendors[3].id, bytes.NewReader(body))
	req.SetPathValue("vendorID", agreementVendors[3].id)
	res := serveStaff(h.putAgreement, req, identityAdmin())
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a periodless periodic agreement answered %d, want 422 (%s)", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "period") {
		t.Fatalf("the refusal %s does not name the period", res.Body.String())
	}
	if _, stored := fs.agreements[agreementVendors[3].id]; stored {
		t.Fatalf("a refused write stored the row %+v", fs.agreements[agreementVendors[3].id])
	}
	if rec2.count() != 0 {
		t.Fatalf("a refused write filed %d audit rows, want none", rec2.count())
	}
}

func TestAgreementHTTP_PutWithAMalformedBodyIs400(t *testing.T) {
	h, _, _ := agreementHandler(t)
	req := httptest.NewRequest("PUT", "/api/v1/warranty/agreements/"+agreementVendors[0].id, strings.NewReader("{not json"))
	req.SetPathValue("vendorID", agreementVendors[0].id)
	res := serveStaff(h.putAgreement, req, identityAdmin())
	if res.Code != http.StatusBadRequest {
		t.Fatalf("a malformed body answered %d, want 400", res.Code)
	}
}
