package compliance

// model.go, the compliance DTOs beyond the frozen Deadline (in module.go): the shared review-status banner
// and the module constants. M6 has ZERO client validation, so every value here is a DOCUMENTED PLACEHOLDER
// (D-021); the DIB-42 Tanya demo is the checkpoint that flips DRAFT → APPROVED.

// defaultRuleDays is the Bill 60 statutory release window (45 days from substantial performance).
const defaultRuleDays = 45

// defaultBanner is the DATA-DRIVEN default DRAFT banner returned when a subject has no explicit review_status
// row, so the shared endpoint always answers a banner.
//
// IT USED TO END "(DIB-42)", and on every database in existence this constant IS the banner: review_status
// holds zero rows, so every banner a person has ever seen came from here rather than from a row. See the
// same removal and the same argument at lien/template.go, a statutory notice does not carry our backlog.
//
// 0038:103 still declares the OLD text as the column DEFAULT. It is now deliberately out of step with this
// constant and cannot be brought back into step without a migration, which item 3 has no reservation for;
// it is recorded in NOTES-SESSION-2026-08-31B.md as needing one. Nothing reaches it today, the one INSERT
// (compliance/repository.go:130) always supplies banner_text explicitly, so no row can pick it up without
// a new write path being added, and TestBanner_CarriesNoWorkstreamCode fails the moment one does.
const defaultBanner = "DRAFT, statute-derived, pending client validation"

// review_status states + subject types (mirror the 0038 CHECK sets).
const (
	stateDraft    = "DRAFT"
	statePending  = "PENDING"
	stateApproved = "APPROVED"
	stateRejected = "REJECTED"
)

// ReviewStatus is one shared banner row, the data-driven DRAFT/PENDING/APPROVED state the M6 UI reads for a
// subject (a job or a document).
type ReviewStatus struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	State       string `json:"state"`
	BannerText  string `json:"banner_text"`
	IsDraft     bool   `json:"is_draft"`
	ReviewedBy  string `json:"reviewed_by,omitempty"`
	ReviewedAt  string `json:"reviewed_at,omitempty"`
}

// ReviewStatusInput is the upsert payload (leadership flips the banner at the DIB-42 checkpoint).
type ReviewStatusInput struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	State       string `json:"state"`
	BannerText  string `json:"banner_text"`
}

func validState(s string) bool {
	switch s {
	case stateDraft, statePending, stateApproved, stateRejected:
		return true
	}
	return false
}
