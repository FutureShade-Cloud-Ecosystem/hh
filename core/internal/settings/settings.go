// Package settings is the DIB-507 configurable-surfaces kit (D-105, the ratified hybrid -
// command/plans/configurable-surfaces-spec.md §4.3), piloted by the Quote Builder (DIB-506) and
// built to be adopted verbatim by the POS retrofit and every module after.
//
// THE SPLIT THE KIT STANDARDIZES:
//
//   - Behavior- and security-bearing knobs are TYPED policy columns per module (quote_policy,
//     pos_policy, …) following 0043's branch-NULL shape: the NULL row is the dealer default, a
//     branch row overrides, one admin-gated PUT flips it live. The kit contributes the shared
//     posture, the validators here (422-in-words BEFORE any CHECK could 500), and the
//     branch-NULL resolution contract every module policy read repeats.
//
//   - PRESENTATION CURATION, which panels/columns/chips/actions render, in what order, is
//     content-free strings + positions in the 0172 generic substrate (surface_role_items /
//     surface_user_items: the 0140/0149 dashboard idiom generalized by surface_id), plus the
//     per-user panel-ergonomics row (surface_user_workspace: pos_user_workspace generalized).
//     Whole-or-absent user overrides; reset IS a DELETE; write_key idempotent replay on the
//     workspace save (the 0166 contract).
//
// THE REGISTRY IS THE FRONT END'S, AND ONLY THE FRONT END'S (0149/0164 ruling, spec OQ-3): item
// ids are opaque strings a per-surface TypeScript module owns (app/src/quote-surface.ts for the
// pilot). This package validates SHAPE only, never lists. An unknown id renders nothing,
// silently; the per-surface registry test is the drift gate.
//
// ROLE SAFETY IS NOT THIS PACKAGE'S JOB. Cost/margin visibility is a module-policy knob read by
// the module's own marshal-layer gate (quote/builder.go's costAllowed, pos/rev8.go's
// marginAllowed). Curation hides buttons and columns; it never grants power and never moves data.
package settings

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// - the kit's error shape -

// StatusError carries an HTTP status with a human sentence, the kit's 422-in-words posture.
type StatusError struct {
	Status int
	Msg    string
}

func (e *StatusError) Error() string { return e.Msg }

// Errf builds a StatusError.
func Errf(status int, format string, a ...any) *StatusError {
	return &StatusError{Status: status, Msg: fmt.Sprintf(format, a...)}
}

// - item substrate vocabulary -

// The four item kinds of spec §3 (C1/C2/C3/C5). Tanya strips (C6) are module-policy toggles, not
// curation rows, and field sets (C4) are typed policy knobs, neither lives in this substrate.
const (
	KindPanel  = "panel"
	KindColumn = "column"
	KindChip   = "chip"
	KindAction = "action"
)

// Item is one curation row: a frontend-registry id at a position, shown or hidden.
type Item struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Position int    `json:"position"`
	Visible  bool   `json:"visible"`
}

// Profiles are 0140's five job shapes (spec OQ-2: reused as-is for the pilot) plus the manager
// refinements 0380 added (manager.<role>, one profile row per manager role, decision 5): the
// curation write gate must accept every profile the dashboard substrate can hold, or a manager's
// profile would be a profile an admin cannot curate.
var validProfiles = map[string]bool{
	"counter": true, "yard": true, "driver": true, "manager": true, "admin": true,
	"manager.branch": true, "manager.sales": true, "manager.yard": true, "manager.logistics": true,
	"manager.procurement": true, "manager.payables": true, "manager.controller": true,
	"manager.general": true,
}

// ProfileFallback is the profile a role with no dashboard_role_profiles row resolves to, the
// least-privileged useful shape, never a manager's view by accident (0140's ruling).
const ProfileFallback = "counter"

// idShape is the SHAPE of a registry id: the server owns no list (OQ-3), only the alphabet.
var idShape = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// maxItemsPerPut bounds one curation write; no real surface catalog approaches it.
const maxItemsPerPut = 128

// ValidateSurfaceID refuses a malformed surface id in words (shape only, never a list).
func ValidateSurfaceID(id string) error {
	if !idShape.MatchString(id) {
		return Errf(http.StatusUnprocessableEntity,
			"settings: surface id must be 1-64 lowercase letters, digits, dots, dashes or underscores")
	}
	return nil
}

// ValidateItems refuses a malformed item set in words. It validates SHAPE (kind enum, id alphabet,
// position >= 0, set size) and duplicate (kind,id) pairs; item ids themselves belong to the
// frontend registry and are deliberately not checked against any server-side list.
func ValidateItems(items []Item) error {
	if len(items) == 0 {
		return Errf(http.StatusUnprocessableEntity, "settings: the item set is empty, to reset, use DELETE")
	}
	if len(items) > maxItemsPerPut {
		return Errf(http.StatusUnprocessableEntity,
			"settings: at most %d items per write", maxItemsPerPut)
	}
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		switch it.Kind {
		case KindPanel, KindColumn, KindChip, KindAction:
		default:
			return Errf(http.StatusUnprocessableEntity,
				"settings: item kind must be panel, column, chip or action")
		}
		if !idShape.MatchString(it.ID) {
			return Errf(http.StatusUnprocessableEntity,
				"settings: item id %q must be 1-64 lowercase letters, digits, dots, dashes or underscores", it.ID)
		}
		if it.Position < 0 {
			return Errf(http.StatusUnprocessableEntity, "settings: item positions cannot be negative")
		}
		key := it.Kind + "\x00" + it.ID
		if seen[key] {
			return Errf(http.StatusUnprocessableEntity, "settings: item %s/%s appears twice", it.Kind, it.ID)
		}
		seen[key] = true
	}
	return nil
}

// ValidateProfile refuses an unknown job shape in words.
func ValidateProfile(p string) error {
	if !validProfiles[p] {
		return Errf(http.StatusUnprocessableEntity,
			"settings: profile must be counter, yard, driver, manager or admin")
	}
	return nil
}

// - shared policy-knob validators (the typed half's common vocabulary) -

// The three-value cost/margin visibility switch (0170's vocabulary, spec §5.2). Shared so the
// quote pilot and the POS retrofit cannot drift on the words.
const (
	VisibilityManagerUp = "MANAGER_UP"
	VisibilityAllStaff  = "ALL_STAFF"
	VisibilityHidden    = "HIDDEN"
)

// ValidateVisibility normalizes/refuses a cost-visibility class. Omitted ("") normalizes to the
// column default (MANAGER_UP); unknown values are a 422 in words before the CHECK could 500.
func ValidateVisibility(module, v string) (string, error) {
	switch v {
	case "":
		return VisibilityManagerUp, nil
	case VisibilityManagerUp, VisibilityAllStaff, VisibilityHidden:
		return v, nil
	default:
		return "", Errf(http.StatusUnprocessableEntity,
			"%s: cost_visibility must be MANAGER_UP, ALL_STAFF or HIDDEN", module)
	}
}

// Workspace geometry bounds, the 0166 constants, kit-owned so every surface agrees.
const (
	SplitMin     = 20
	SplitMax     = 80
	SplitDefault = 58

	ArrangementPrimaryLeft  = "PRIMARY_LEFT"
	ArrangementPrimaryRight = "PRIMARY_RIGHT"
)

// ValidateGeometry normalizes/refuses the shared split+arrangement pair (the 0166 posture:
// omitted split (0) → the surface default; omitted arrangement → PRIMARY_LEFT; out-of-range or
// unknown values are a 422 with words, never a CHECK-constraint 500).
func ValidateGeometry(module string, splitPct int, arrangement string, defaultSplit int) (int, string, error) {
	if splitPct == 0 {
		splitPct = defaultSplit
	}
	if splitPct < SplitMin || splitPct > SplitMax {
		return 0, "", Errf(http.StatusUnprocessableEntity,
			"%s: workspace split_pct must be between %d and %d", module, SplitMin, SplitMax)
	}
	switch arrangement {
	case "":
		arrangement = ArrangementPrimaryLeft
	case ArrangementPrimaryLeft, ArrangementPrimaryRight:
	default:
		return 0, "", Errf(http.StatusUnprocessableEntity,
			"%s: workspace arrangement must be PRIMARY_LEFT or PRIMARY_RIGHT", module)
	}
	return splitPct, arrangement, nil
}

// openRefShape bounds the surface-neutral "open object" reference (0174): an opaque id the
// surface frontend interprets, printable, whitespace-free, at most 64 chars. ” = nothing open.
var openRefShape = regexp.MustCompile(`^[!-~]{0,64}$`)

// ValidateOpenRef refuses a malformed open_ref in words BEFORE the CHECK could 500.
func ValidateOpenRef(module, ref string) error {
	if !openRefShape.MatchString(ref) {
		return Errf(http.StatusUnprocessableEntity,
			"%s: open_ref must be at most 64 printable characters with no spaces", module)
	}
	return nil
}

// ValidateIntRange refuses an out-of-range integer knob in words, naming the knob and its bounds.
func ValidateIntRange(module, knob string, v, lo, hi int) error {
	if v < lo || v > hi {
		return Errf(http.StatusUnprocessableEntity,
			"%s: %s must be between %d and %d", module, knob, lo, hi)
	}
	return nil
}

// ValidateChipSet normalizes/refuses a curated chip category set: at most 8 entries, each a
// non-empty comma-free string of at most 40 chars; nil/empty = "not curated" (the module derives
// an honest data-driven default at read time, the 0170 posture).
func ValidateChipSet(module string, set []string) ([]string, error) {
	if len(set) == 0 {
		return nil, nil
	}
	if len(set) > 8 {
		return nil, Errf(http.StatusUnprocessableEntity, "%s: quick_chip_set holds at most 8 categories", module)
	}
	out := make([]string, 0, len(set))
	for _, c := range set {
		t := strings.TrimSpace(c)
		if t == "" || len([]rune(t)) > 40 || strings.Contains(t, ",") {
			return nil, Errf(http.StatusUnprocessableEntity,
				"%s: each quick_chip_set category must be 1-40 characters with no comma", module)
		}
		out = append(out, t)
	}
	return out, nil
}
