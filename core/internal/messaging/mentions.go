package messaging

import (
	"slices"
	"strings"
	"unicode"
)

// mentions.go is THE one place the mention syntax lives (walk change 7, ruling
// 5, row 2139: the mentions view lists messages naming the caller or the
// caller's role). Colton may change any ruling, so the syntax is kept here and
// nowhere else: a mention is an @ at a word boundary followed by a candidate's
// name, matched case-insensitively and longest first. The candidates are the
// channel's members (by display name and by e-mail local part, the vocabulary
// the 0152 badge heuristic answered to) and the house's active roles (by code
// and by name). A person mention stores the member's user id; a role mention
// stores the role's canonical code. Nothing else in the package parses a body.

// mentionPerson is one candidate a body may name by person: the member's user
// id, display name and e-mail local part (lowercased).
type mentionPerson struct {
	ID         string
	Name       string
	EmailLocal string
}

// mentionRole is one candidate a body may name by role: the role's canonical
// code and its display name.
type mentionRole struct {
	Code string
	Name string
}

// MessageMentions is the parsed mention payload of one message: the members it
// names and the roles it names. Both empty is the ordinary post.
type MessageMentions struct {
	UserIDs   []string
	RoleCodes []string
}

// mentionCandidate is one matchable name with the target it resolves to.
type mentionCandidate struct {
	name  string // lowercased match text
	user  string // user id when this names a person, else ""
	role  string // canonical role code when this names a role, else ""
	width int    // rune width of the match text, longest first wins
}

// parseMentions scans body for @<name> tokens and resolves each against the
// candidates. A match must start at a word boundary (the body's start or after
// a space) and end at one (the body's end or before a space or punctuation the
// candidate itself does not continue with), so "@Sam" never lands inside
// "@SamRivera" and the @ in "a@b.example" (preceded by a letter) is not a
// mention at all. Names with spaces ("Sam Rivera", "Yard Manager") match
// across the space: the scan tries every candidate at every @, longest first,
// so "@Sam Rivera" resolves to the person and never to a shorter "@Sam".
// Repeats collapse: naming the same person twice stores one row.
func parseMentions(body string, persons []mentionPerson, roles []mentionRole) MessageMentions {
	cands := mentionCandidates(persons, roles)
	out := MessageMentions{}
	seenUser := map[string]bool{}
	seenRole := map[string]bool{}

	lower := strings.ToLower(body)
	runes := []rune(lower)
	for i, r := range runes {
		if r != '@' {
			continue
		}
		// The @ itself must sit at a word boundary: the body's start or a
		// character that cannot be mid-word (a letter or digit before it
		// means this @ is inside an address, not a mention).
		if i > 0 && isWordRune(runes[i-1]) {
			continue
		}
		for _, c := range cands {
			end := i + 1 + c.width
			if end > len(runes) {
				continue
			}
			if !strings.HasPrefix(string(runes[i+1:]), c.name) {
				continue
			}
			// The candidates run longest first, so this is the LONGEST name
			// that matches the text here. If the text continues past it (a
			// word rune follows), the body carries a longer handle the
			// candidates do not name, and no shorter candidate may steal a
			// piece of it: "@sam.riverax" names nobody, "@Sam" in "@SamRivera"
			// does not fire.
			if end < len(runes) && isWordRune(runes[end]) {
				break
			}
			if c.user != "" {
				if !seenUser[c.user] {
					seenUser[c.user] = true
					out.UserIDs = append(out.UserIDs, c.user)
				}
			} else if !seenRole[c.role] {
				seenRole[c.role] = true
				out.RoleCodes = append(out.RoleCodes, c.role)
			}
			break // the longest candidate at this @ won; one target per @
		}
	}
	return out
}

// mentionCandidates flattens the persons and roles into one longest-first list
// of matchable names. A candidate whose name is empty (a member with a blank
// display name, a role with a blank label) is skipped; every remaining name is
// lowercased and measured in runes so the width check above is exact.
func mentionCandidates(persons []mentionPerson, roles []mentionRole) []mentionCandidate {
	cands := make([]mentionCandidate, 0, len(persons)*2+len(roles)*2)
	add := func(name, user, role string) {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return
		}
		cands = append(cands, mentionCandidate{name: name, user: user, role: role, width: len([]rune(name))})
	}
	for _, p := range persons {
		add(p.Name, p.ID, "")
		add(p.EmailLocal, p.ID, "")
	}
	for _, ro := range roles {
		add(ro.Name, "", ro.Code)
		add(ro.Code, "", ro.Code)
	}
	// Longest first so the scan's first hit at an @ is the longest name that
	// matches there. The sort is STABLE, so at equal width the flatten order
	// above wins: a person (name, then e-mail local part) stays ahead of a role
	// that shares the name, and one target per @ resolves the @ to the person.
	slices.SortStableFunc(cands, func(a, b mentionCandidate) int { return b.width - a.width })
	return cands
}

// isWordRune reports whether r continues a name: a letter, a digit or the
// underscore the role codes carry. A name may also CONTAIN the in-word marks
// ('.', '/', '-') and the match test handles those through the candidate text
// itself; the boundary tests only ask whether a name may START or STOP beside
// a character. So "@sam.rivera," keeps the whole handle (the comma stops it),
// "@Sam. Please look" mentions Sam (the sentence period stops the name), and
// "@sam.riverax" mentions nobody (the trailing letter proves a longer handle
// the candidate list did not name).
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
