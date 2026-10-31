package billing

import "testing"

func TestSameCreditBody_EveryFieldOfTheCreditCounts(t *testing.T) {
	existing := InvoiceDoc{CustomerID: "c1", BranchID: "b1", ReversesInvoiceID: "i1", SubtotalCents: -5000, jobID: "j1"}
	base := CreditMemoInput{CustomerID: "c1", BranchID: "b1", ReversesInvoiceID: "i1", JobID: "j1", AmountCents: 5000}
	if !sameCreditBody(existing, base) {
		t.Fatal("the same credit must match")
	}
	whole := base
	whole.AmountCents = 0
	if !sameCreditBody(existing, whole) {
		t.Fatal("a zero amount means the whole of what is left and matches any amount")
	}
	for name, mut := range map[string]func(*CreditMemoInput){
		"customer": func(i *CreditMemoInput) { i.CustomerID = "c2" },
		"branch":   func(i *CreditMemoInput) { i.BranchID = "b2" },
		"invoice":  func(i *CreditMemoInput) { i.ReversesInvoiceID = "i2" },
		"job":      func(i *CreditMemoInput) { i.JobID = "j2" },
		"amount":   func(i *CreditMemoInput) { i.AmountCents = 4999 },
	} {
		in := base
		mut(&in)
		if sameCreditBody(existing, in) {
			t.Fatalf("a different %s must not match", name)
		}
	}
}
