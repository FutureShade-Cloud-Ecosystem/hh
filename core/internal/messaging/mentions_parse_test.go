package messaging

// mentions_parse_test.go: the mention syntax's unit legs, DB-free, against the
// parse in mentions.go (the syntax's one place, ruling 5). The integration
// suite proves the stored half end to end; these legs pin the match rules a
// body can turn on: the display name, the e-mail handle, the role code and
// label, longest name first, case folded, boundary exact, repeats collapsed.

import "testing"

var (
	mpSam  = mentionPerson{ID: "u-sam", Name: "Sam Rivera", EmailLocal: "sam.rivera"}
	mpCase = mentionPerson{ID: "u-casey", Name: "Casey Doyle", EmailLocal: "casey.doyle"}
	mrYard = mentionRole{Code: "YARD_MANAGER", Name: "Yard Manager"}
	mrPay  = mentionRole{Code: "PAYABLES_RECEIVABLES", Name: "Payables/Receivables"}
)

func mentionsOf(body string) MessageMentions {
	return parseMentions(body, []mentionPerson{mpSam, mpCase}, []mentionRole{mrYard, mrPay})
}

func assertMentions(t *testing.T, got MessageMentions, users, roles []string) {
	t.Helper()
	if len(got.UserIDs) != len(users) || len(got.RoleCodes) != len(roles) {
		t.Fatalf("mentions=%+v, want users %v roles %v", got, users, roles)
	}
	for i, u := range users {
		if got.UserIDs[i] != u {
			t.Fatalf("user %d: got %s want %s (%+v)", i, got.UserIDs[i], u, got)
		}
	}
	for i, r := range roles {
		if got.RoleCodes[i] != r {
			t.Fatalf("role %d: got %s want %s (%+v)", i, got.RoleCodes[i], r, got)
		}
	}
}

func TestParseMentions_DisplayName_Handle_Role_Label_And_Code(t *testing.T) {
	assertMentions(t, mentionsOf("@Sam Rivera can you take this one?"),
		[]string{"u-sam"}, nil)
	assertMentions(t, mentionsOf("ask @sam.rivera at the desk"),
		[]string{"u-sam"}, nil)
	// The role answers to its code and to its label, case folded either way.
	assertMentions(t, mentionsOf("@yard_manager to the dock please"),
		nil, []string{"YARD_MANAGER"})
	assertMentions(t, mentionsOf("paging @Yard Manager to the dock"),
		nil, []string{"YARD_MANAGER"})
	assertMentions(t, mentionsOf("@yard manager, the dock"),
		nil, []string{"YARD_MANAGER"})
	// A label carrying an in-word mark matches whole.
	assertMentions(t, mentionsOf("@Payables/Receivables to the till"),
		nil, []string{"PAYABLES_RECEIVABLES"})
	// Several targets in one body, repeats collapsed.
	assertMentions(t, mentionsOf("@Sam Rivera and @Casey Doyle and @Sam Rivera again"),
		[]string{"u-sam", "u-casey"}, nil)
	assertMentions(t, mentionsOf("@Sam Rivera and @yard_manager to the dock"),
		[]string{"u-sam"}, []string{"YARD_MANAGER"})
}

func TestParseMentions_Boundaries_And_Nobody(t *testing.T) {
	// No @, no mention.
	assertMentions(t, mentionsOf("plain words for the room"), nil, nil)
	// An @ inside an address (preceded by a letter) is not a mention.
	assertMentions(t, mentionsOf("write to sam.rivera@example.example instead"), nil, nil)
	// A longer handle that continues past the candidate names nobody.
	assertMentions(t, mentionsOf("@sam.riverax is not Sam"), nil, nil)
	// A name cut short by a longer neighbour's text names nobody when the
	// longer candidate does not match; the shorter one never steals.
	assertMentions(t, mentionsOf("@SamRivers is not Sam Rivera"), nil, nil)
	// A sentence period ends the name: the mention still lands, full name or
	// dotted handle alike.
	assertMentions(t, mentionsOf("thanks @Sam Rivera. the pallet is in"),
		[]string{"u-sam"}, nil)
	assertMentions(t, mentionsOf("thanks @sam.rivera. done"),
		[]string{"u-sam"}, nil)
	// A name stops at a comma, a colon, an exclamation.
	assertMentions(t, mentionsOf("@Sam Rivera, @Casey Doyle: nice work!"),
		[]string{"u-sam", "u-casey"}, nil)
	// An empty body is an empty answer, and the parse is nil-safe on it.
	assertMentions(t, parseMentions("", []mentionPerson{mpSam}, []mentionRole{mrYard}), nil, nil)
}

// The longest candidate wins at one @: with both "Sam" (a second member's
// short name) and "Sam Rivera" in the room, the longer name resolves and the
// short one does not fire.
func TestParseMentions_LongestFirst(t *testing.T) {
	short := mentionPerson{ID: "u-short", Name: "Sam", EmailLocal: "sam"}
	got := parseMentions("@Sam Rivera to the yard", []mentionPerson{short, mpSam}, nil)
	assertMentions(t, got, []string{"u-sam"}, nil)
	got = parseMentions("@Sam to the yard", []mentionPerson{short, mpSam}, nil)
	assertMentions(t, got, []string{"u-short"}, nil)
}

// Equal width keeps the flatten order: a person ahead of a role. The candidates
// are flattened persons first (display name, then e-mail local part) and roles
// after, and the sort is stable, so a person and a role that share a name (a
// member named "Yard" beside a role labelled "Yard", a member's "sales" handle
// beside the SALES code) tie on width with the person's candidate in front and
// the @ resolves to the person. The list carries more than a dozen candidates
// on purpose: slices.SortFunc is a pdqsort that keeps tie order only inside its
// insertion-sort band, and this test must fail if that variant comes back.
func TestMentionCandidates_EqualWidthKeepsPersonBeforeRole(t *testing.T) {
	persons := []mentionPerson{
		{ID: "u-ana", Name: "Ana Fields", EmailLocal: "ana.fields"},
		{ID: "u-bo", Name: "Bo Chan", EmailLocal: "bo.chan"},
		{ID: "u-yard", Name: "Yard", EmailLocal: "yard.person"},
		{ID: "u-sales", Name: "Sales Desk", EmailLocal: "sales"},
	}
	roles := []mentionRole{
		{Code: "YARD", Name: "Yard"},
		{Code: "SALES", Name: "Sales Desk"},
		{Code: "OFFICE", Name: "Office"},
		{Code: "CREW", Name: "Crew"},
	}
	cands := mentionCandidates(persons, roles)
	if len(cands) <= 12 {
		t.Fatalf("candidate count %d does not cross pdqsort's insertion-sort band", len(cands))
	}
	posOf := func(match func(mentionCandidate) bool) int {
		for i, c := range cands {
			if match(c) {
				return i
			}
		}
		t.Fatalf("candidate not found in %+v", cands)
		return -1
	}
	for _, tie := range []struct{ text, userID, roleCode string }{
		{"yard", "u-yard", "YARD"},
		{"sales", "u-sales", "SALES"},
	} {
		person := posOf(func(c mentionCandidate) bool { return c.name == tie.text && c.user == tie.userID })
		role := posOf(func(c mentionCandidate) bool { return c.name == tie.text && c.role == tie.roleCode })
		if person > role {
			t.Fatalf("tie %q: the role's candidate (%d) is ahead of the person's (%d)", tie.text, role, person)
		}
	}
	// The comparator is unchanged: width alone, descending, through the list.
	for i := 1; i < len(cands); i++ {
		if cands[i-1].width < cands[i].width {
			t.Fatalf("candidates %d and %d rise in width: %d then %d", i-1, i, cands[i-1].width, cands[i].width)
		}
	}
	// The parse agrees: at a shared name the @ names the person, not the role.
	assertMentions(t, parseMentions("@Yard and @sales please", persons, roles),
		[]string{"u-yard", "u-sales"}, nil)
}
