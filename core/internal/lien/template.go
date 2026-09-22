package lien

import (
	"fmt"
	"sort"
	"strings"
)

// template.go, the DATA-DRIVEN Ontario Construction Act document templates. M6 has ZERO client-validated
// requirements (Grant's lien discovery stale, Amanda silent), so the forms are statute-derived skeletons
// rendered from merge-field DATA, not hardcoded prose, and every rendered doc carries a DRAFT banner until a
// client sign-off (DIB-42) flips it. Adding/adjusting a form is a table of merge lines here, never a redesign.

// draftBanner is the default DRAFT banner text stamped at the head of every rendered document. It is a
// documented placeholder; the authoritative per-subject banner is served data-driven by the shared
// review-status endpoint (internal/compliance), so a banner change is a row edit, not a redeploy.
//
// IT USED TO END "(DIB-42)". A lien document is a STATUTORY NOTICE that goes to an owner, a contractor
// or their lawyer, and this banner is stamped at the head of every one of them, so our ticket number
// was riding out on the most formal thing this product produces. Item 3b's browser walkthrough found
// it; no source scan could have, because the client gates read app/src and this sentence is composed
// in Go. The words a reader needs are unchanged: the document is a draft, it was derived from the
// statute rather than confirmed against the client's own forms, and it is waiting on that
// confirmation. Which of OUR tickets tracks the confirmation is not one of them.
const draftBanner = "*** DRAFT, statute-derived, pending client validation ***"

// templateSpec is one document form: its human title + the ordered merge-field lines. A line is a label and
// the data key it pulls from `Data`; a missing key renders "____" (a blank to be filled), so a template never
// silently omits a statutory field.
type templateSpec struct {
	Title string
	Lines []templateLine
}

type templateLine struct {
	Label string
	Key   string
}

// templates is the FROZEN-INTENT, DATA-DRIVEN registry of the Construction-Act forms M6 renders. The set
// mirrors the 0038 template_kind CHECK.
var templates = map[string]templateSpec{
	"CONDITIONAL_RELEASE": {
		Title: "Conditional Release of Preserved Lien (Payment)",
		Lines: []templateLine{
			{"Job / Improvement", "job_no"},
			{"Owner", "owner"},
			{"Contractor / Claimant", "claimant"},
			{"Amount of this payment", "payment_amount"},
			{"Holdback retained", "holdback_amount"},
			{"Condition", "condition"},
		},
	},
	"UNCONDITIONAL_RELEASE": {
		Title: "Unconditional Release of Preserved Lien",
		Lines: []templateLine{
			{"Job / Improvement", "job_no"},
			{"Owner", "owner"},
			{"Contractor / Claimant", "claimant"},
			{"Amount received in full", "payment_amount"},
		},
	},
	"STATUTORY_DECLARATION": {
		Title: "Statutory Declaration (Construction Act)",
		Lines: []templateLine{
			{"Job / Improvement", "job_no"},
			{"Declarant", "declarant"},
			{"Capacity", "capacity"},
			{"All accounts for labour/services/materials paid", "accounts_paid"},
			{"Exceptions", "exceptions"},
		},
	},
	"DISCHARGE": {
		Title: "Discharge of Preserved Lien",
		Lines: []templateLine{
			{"Job / Improvement", "job_no"},
			{"Lien claim number", "lien_claim_no"},
			{"Claimant", "claimant"},
			{"Discharged amount", "discharged_amount"},
		},
	},
	"NOTICE": {
		Title: "Notice (Construction Act)",
		Lines: []templateLine{
			{"Job / Improvement", "job_no"},
			{"To", "recipient"},
			{"Subject", "subject"},
			{"Body", "body"},
		},
	},
	"OTHER": {
		Title: "Construction Act Document",
		Lines: []templateLine{
			{"Job / Improvement", "job_no"},
			{"Body", "body"},
		},
	},
}

// knownTemplate reports whether a template_kind is a renderable form.
func knownTemplate(kind string) bool {
	_, ok := templates[kind]
	return ok
}

// renderTemplate renders a template_kind from its merge-field data into a versioned text body, headed by the
// DRAFT banner. Deterministic (stable line order) so version diffs are meaningful and tests are exact.
func renderTemplate(kind string, version int, data map[string]any) (string, error) {
	spec, ok := templates[kind]
	if !ok {
		return "", unprocessable("lien: unknown template_kind %q", kind)
	}
	var b strings.Builder
	b.WriteString(draftBanner)
	b.WriteString("\n\n")
	b.WriteString(spec.Title)
	b.WriteString(fmt.Sprintf("  (v%d)\n", version))
	b.WriteString(strings.Repeat("-", len(spec.Title)+8))
	b.WriteString("\n")
	for _, ln := range spec.Lines {
		b.WriteString(ln.Label)
		b.WriteString(": ")
		b.WriteString(mergeValue(data, ln.Key))
		b.WriteString("\n")
	}
	// Any extra data keys the caller supplied that the template did not consume are appended (deterministic),
	// so a form never drops data the operator entered.
	extra := extraKeys(spec, data)
	if len(extra) > 0 {
		b.WriteString("\nAdditional:\n")
		for _, k := range extra {
			b.WriteString(fmt.Sprintf("  %s: %s\n", k, mergeValue(data, k)))
		}
	}
	return b.String(), nil
}

// mergeValue renders a merge key; a missing/empty value renders "____" (a blank statutory field).
func mergeValue(data map[string]any, key string) string {
	v, ok := data[key]
	if !ok || v == nil {
		return "____"
	}
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	if s == "" {
		return "____"
	}
	return s
}

func extraKeys(spec templateSpec, data map[string]any) []string {
	consumed := make(map[string]bool, len(spec.Lines))
	for _, ln := range spec.Lines {
		consumed[ln.Key] = true
	}
	var out []string
	for k := range data {
		if !consumed[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
