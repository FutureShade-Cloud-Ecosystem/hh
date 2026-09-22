package dispatch

// driver_session_policy_test.go, the 0209 knob's bounds, and the number that is written down twice.
//
// TWO THINGS ARE PINNED HERE AND THE SECOND IS THE ONE THAT DECAYS QUIETLY.
//
// The bounds are ordinary: a validator that refuses in words before a CHECK could 500, in the posture
// every other knob on this table already takes. Nothing subtle.
//
// The second is not. `168` appears in three places by necessity, the CHECK in 0209, the Go bound
// here, and hhidentity.MaxDriverSessionTTL, which is the outer lifetime the token verifier will honour
// on a driver token. They cannot be one constant: identity must not import dispatch (the dependency
// runs the other way), and the database does not import either. So they are three copies of one
// number, and three copies of one number is exactly the shape that drifts. TestDriverSessionBounds_
// MatchTheIdentityCeiling makes two of them a compile-and-run failure if they part; 0209's header
// names this test so a person raising the CHECK finds it.
//
// WHY THE DRIFT WOULD BE SILENT EITHER WAY. Ceiling BELOW the storable maximum: a dealer sets 168, a
// driver signs in, and their token is refused by the very next request, a login that works and then
// does not. Ceiling ABOVE it: nothing fails at all, which is worse, because a backstop that can never
// fire is indistinguishable from a backstop that is doing its job.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	hhidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// CARRY NOTE (guards swap, HH-3.2 B5): the pin compared against the ERP
// identity package's session ceiling; under the carry the ceiling lives in
// the Phase B identity package (the HS256 world that held it stayed
// behind), so the cross-check that pins the storable maximum to the
// honoured ceiling re-points at that package. The number is the same 168.

func TestDriverSessionBounds_MatchTheIdentityCeiling(t *testing.T) {
	if got := time.Duration(MaxDriverSessionHours) * time.Hour; got != hhidentity.MaxDriverSessionTTL {
		t.Fatalf("the storable maximum (%v) and the honoured ceiling (%v) must be the same number, "+
			"below it, a dealer's legitimate setting mints a token nothing will verify; above it, the "+
			"backstop can never fire and nobody can tell", got, hhidentity.MaxDriverSessionTTL)
	}
	// The default is TODAY'S CONSTANT written down, not a new policy. A dealer who never opens the
	// settings screen must get the session they already have.
	if time.Duration(DefaultDriverSessionHours)*time.Hour != 12*time.Hour {
		t.Fatalf("the default must stay identity's twelve hours, got %d", DefaultDriverSessionHours)
	}
}

func TestValidatePolicy_DriverSessionHoursRefusesOutOfBoundsInWords(t *testing.T) {
	base := Policy{CommitPromptHours: 48, ReopenPolicy: ReopenAnyone, LoadValueVisibility: settings.VisibilityAllStaff}

	for _, tc := range []struct {
		name  string
		hours int
	}{
		// Zero is handled separately below, it NORMALISES rather than failing, because an omitted
		// field is not a request for a zero-hour session.
		{"negative", -1},
		{"one past the ceiling", MaxDriverSessionHours + 1},
		{"a year", 8760},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			p.DriverSessionHours = tc.hours
			_, err := ValidatePolicy(p)
			if err == nil {
				t.Fatalf("%d hours must be refused in words before the CHECK could 500", tc.hours)
			}
			// 422-in-words, the settings kit's posture, not a 500 carrying a constraint name.
			var se *settings.StatusError
			if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity {
				t.Fatalf("the refusal must be a 422 in words, got %v", err)
			}
		})
	}

	// THE BOUNDS ARE INCLUSIVE ON BOTH SIDES, exactly as `BETWEEN 1 AND 168` is. An hour is a real
	// setting, a dealer who wants drivers signing in constantly, and a week is the ceiling itself.
	for _, hours := range []int{1, 12, MaxDriverSessionHours} {
		p := base
		p.DriverSessionHours = hours
		out, err := ValidatePolicy(p)
		if err != nil {
			t.Fatalf("%d hours is inside the CHECK and must be storable: %v", hours, err)
		}
		if out.DriverSessionHours != hours {
			t.Fatalf("a deliberate %d must survive validation, got %d", hours, out.DriverSessionHours)
		}
	}

	// AN OMITTED VALUE NORMALISES TO THE DEFAULT rather than being honoured as a zero. This is the one
	// place this knob differs from commit_prompt_hours a few lines above it in the validator: a zero
	// prompt window is a dealer switching the nagging off, and a zero-hour session is a person signed
	// out at the moment they sign in.
	out, err := ValidatePolicy(Policy{})
	if err != nil {
		t.Fatalf("an empty policy must normalise, not fail: %v", err)
	}
	if out.DriverSessionHours != DefaultDriverSessionHours {
		t.Fatalf("an omitted session length must normalise to %d, got %d",
			DefaultDriverSessionHours, out.DriverSessionHours)
	}
}

// TestDefaultPolicy_FailsClosedToTheSHORTSession. Failing closed on a session length means the SHORT
// number: a policy read that failed must never hand back a week. Twelve hours is what this system did
// before the knob existed, so an unreadable row buys a driver exactly nothing and costs them nothing.
func TestDefaultPolicy_FailsClosedToTheSHORTSession(t *testing.T) {
	if got := DefaultPolicy().DriverSessionHours; got != DefaultDriverSessionHours {
		t.Fatalf("the fallback session must be today's %d hours, got %d", DefaultDriverSessionHours, got)
	}
	if DefaultPolicy().DriverSessionHours >= MaxDriverSessionHours {
		t.Fatal("failing closed must never resolve to the maximum admissible session")
	}
}

// TestDriverSessionHours_FailsClosedWithNoRepository is the seam's shipped behaviour under the outage
// the adapter's comment promises it can never see: with no repository at all, PolicyFor returns
// DefaultPolicy and this method answers twelve. That is why the main.go adapter never returns an
// error, there is no state in which it knows something identity's own fallback does not.
func TestDriverSessionHours_FailsClosedWithNoRepository(t *testing.T) {
	svc := NewService(nil)
	if got := svc.DriverSessionHours(context.Background()); got != DefaultDriverSessionHours {
		t.Fatalf("an unreadable policy must resolve to %d hours, got %d", DefaultDriverSessionHours, got)
	}
}

// TestPolicyChanges_ASessionLengthChangeIsRecorded. A change to how long a credential is honoured is
// the last knob on this table anybody should be able to move without a trace. It rides the same
// field/before/after row as the other five, no new allowlisted audit key, no second history shape.
func TestPolicyChanges_ASessionLengthChangeIsRecorded(t *testing.T) {
	before := Policy{
		CommitPromptHours: 48, ReopenPolicy: ReopenManager, LoadValueVisibility: settings.VisibilityManagerUp,
		CommitHorizonWeeks: 8, WorkingViewDays: 14, FreightSplitMode: FreightSplitEven,
		DriverSessionHours: DefaultDriverSessionHours,
	}
	after := before
	after.DriverSessionHours = 72

	got := policyChanges(before, after)
	if len(got) != 1 {
		t.Fatalf("one knob moved, so one row: %+v", got)
	}
	if got[0].Field != "driver_session_hours" || got[0].Before != "12" || got[0].After != "72" {
		t.Fatalf("both sides of the change must be recorded under the column's own name: %+v", got[0])
	}
	if len(policyChanges(before, before)) != 0 {
		t.Fatal("an unchanged save must still record nothing")
	}
}
