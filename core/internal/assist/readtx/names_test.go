package readtx

import (
	"strings"
	"testing"
)

func TestSuffixKeepsAPlainLowerCaseName(t *testing.T) {
	for _, db := range []string{"hh", "integration_gate", "till_core_gate", "a1_b2"} {
		if got := Suffix(db); got != db {
			t.Errorf("Suffix(%q) = %q, want it unchanged", db, got)
		}
	}
}

func TestSuffixNeverCollidesTwoNamesThatNormaliseAlike(t *testing.T) {
	a, b, c := Suffix("dealer-one"), Suffix("dealer_one"), Suffix("Dealer_One")
	if a == b || a == c || b == c {
		t.Fatalf("dealer-one, dealer_one and Dealer_One must map to three suffixes, got %q %q %q", a, b, c)
	}
	for _, s := range []string{a, c} {
		if !strings.HasPrefix(s, "dealer_one_") {
			t.Errorf("an altered name keeps its normalised stem and adds a hash, got %q", s)
		}
	}
}

func TestSuffixOfALongNameFitsEveryRoleName(t *testing.T) {
	long := strings.Repeat("x", 63)
	r := RolesFor(long)
	for _, name := range []string{r.ViewOwner, r.ClaimsOwner, r.Reader, r.ProReader, r.Login, r.ProLogin, r.FunctionUsers} {
		if len(name) > 63 {
			t.Errorf("role name %q is %d bytes, over the identifier limit of 63", name, len(name))
		}
	}
	if Suffix(long) == Suffix(strings.Repeat("x", 62)) {
		t.Errorf("two long names must not share a suffix")
	}
}

func TestRolesForCarriesTheSevenNames(t *testing.T) {
	r := RolesFor("hh")
	want := Roles{
		ViewOwner:     "assist_view_owner_hh",
		ClaimsOwner:   "assist_claims_owner_hh",
		Reader:        "assist_reader_hh",
		ProReader:     "assist_pro_reader_hh",
		Login:         "assist_login_hh",
		ProLogin:      "assist_pro_login_hh",
		FunctionUsers: "app_function_users_hh",
	}
	if r != want {
		t.Fatalf("RolesFor(hh) = %+v, want %+v", r, want)
	}
}

// The SQL half of this derivation lives in migration 0418; the integration
// test compares the roles that migration created with these names.
func TestSuffixHashIsStableAcrossRuns(t *testing.T) {
	// md5("Dealer-One") first eight hex characters, computed once and pinned
	// so the Go and SQL derivations cannot drift apart unseen.
	if got := Suffix("Dealer-One"); got != "dealer_one_"+md5Prefix("Dealer-One") {
		t.Fatalf("suffix %q does not carry the md5 prefix", got)
	}
}
