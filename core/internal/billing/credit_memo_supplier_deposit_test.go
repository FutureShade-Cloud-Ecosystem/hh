package billing

// credit_memo_supplier_deposit_test.go, item 9 delta review P2-C: a credit
// memo against a SUPPLIER_DEPOSIT document is refused in words. The deposit
// document posted DR AR / CR Customer Deposits and booked no revenue and no
// tax, so the memo's ordinary reversing entry (DR revenue, DR HST, CR AR)
// would debit revenue that was never credited and leave the deposit liability
// overstated; the ledger would balance and the accounts would be wrong. The
// release item that follows item 9 cancels deposits properly; until then the
// refusal is the honest answer, at the register's own door as everywhere
// else. (The register carries no memo issuing route today; this leg pins the
// door's error mapping so the refusal answers in words the day one lands.)

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterWriteErr_SupplierDepositMemoRefusalAnswersInWords(t *testing.T) {
	h := NewRegisterHandler(nil, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ar/register/invoices/x/credit-memo", nil)
	h.writeErr(rec, req, &SupplierDepositNotCreditable{InvoiceNo: "#INV-42"})

	if rec.Code != http.StatusConflict {
		t.Fatalf("the refusal answers %d, want 409", rec.Code)
	}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	if body.Error != SupplierDepositNotCreditableWords {
		t.Fatalf("the refusal's words are %q, want the fixed sentence %q", body.Error, SupplierDepositNotCreditableWords)
	}
	if body.Code != SupplierDepositNotCreditableCode {
		t.Fatalf("the refusal's stable code is %q, want %q", body.Code, SupplierDepositNotCreditableCode)
	}
}
