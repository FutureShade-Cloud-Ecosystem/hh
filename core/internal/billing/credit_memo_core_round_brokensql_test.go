//go:build integration

package billing

// credit_memo_core_round_brokensql_test.go: the guard's own unit legs (FLAKE-13).
//
// The approval cap leg's guard reads the SQL state from the error's kind. Two states are the
// broken SQL answers the re-check must never give: 42725 ("operator is not unique") and 42702
// ("column reference is ambiguous"), which a re-check that joins two tables raises. These legs
// need no database: they feed the error and read the answer.

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// TestBrokenSQLState_NamesTheAmbiguousColumnState is the leg the old guard had no name for:
// a 42702 is a broken SQL answer too, and a re-check that joins two tables raises it.
func TestBrokenSQLState_NamesTheAmbiguousColumnState(t *testing.T) {
	err := fmt.Errorf("billing: read what is already credited at the approval: %w", &pgconn.PgError{
		Code:    "42702",
		Message: `column reference "amount" is ambiguous`,
	})
	state, broken := brokenSQLState(err)
	if !broken {
		t.Fatalf("the guard read %q from a 42702, want the broken SQL state named", state)
	}
	if state != "42702" {
		t.Fatalf("the guard read the state %q, want 42702", state)
	}
}

// TestBrokenSQLState_NamesTheAmbiguousOperatorState keeps the state the guard always named.
func TestBrokenSQLState_NamesTheAmbiguousOperatorState(t *testing.T) {
	err := fmt.Errorf("billing: read what is already credited at the approval: %w", &pgconn.PgError{
		Code:    "42725",
		Message: "operator is not unique: unknown + unknown",
	})
	state, broken := brokenSQLState(err)
	if !broken {
		t.Fatalf("the guard read %q from a 42725, want the broken SQL state named", state)
	}
	if state != "42725" {
		t.Fatalf("the guard read the state %q, want 42725", state)
	}
}

// TestBrokenSQLState_SaysFalseOnACorrectRefusal is the flake the guard was made for: the cap's
// own refusal is not a database error at all, so no state is read from it.
func TestBrokenSQLState_SaysFalseOnACorrectRefusal(t *testing.T) {
	err := httpx.BadRequestf("the credits already issued against invoice %s leave %s before tax", "ABC-926427256", "-$600.00")
	state, broken := brokenSQLState(err)
	if broken {
		t.Fatalf("the guard called the cap's own refusal broken SQL (state %s), want no state", state)
	}
}

// TestBrokenSQLState_SaysFalseOnAnotherState keeps the guard's door narrow: a Postgres error of
// a state the re-check does not raise is not a broken SQL answer this leg names.
func TestBrokenSQLState_SaysFalseOnAnotherState(t *testing.T) {
	err := fmt.Errorf("billing: finalize parked credit memo: %w", &pgconn.PgError{
		Code:    "42703",
		Message: `column "balance_after" of relation "customer_transactions" does not exist`,
	})
	state, broken := brokenSQLState(err)
	if broken {
		t.Fatalf("the guard called a 42703 broken SQL (state %s), want no state", state)
	}
}
