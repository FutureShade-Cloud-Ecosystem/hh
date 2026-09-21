package customerquote

// The audit seam (ADR-0022 decision 2). The audit module is not in this
// lane's base: core/internal carries identity, platform and testkit only at
// the branch point, and the audit module arrives with Lane A's ERP carry (the
// review's F-7). This file defines the narrow seam this module consumes, and
// nothing more: the concrete wiring is an integration-time re-point that
// satisfies the interface with the audit module's transactional recorder. The
// seam is defined once, here, so no later batch builds it a second time.

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// TxExec is the least the seam needs from the acceptance's own transaction:
// the write surface pgx.Tx already satisfies structurally, the same shape the
// carried audit module's Execer pins (ERP backend/internal/audit/repository.go).
type TxExec interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// The pinned audit row vocabulary (ADR-0022 decision 2, the linkage key and
// the Extra key set). The linkage is deliberately NOT the dealer's: the dealer
// acceptance anchors to its audit row through an audit_seq column the frozen
// quote_acceptance table lacks, and its audit row keys on the quote rather
// than the acceptance. Here the audit row for an acceptance is found by the
// action constant plus entity type quote_acceptance plus the entity id equal
// to the quote_acceptance row id; zero rows (including a deleted audit row)
// or multiple rows fails verification closed.
const (
	// AuditAction is the pinned action constant of the acceptance row.
	AuditAction = "CUSTOMER_QUOTE_ACCEPTED"
	// AuditEntityType is the pinned entity type of the acceptance row.
	AuditEntityType = "quote_acceptance"
	// AuditPurpose is the pinned purpose field of the acceptance row.
	AuditPurpose = "CUSTOMER_QUOTE_ACCEPTANCE"
)

// The Extra keys the acceptance row carries. The first six join the audit
// module's deny-by-default allowlist openly at the integration re-point; the
// integer amount passes the chokepoint unchanged. The hex-valued keys
// (artifact_hash, artifact_sig, token_hash) ride separator-encoded, see
// separatorEncodeHex below.
const (
	ExtraArtifactHash = "artifact_hash"
	ExtraArtifactSig  = "artifact_sig"
	ExtraAlg          = "alg"
	ExtraKeyID        = "key_id"
	ExtraRefType      = "ref_type"
	ExtraTokenHash    = "token_hash"
	ExtraAmountCents  = "amount_cents"

	// RefTypeShareToken names the credential that carried the acceptance:
	// the customer-quote share token, never a person.
	RefTypeShareToken = "customer_quote_share_token"
)

// AcceptanceAuditRow is the one audit row this module writes and reads. The
// actorship rule is the dealer pair's carried verbatim: an acceptance has no
// authenticated principal, and attributing it to whoever minted the link
// would misattribute the customer's decision, so the row names the token
// (ref_type plus token_hash) and no staff, tenant or platform user id.
//
// ArtifactHash, ArtifactSig and TokenHash ride separator-encoded (groups of
// six hex characters joined by colons) so no stored value can contain a run
// of seven or more consecutive digits; that run is exactly what the carried
// audit redaction chokepoint redacts on (ERP
// backend/internal/audit/service.go, the cleartext detector, threshold
// seven), and roughly half of random 64-character hex signatures trip it.
// The encoding is part of the seam contract; decoding lives in this module.
type AcceptanceAuditRow struct {
	AcceptanceID       string
	QuoteID            string
	OrgID              string
	TenantID           string
	AcceptedTotalCents int64
	ArtifactHash       string // separator-encoded hex
	ArtifactSig        string // separator-encoded hex
	Alg                string
	KeyID              string
	TokenHash          string // separator-encoded hex
}

// AuditSeam is the narrow interface this module consumes. Implementations
// must hold both properties the ADR pins:
//
//   - RecordAcceptanceTx writes the row ON the caller's acceptance
//     transaction, so it commits or rolls back atomically with the status
//     flip and the acceptance row. The audit write is mandatory, never
//     degradable: the dealer precedent degrades when its seam is nil, and
//     this surface deliberately diverges, because an acceptance without its
//     audit row would be an unsigned acceptance. A non-nil error rolls the
//     whole acceptance back.
//   - LoadAcceptance returns the row for one acceptance id under the pinned
//     linkage key. Zero rows or multiple rows reports ambiguity (ok false
//     with no error) and fails verification closed; the concrete shape of
//     that failure is the implementation's, the closedness is the contract.
type AuditSeam interface {
	RecordAcceptanceTx(ctx context.Context, tx TxExec, row AcceptanceAuditRow) error
	LoadAcceptance(ctx context.Context, acceptanceID string) (AcceptanceAuditRow, bool, error)
}

// separatorGroupLen is the hex group length of the separator encoding: six
// characters, so the longest possible digit run inside one group is six, one
// below the chokepoint's threshold of seven.
const separatorGroupLen = 6

// separatorEncodeHex encodes a lowercase hex string so that no run of seven
// or more consecutive digits can occur in the encoded value: groups of six
// hex characters joined by colons. The colon is not a hex character and not
// a digit, so every digit run restarts at each group boundary.
func separatorEncodeHex(h string) string {
	if h == "" {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(h); i += separatorGroupLen {
		end := i + separatorGroupLen
		if end > len(h) {
			end = len(h)
		}
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(h[i:end])
	}
	return b.String()
}

// separatorDecodeHex inverts separatorEncodeHex, validating the shape so a
// corrupted audit row fails closed rather than decoding to a wrong value:
// groups of one to six lowercase hex characters joined by single colons,
// every group except the last exactly six (the encoder splits at fixed
// boundaries and only the final group of a 64-hex atom can be short), and
// the decoded whole exactly the length of a sha256 hex atom, the only value
// class that ever rides encoded.
func separatorDecodeHex(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	groups := strings.Split(s, ":")
	var out strings.Builder
	for i, g := range groups {
		if g == "" || len(g) > separatorGroupLen {
			return "", fmt.Errorf("customerquote: value %q is not separator-encoded hex", s)
		}
		for j := 0; j < len(g); j++ {
			c := g[j]
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return "", fmt.Errorf("customerquote: value %q is not separator-encoded hex", s)
			}
		}
		if i < len(groups)-1 && len(g) != separatorGroupLen {
			return "", fmt.Errorf("customerquote: value %q has a short non-final group", s)
		}
		out.WriteString(g)
	}
	if out.Len() != hashHexLen {
		return "", fmt.Errorf("customerquote: value %q decodes to %d hex characters, want %d", s, out.Len(), hashHexLen)
	}
	return out.String(), nil
}

// EncodeAuditRow encodes a row's hex values for the ride over the seam.
func EncodeAuditRow(row AcceptanceAuditRow) AcceptanceAuditRow {
	row.ArtifactHash = separatorEncodeHex(row.ArtifactHash)
	row.ArtifactSig = separatorEncodeHex(row.ArtifactSig)
	row.TokenHash = separatorEncodeHex(row.TokenHash)
	return row
}

// DecodeAuditRow decodes a row loaded over the seam.
func DecodeAuditRow(row AcceptanceAuditRow) (AcceptanceAuditRow, error) {
	var err error
	if row.ArtifactHash, err = separatorDecodeHex(row.ArtifactHash); err != nil {
		return AcceptanceAuditRow{}, err
	}
	if row.ArtifactSig, err = separatorDecodeHex(row.ArtifactSig); err != nil {
		return AcceptanceAuditRow{}, err
	}
	if row.TokenHash, err = separatorDecodeHex(row.TokenHash); err != nil {
		return AcceptanceAuditRow{}, err
	}
	return row, nil
}

// HasSevenDigitRun reports whether a string carries a run of seven or more
// consecutive digits, the carried chokepoint's trigger. It exists for the
// encoding's proof tests and for the loader's key id validation.
func HasSevenDigitRun(s string) bool {
	run := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			run++
			if run >= 7 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}
