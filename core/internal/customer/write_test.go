package customer

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// fakeWriter records what the service persists so the tests can assert the address was
// normalized before it reached the store.
type fakeWriter struct {
	lastCreate CreateInput
	lastAddr   map[string]any
}

func (f *fakeWriter) InsertCustomer(_ context.Context, in CreateInput) (Account, error) {
	f.lastCreate = in
	return Account{CustomerID: "new-id", AccountNo: in.AccountNo, Name: in.Name}, nil
}

func (f *fakeWriter) UpdateBillingAddress(_ context.Context, customerID string, addr map[string]any) (Account, error) {
	f.lastAddr = addr
	return Account{CustomerID: customerID}, nil
}

func isBad(err error) bool {
	var bre httpx.BadRequestError
	return errors.As(err, &bre)
}

// TestCreateCustomer_NormalizesAddress, a valid lowercase/no-space postal + lowercase
// province is normalized before persistence (M5).
func TestCreateCustomer_NormalizesAddress(t *testing.T) {
	w := &fakeWriter{}
	svc := NewService(nil).WithWriter(w)
	_, err := svc.CreateCustomer(context.Background(), CreateInput{
		AccountNo: "DIB-9001", Name: "Quinte Hardscapes",
		BillingAddress: map[string]any{"line1": "1 Yard Rd", "city": "Trenton", "province": "on", "postal": "k8v5p7"},
	})
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}
	if got := w.lastCreate.BillingAddress["postal"]; got != "K8V 5P7" {
		t.Fatalf("postal persisted as %v, want K8V 5P7", got)
	}
	if got := w.lastCreate.BillingAddress["province"]; got != "ON" {
		t.Fatalf("province persisted as %v, want ON", got)
	}
}

// TestCreateCustomer_RejectsUSZip, a US ZIP is a 400, nothing persisted.
func TestCreateCustomer_RejectsUSZip(t *testing.T) {
	w := &fakeWriter{}
	svc := NewService(nil).WithWriter(w)
	_, err := svc.CreateCustomer(context.Background(), CreateInput{
		AccountNo: "DIB-9002", Name: "Bad Address Co",
		BillingAddress: map[string]any{"province": "ON", "postal": "90210"},
	})
	if err == nil || !isBad(err) {
		t.Fatalf("US ZIP should be a bad-request 400, got %v", err)
	}
	if w.lastCreate.AccountNo != "" {
		t.Fatal("customer was persisted despite an invalid address")
	}
}

// TestCreateCustomer_RejectsBadProvince, a non-Canadian province is a 400.
func TestCreateCustomer_RejectsBadProvince(t *testing.T) {
	svc := NewService(nil).WithWriter(&fakeWriter{})
	_, err := svc.CreateCustomer(context.Background(), CreateInput{
		AccountNo: "DIB-9003", Name: "Wrong Province Co",
		BillingAddress: map[string]any{"province": "NY", "postal": "K8V 5P7"},
	})
	if err == nil || !isBad(err) {
		t.Fatalf("province NY should be a bad-request 400, got %v", err)
	}
}

// TestUpdateBillingAddress_Normalizes, the update path applies the same normalization.
func TestUpdateBillingAddress_Normalizes(t *testing.T) {
	w := &fakeWriter{}
	svc := NewService(nil).WithWriter(w)
	if _, err := svc.UpdateBillingAddress(context.Background(), "cust-1",
		map[string]any{"province": "qc", "postal": "h2x1y4"}); err != nil {
		t.Fatalf("UpdateBillingAddress: %v", err)
	}
	if w.lastAddr["postal"] != "H2X 1Y4" || w.lastAddr["province"] != "QC" {
		t.Fatalf("address not normalized: %+v", w.lastAddr)
	}
}

// TestCreateCustomer_RequiredFields, account_no + name are required.
func TestCreateCustomer_RequiredFields(t *testing.T) {
	svc := NewService(nil).WithWriter(&fakeWriter{})
	if _, err := svc.CreateCustomer(context.Background(), CreateInput{Name: "No Acct"}); !isBad(err) {
		t.Fatal("missing account_no should be a 400")
	}
	if _, err := svc.CreateCustomer(context.Background(), CreateInput{AccountNo: "X"}); !isBad(err) {
		t.Fatal("missing name should be a 400")
	}
}
