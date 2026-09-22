//go:build integration

// duplicate_account_integration_test.go, regression guard for the MAJOR recon finding against
// the live pgx Repository: a second insert with the same account_no (CITEXT NOT NULL UNIQUE,
// migration 0006) must return the ErrDuplicateAccountNo sentinel, NOT a wrapped fmt.Errorf that
// the handler would surface as a 500. CITEXT means the collision is case-insensitive, so the
// duplicate uses a different case to also pin the CITEXT semantics.
//
//	Run: DATABASE_URL=postgres://hardscape:hardscape@localhost:5435/hardscapeos go test \
//	  -tags integration ./internal/customer/... -run TestIntegration_DuplicateAccountNo
package customer

import (
	"context"
	"errors"
	"testing"
)

func TestIntegration_DuplicateAccountNo_ReturnsSentinel(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	acctNo := "DIB-DUP-" + randSuffix()
	first, err := repo.InsertCustomer(ctx, CreateInput{
		AccountNo: acctNo, Name: "Dup Guard Co", CustomerType: "CONTRACTOR",
	})
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, first.CustomerID)
	})

	// Same account_no in a different case: CITEXT UNIQUE still collides.
	_, err = repo.InsertCustomer(ctx, CreateInput{
		AccountNo: toDifferentCase(acctNo), Name: "Dup Guard Co 2", CustomerType: "CONTRACTOR",
	})
	if !errors.Is(err, ErrDuplicateAccountNo) {
		t.Fatalf("duplicate account_no: got err %v, want ErrDuplicateAccountNo", err)
	}
}

// toDifferentCase flips the case of the last letter so the duplicate differs textually but
// collides under CITEXT.
func toDifferentCase(s string) string {
	b := []byte(s)
	for i := len(b) - 1; i >= 0; i-- {
		switch {
		case b[i] >= 'a' && b[i] <= 'z':
			b[i] -= 32
			return string(b)
		case b[i] >= 'A' && b[i] <= 'Z':
			b[i] += 32
			return string(b)
		}
	}
	return s
}
