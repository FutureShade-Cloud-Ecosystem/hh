// Package tour is the guided demo walkthrough, the nine chapters of migration 0192, the steps
// inside them, and the bookmark of 0193 that says where one person had got to.
//
// -- THE ACCEPTANCE CLAUSE THAT SHAPES THE WHOLE PACKAGE -----------------------------------------
//
// "Byte-absent from payloads when off, not CSS-hidden." When the deployment is not a demo, or the
// dealer has turned the tour off, GET /api/v1/tour answers the two bytes `{}`. Not
// `{"chapters":null}`, not `{"chapters":[]}`, and above all not a populated body the client is
// trusted to throw away, a client that is trusted to hide something has been handed it.
//
// THE MECHANISM IS THE TYPE, NOT THE HANDLER. `Tour` carries omitempty on every field and exactly
// one method builds it (Service.Tour). A refused caller gets the ZERO VALUE, which marshals as
// `{}` because there is no field left that could marshal to anything. That is the shipped house
// pattern, dispatch's LoadStop money fields, pos.MarginGlance, billing's ARLanding.Advanced, and
// it is chosen over "the handler decides what to write" for one reason: a gate that lives in a
// handler is one refactor away from a second handler that forgot it, while a gate that lives in
// the type travels with the type.
//
// NULL IS NOT ABSENCE, AND THIS IS WHERE THAT IS PAID FOR. `[]Chapter`, `[]Mark` and `[]Step` are
// the three shapes in this file that marshal as the literal `null` the moment one of them is given
// a json tag without omitempty. Three separate trains in this repo shipped a `"field":null` believing it was
// the same as an absent key; it is not, because `"chapters" in payload` is true for both. The unit
// gate scans the OFF bytes for the string "null" for exactly that reason.
//
// -- THE FIELD NAMES ARE THE COLUMN NAMES, AND POSITION IS ON THE WIRE --------------------------
//
// `chapter_key` / `step_key` / `position`, not `key` / `index`. The consumer (app/src/_tour.ts)
// normalises at its own loader boundary and re-sorts by (position, key) because it is written not
// to trust a body's order, the house zero-trust convention for every payload. That makes POSITION
// LOAD-BEARING rather than redundant: a payload that omitted it would leave every chapter at 0 and
// the client would sort the nine alphabetically, opening the demo on `bulk-board`. So the server
// sorts, and sends the key it sorted by, and the two agree.
//
// omitempty ON AN int IS SAFE HERE, WHICH IS NOT GENERALLY TRUE. Position 0 is a real value, it is
// the FIRST chapter, and omitempty drops it. It survives because the consumer reads a missing
// number as 0 (`_tour.ts`'s `num()`), so absent and zero mean the same thing on both sides of the
// wire. That coincidence is what lets the byte-absence rule stay total: every field, no exceptions,
// including the two integers.
//
// -- PROGRESS IS A SIBLING COLLECTION, NOT A FIELD ON A CHAPTER ---------------------------------
//
// `{"chapters":[…], "progress":[…]}`, the payload renders the two tables as two collections joined
// on chapter_key, which is what they are. Hanging a bookmark off its chapter would read more
// naturally and is what this file did first; it loses because the client's normaliser reads the two
// independently, and because a progress row belongs to a PERSON while a chapter belongs to the
// deployment, folding one into the other puts a per-caller fact inside a shared one, which is the
// shape that eventually gets cached.
package tour

import (
	"fmt"
	"net/http"
	"regexp"
)

// -- the refusals --------------------------------------------------------------------------------

// StatusError carries an HTTP status with a human sentence, the settings kit's 422-in-words
// posture, copied rather than imported. Importing settings.StatusError would make this module
// depend on the curation kit for a two-field error type and put a settings-shaped sentence in a
// tour refusal; the type is four lines and the coupling would outlive the saving.
type StatusError struct {
	Status int
	Msg    string
}

func (e *StatusError) Error() string { return e.Msg }

// Errf builds a StatusError.
func Errf(status int, format string, a ...any) *StatusError {
	return &StatusError{Status: status, Msg: fmt.Sprintf(format, a...)}
}

// NotPlatformAdminError is the refusal a dealer's administrator gets at a platform write. It is the
// SAME shape settings.NotPlatformAdminError takes, for the same reason 0191:44-70 gives: IsAdmin()
// admits four roles including the dealer's own General Manager, who is unambiguously an
// administrator and still may not edit the copy every deployment reads. A single "you are not an
// administrator" sentence would have to lie to one of them.
//
// The sentence names WHO rather than stopping at "no", the walkthrough-error voice, because a
// General Manager who reaches this has a real question ("then who?") and being told only that they
// may not makes them file a ticket to find out. It covers BOTH platform routes in one clause,
// because one error type serves both and a sentence that named only the copy would be wrong half
// the time it is read.
type NotPlatformAdminError struct{ RoleCode string }

func (e *NotPlatformAdminError) Error() string {
	if e.RoleCode == "" {
		return "tour: the walkthrough is the platform administrator's to set up"
	}
	return fmt.Sprintf(
		"tour: the walkthrough is the platform administrator's to set up, %s cannot change its copy or its demo switch",
		e.RoleCode)
}

// -- the two knobs (0192 §1) ---------------------------------------------------------------------

// Policy is one resolved tour_policy row: the platform operator's demo switch and the dealer's own.
//
// TWO KNOBS, TWO OWNERS, AND THE PRODUCT OF THEM. `demo_mode` defaults FALSE so a real dealer never
// meets a tour by accident; `dealer_enabled` defaults TRUE so turning a deployment into a demo is
// one switch rather than two. The tour renders only when both are true.
type Policy struct {
	BranchID      string `json:"branch_id,omitempty"`
	DemoMode      bool   `json:"demo_mode"`
	DealerEnabled bool   `json:"dealer_enabled"`
}

// DefaultPolicy is what every failure path answers: OFF, by way of a demo_mode nobody set.
//
// It mirrors the column defaults of 0192 rather than inventing a Go-side pair, so "what does an
// unreadable policy mean" and "what does a fresh deployment mean" are the same sentence. Note
// which half is restrictive: DealerEnabled is TRUE here and the answer is still OFF, because the
// product of the two is what renders and demo_mode is the half that must be switched on by hand.
func DefaultPolicy() Policy { return Policy{DemoMode: false, DealerEnabled: true} }

// On reports whether the walkthrough renders at all.
func (p Policy) On() bool { return p.DemoMode && p.DealerEnabled }

// -- the read payload ----------------------------------------------------------------------------

// Mark is one row of tour_user_progress: where THIS caller had got to in one chapter (0193).
//
// A MARK, NOT A "PROGRESS". The collection is the person's progress; one row is a bookmark, and
// naming the row for the collection is what would make `progress.progress` read like a mistake.
//
// ABSENCE IS STILL THE SIGNAL. There is no row for a chapter nobody started, so "never started"
// arrives as no mark at all rather than as a mark of empty strings, 0193 is emphatic that "never
// started" and "went back to the beginning" are the same state to the person, and a third spelling
// is a third state every client then has to learn to fold back into the first two.
type Mark struct {
	ChapterKey string `json:"chapter_key,omitempty"`
	// StepKey is the step they are ON, not the one they finished, so a resume returns them to what
	// they were looking at. Empty (and therefore absent) when the chapter is DONE or DISMISSED.
	StepKey string `json:"step_key,omitempty"`
	State   string `json:"state,omitempty"`
}

// Step is one bubble: where to be, what to point at, what it says, and how the person moves on.
type Step struct {
	StepKey  string `json:"step_key,omitempty"`
	Position int    `json:"position,omitempty"`
	// Route is '', and therefore absent, when the step speaks about wherever the person already
	// is. Absent rather than "" so the client's check is `if (step.route)` and never a comparison
	// against an empty string somebody will one day spell as null.
	Route  string `json:"route,omitempty"`
	Anchor string `json:"anchor,omitempty"`
	Line   string `json:"line,omitempty"`
	// Advance is NEXT | CLICK | ROUTE, 0192's grammar for how the person moves on. NEXT is a
	// control the tour draws; CLICK waits for the anchor itself to be pressed, which is what makes
	// this a walkthrough rather than a slide deck; ROUTE waits for the shell to arrive somewhere.
	Advance string `json:"advance,omitempty"`
}

// Chapter is one chapter and its steps, in order. It carries no bookmark: that is a per-person
// fact and it travels in Tour.Progress beside the chapters, not inside them.
type Chapter struct {
	ChapterKey string `json:"chapter_key,omitempty"`
	Position   int    `json:"position,omitempty"`
	Title      string `json:"title,omitempty"`
	Promise    string `json:"promise,omitempty"`
	// Role is the role whose day the chapter is about. EDITORIAL, NOT AN AUTHORIZATION FENCE
	// (0192 §2): a chapter about the counter is about the counter whether or not this dealer has
	// staffed an INSIDE_SALES seat, and the API is where authorization lives. So the read does not
	// filter chapters by the caller's role, it would hide eight ninths of a demo from the one
	// person being demoed to.
	Role  string `json:"role,omitempty"`
	Route string `json:"route,omitempty"`
	Steps []Step `json:"steps,omitempty"`
}

// Tour is the GET /api/v1/tour answer and the gated payload this package exists to get right.
//
// EVERY FIELD CARRIES omitempty, INCLUDING THE BOOL. That is not tidiness: a struct with one field
// lacking omitempty cannot marshal as `{}`, and the whole acceptance clause is that it can. CanEdit
// is a bool for the same reason PlatformHotkeyView carries one, authority is the server's fact and
// a browser evaluating a role code is a second authority model, and false is absent, which is the
// right reading for a client that asks `if (tour.can_edit)`.
type Tour struct {
	Chapters []Chapter `json:"chapters,omitempty"`
	// Progress carries ONLY the chapters that are also in Chapters, so every row here names
	// something the same body sent. A payload that shipped a bookmark for a retired chapter would
	// hand a client a mark it has nowhere to draw and no rule for what to do with.
	Progress []Mark `json:"progress,omitempty"`
	CanEdit  bool   `json:"can_edit,omitempty"`
}

// -- the write bodies ----------------------------------------------------------------------------

// StepEdit is the PUT /api/v1/platform/tour/steps body: which step, and the three fields a
// platform administrator may change.
//
// THREE FIELDS, AND THE OMISSIONS ARE THE DESIGN. `position` and `advance` are the walkthrough's
// STRUCTURE, reordering the steps or changing what makes one advance is a different act from
// fixing a sentence, and folding it into the copy edit would mean a typo fix could silently
// reorder a chapter. `chapter_key` and `step_key` are 0192's add-only ids: they identify the row
// and are never rewritten, because a rename orphans the bookmark of everyone who had got there.
type StepEdit struct {
	ChapterKey string `json:"chapter_key"`
	StepKey    string `json:"step_key"`
	Line       string `json:"line"`
	Route      string `json:"route"`
	Anchor     string `json:"anchor"`
}

// PolicyEdit is the PUT /api/v1/platform/tour/policy body: the demo knob, and only the demo knob.
//
// `dealer_enabled` IS DELIBERATELY NOT HERE. 0192 §1 gives the two knobs two owners, demo_mode is
// the platform operator's (IsSuperAdmin), dealer_enabled is the dealer's own (IsAdmin), and this
// route is gated on the first. Carrying both would mean a platform operator flipping the demo
// switch also restates a value they do not own, so a client holding a stale `dealer_enabled: true`
// would silently switch a tour back on that the dealer had turned off. The dealer's own switch
// wants the dealer's own route on their own settings surface; until it exists the column keeps its
// TRUE default and no route can quietly change it.
type PolicyEdit struct {
	BranchID string `json:"branch_id"`
	DemoMode bool   `json:"demo_mode"`
}

// ProgressEdit is the PUT /api/v1/tour/progress body.
//
// THERE IS NO user_id FIELD AND THERE MUST NEVER BE ONE. The row written is the authenticated
// caller's, resolved server-side from the request context. A user id in a body is a claim, and a
// claim is exactly what an authenticated request already made, accepting a second one would let
// anybody mark anybody else's walkthrough dismissed.
type ProgressEdit struct {
	ChapterKey string `json:"chapter_key"`
	StepKey    string `json:"step_key"`
	State      string `json:"state"`
}

// -- the grammars, lifted from 0192/0193 so a refusal happens in words ---------------------------

// MaxLine is the chrome budget from 0192 §3, counted in CHARACTERS to match Postgres char_length.
// A byte count would refuse a legal 72-character line containing an em dash and accept an illegal
// one that the CHECK then answers with a 500, the exact inversion this validator exists to
// prevent.
const MaxLine = 72

// maxKey matches 0192's chapter_key/step_key CHECK: 64 characters of a deliberately small grammar.
const maxKey = 64

var (
	keyRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	routeRe  = regexp.MustCompile(`^#[a-z-]+(/[a-z-]+)?$`)
	anchorRe = regexp.MustCompile(`^(testid|tag):[A-Za-z0-9._:-]{1,64}$`)
)

// States are 0193's three, and DISMISSED is the one the product must respect hardest.
const (
	StateOpen      = "OPEN"
	StateDone      = "DONE"
	StateDismissed = "DISMISSED"
)

// ValidateStepEdit refuses a malformed copy edit IN WORDS, with a 422, before any CHECK in 0192
// could answer with a 500.
//
// THE LINE LIMIT IS NAMED WITH ITS FIELD AND ITS NUMBER, and it says what the budget is FOR. "line
// must be ≤72" is a constraint; "a step says one line, at most 72 characters" is the doctrine the
// person just bumped into, and somebody writing demo copy needs the second one to write the next
// line correctly rather than by trial.
func ValidateStepEdit(in StepEdit) error {
	if !keyRe.MatchString(in.ChapterKey) {
		return Errf(http.StatusUnprocessableEntity,
			"tour: chapter_key names a chapter, lower case, digits, dot, dash or underscore, at most %d characters", maxKey)
	}
	if !keyRe.MatchString(in.StepKey) {
		return Errf(http.StatusUnprocessableEntity,
			"tour: step_key names a step, lower case, digits, dot, dash or underscore, at most %d characters", maxKey)
	}
	n := len([]rune(in.Line))
	if n == 0 {
		return Errf(http.StatusUnprocessableEntity, "tour: line is what the step says, a step with nothing to say is a step to delete")
	}
	if n > MaxLine {
		return Errf(http.StatusUnprocessableEntity,
			"tour: line is %d characters, a step says one line and the limit is %d, because a walkthrough speaks in headlines", n, MaxLine)
	}
	if in.Route != "" && !routeRe.MatchString(in.Route) {
		return Errf(http.StatusUnprocessableEntity,
			"tour: route is a screen this app has, spelled like #sales/pos, leave it empty to stay where the person already is")
	}
	if in.Anchor != "" && !anchorRe.MatchString(in.Anchor) {
		return Errf(http.StatusUnprocessableEntity,
			"tour: anchor points at a published test id or a registered element, testid:pos-browse or tag:hos-pos-terminal, and nothing else")
	}
	return nil
}

// ValidateProgressEdit refuses a bookmark nobody could resume.
//
// step_key IS SHAPE-CHECKED BUT NOT EXISTENCE-CHECKED, which is 0193's ruling and not an
// oversight: the column is deliberately not a foreign key, so that a retired step leaves a stale
// bookmark READABLE rather than cascading a person's history away. Checking the shape costs
// nothing and keeps junk out; checking the target would reintroduce the cascade through the back
// door.
func ValidateProgressEdit(in ProgressEdit) error {
	if !keyRe.MatchString(in.ChapterKey) {
		return Errf(http.StatusUnprocessableEntity,
			"tour: chapter_key names the chapter this bookmark belongs to")
	}
	if in.StepKey != "" && !keyRe.MatchString(in.StepKey) {
		return Errf(http.StatusUnprocessableEntity,
			"tour: step_key names the step they are on, leave it empty when the chapter is finished or closed")
	}
	switch in.State {
	case StateOpen, StateDone, StateDismissed:
	default:
		return Errf(http.StatusUnprocessableEntity,
			"tour: state is %s while they are partway through, %s when they ran it to the end, or %s when they closed it",
			StateOpen, StateDone, StateDismissed)
	}
	// A finished or closed chapter has no step to return to (0193's column comment). Accepting one
	// would store a bookmark that a resume would honour and a DONE chapter would then reopen at.
	if in.State != StateOpen && in.StepKey != "" {
		return Errf(http.StatusUnprocessableEntity,
			"tour: a %s chapter has no step to come back to, leave step_key empty", in.State)
	}
	return nil
}
