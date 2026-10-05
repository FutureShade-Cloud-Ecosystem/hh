package audit

import "testing"

// The pos SALE_COMPLETED completion record (pos/service.go) carries the order
// facts a counter trail answers "against what, sold by whom" with, as IDS AND
// REFERENCES only: the PO or job reference, the credited salesperson's staff
// id and the job reference. A person's name and an address never ride the
// trail: salesperson_name and job_address stay dropped at this chokepoint (a
// name is read from its id when shown). The tax relief assessment record
// carries the declared use and the fulfilment code.
func TestSaleCompletedExtraKeys_IdsAndReferencesSurviveNamesAndAddressesDoNot(t *testing.T) {
	e := recordOne(t, SensitiveAccess{
		Action: ActSaleCompleted, EntityType: "pos.sale", EntityID: sampleUUID,
		Reason: "CASH",
		Extra: map[string]any{
			"po_job_ref":       "PO 4471 / SMITH DECK",
			"salesperson_id":   sampleUUID,
			"job_reference":    "SMITH-DECK",
			"salesperson_name": "A Person",
			"job_address":      "128 Front St, Trenton ON",
		},
	})
	if e.Changes["po_job_ref"] != "PO 4471 / SMITH DECK" {
		t.Errorf("po_job_ref must survive verbatim: %+v", e.Changes)
	}
	if e.Changes["salesperson_id"] != sampleUUID {
		t.Errorf("salesperson_id must survive: %+v", e.Changes)
	}
	if e.Changes["job_reference"] != "SMITH-DECK" {
		t.Errorf("job_reference must survive verbatim: %+v", e.Changes)
	}
	for _, k := range []string{"salesperson_name", "job_address"} {
		if _, ok := e.Changes[k]; ok {
			t.Errorf("%s is a name or an address and must stay dropped: %+v", k, e.Changes)
		}
	}
}

// po_job_ref and job_reference are free text: a value carrying a run of seven
// or more digits redacts to [redacted] at the chokepoint like any other string.
func TestSaleCompletedExtraKeys_LongDigitRunInAReferenceRedacts(t *testing.T) {
	e := recordOne(t, SensitiveAccess{
		Action: ActSaleCompleted, EntityType: "pos.sale", EntityID: sampleUUID,
		Extra: map[string]any{
			"po_job_ref":    "PO 12345678",
			"job_reference": "JOB-98765432",
		},
	})
	if e.Changes["po_job_ref"] != "[redacted]" || e.Changes["job_reference"] != "[redacted]" {
		t.Errorf("a long digit run in a reference must redact: %+v", e.Changes)
	}
}

func TestSaleCompletedExtraKeys_AreNotOperatorSafe(t *testing.T) {
	for _, k := range []string{"po_job_ref", "salesperson_id", "job_reference", "declared_use", "fulfilment"} {
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
