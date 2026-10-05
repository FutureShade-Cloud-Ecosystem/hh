package audit

import "testing"

// The pos SALE_COMPLETED completion record (pos/service.go) carries the order
// facts a counter trail answers "sold by whom" with, as IDS ONLY: the credited
// salesperson's staff id. A person's name and an address never ride the trail
// (salesperson_name and job_address stay dropped here; a name is read from its
// id when shown), and neither do the two free text references po_job_ref and
// job_reference: staff type those at the counter, so a value can hold a
// customer's name or a site address, and the reference is read from the sale
// record when shown. The tax relief assessment record carries the declared use
// and the fulfilment code.
func TestSaleCompletedExtraKeys_IdsSurviveFreeTextAndNamesDoNot(t *testing.T) {
	e := recordOne(t, SensitiveAccess{
		Action: ActSaleCompleted, EntityType: "pos.sale", EntityID: sampleUUID,
		Reason: "CASH",
		Extra: map[string]any{
			"po_job_ref":       "PO 4471 / JOB 12",
			"salesperson_id":   sampleUUID,
			"job_reference":    "JOB-12",
			"salesperson_name": "A Person",
			"job_address":      "128 Front St, Trenton ON",
		},
	})
	if e.Changes["salesperson_id"] != sampleUUID {
		t.Errorf("salesperson_id must survive: %+v", e.Changes)
	}
	for _, k := range []string{"po_job_ref", "job_reference", "salesperson_name", "job_address"} {
		if _, ok := e.Changes[k]; ok {
			t.Errorf("%s must stay dropped (free text, a name or an address): %+v", k, e.Changes)
		}
	}
}

func TestSaleCompletedExtraKeys_AreNotOperatorSafe(t *testing.T) {
	for _, k := range []string{"salesperson_id", "declared_use", "fulfilment"} {
		if operatorSafeKeys[k] {
			t.Errorf("%s must stay admin only (not in operatorSafeKeys)", k)
		}
	}
}

func TestReliefAssessExtraKeys_SurviveTheAllowlist(t *testing.T) {
	e := recordOne(t, SensitiveAccess{
		Action: ActTaxReliefAssess, EntityType: "fn.eligibility_profile", EntityID: sampleUUID,
		Purpose: "TAX_RELIEF_ASSESS",
		Extra:   map[string]any{"declared_use": "personal", "fulfilment": "pickup"},
	})
	if e.Changes["declared_use"] != "personal" || e.Changes["fulfilment"] != "pickup" {
		t.Errorf("declared_use and fulfilment must survive the allowlist: %+v", e.Changes)
	}
}
