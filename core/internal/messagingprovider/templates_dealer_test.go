package messagingprovider

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/dealer"
)

func renderedTexts() []string {
	r := Recipient{Name: "Pat", Phone: "+15550100"}
	return []string{
		RenderAR(ARNotice{InvoiceNo: "INV-1", AmountCents: 1000, DueDate: "2026-01-02"}, r).Body,
		RenderAR(ARNotice{InvoiceNo: "INV-1", AmountCents: 1000, DaysPastDue: 5}, r).Body,
		RenderAR(ARNotice{InvoiceNo: "STMT-1", AmountCents: 1000}, r).Body,
		RenderDispatch(DispatchNotice{OrderNo: "SO-1", StageRef: "Q1"}, r).Body,
	}
}

// The SMS signature is the dealer's own configured name.
func TestSMSSignature_ReadsTheConfiguredDealerName(t *testing.T) {
	t.Cleanup(func() { dealer.Set("") })
	dealer.Set("Gable Landscape Supply")
	for _, body := range renderedTexts() {
		if !strings.HasSuffix(body, ", Gable Landscape Supply") {
			t.Errorf("text %q does not end with the configured dealer name", body)
		}
	}
}

// With no dealer name set the signature is a neutral role word, never another dealer's name.
func TestSMSSignature_NeutralWhenNoDealerName(t *testing.T) {
	dealer.Set("")
	for _, body := range renderedTexts() {
		if strings.Contains(strings.ToLower(body), "dibbits") {
			t.Errorf("text %q carries a dealer's name from code", body)
		}
		if !strings.HasSuffix(body, ", your dealer") {
			t.Errorf("text %q does not end with the neutral signature", body)
		}
	}
}
