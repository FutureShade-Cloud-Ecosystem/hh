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
