package assist

// answer_fields.go, the answer fields slice 5 adds (PANEL-API.md section 8.2): citations, notice,
// scope and answer_id. Every one is omitempty so a v1 panel keeps working.

import (
	"crypto/rand"
	"encoding/hex"
)

// Notice is the one structured notice of an answer. The core sends the code (and the class, for a
// refusal); the panel words it. The core sends no notice words of its own.
//
// Codes in this slice: "refused" (Class names the class the asker lacks and never a record) and
// "too_broad" (the question's plan was too large). "partial" and "stale" are refused by design: they
// would tell an asker that rows were hidden from them.
type Notice struct {
	Code  string `json:"code"`
	Class string `json:"class,omitempty"`
}

const (
	NoticeRefused  = "refused"
	NoticeTooBroad = "too_broad"
)

// Citation is one verified citation: N is the number the model wrote as [N], Label and Ref are built
// by the server from the exchange's own tool results.
type Citation struct {
	N     int    `json:"n"`
	Label string `json:"label"`
	Ref   Ref    `json:"ref"`
}

// ScopeBranch is one branch an answer's figures cover.
type ScopeBranch struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// ScopePeriod is the period a query filtered on, in the dealer's zone.
type ScopePeriod struct {
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	Label string `json:"label,omitempty"`
}

// Scope says plainly what an answer's figures cover, without counting what is withheld.
type Scope struct {
	Branches    []ScopeBranch `json:"branches,omitempty"`
	AllBranches bool          `json:"all_branches,omitempty"`
	Period      *ScopePeriod  `json:"period,omitempty"`
	AsOf        string        `json:"as_of,omitempty"`
}

// newAnswerID is opaque: 16 random bytes.
func newAnswerID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
