// Package customerquote is the customer-quote send and acceptance lifecycle
// (HH-3.6, the behaviour half of contract gap G1): the contractor's proposal
// to the homeowner, built, updated, sent and revised on the tenant leg, and
// viewed and accepted on the public leg where the opaque share token is the
// whole authority (D23: the homeowner is carried by the magic-link token,
// never by an account). The token rotation and the signed acceptance
// artifact implement ADR-0022 (docs/adr/0022-acceptance-artifact-signature.md);
// the carried HH Pro rules R1 to R11 and their citations live with the code
// that carries them. The module owns customer_quote and quote_acceptance and
// touches no other table; the audit row rides the seam defined in audit.go.
package customerquote

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

// The share token primitive, carried from the dealer pair (ERP
// backend/internal/quote/share.go, the token section): 32 bytes of crypto/rand,
// base64url, exactly the alphabet and length this file's shape check pins. A
// share token is a credential, not an authentication: whoever holds it is
// treated as the customer, it grants read and accept of exactly one quote and
// nothing else, and only the sha256 hex is ever stored (the migration's Block 8
// intent, raw value only in the send response).
const (
	// tokenRawLen is the base64url length of 32 bytes (unpadded).
	tokenRawLen = 43
	// hashHexLen is the length of a sha256 hex atom.
	hashHexLen = 64
)

// mintedShape is the mint's own alphabet and length; a presented token that
// fails it never existed as a minted token and answers 404 with the
// never-existed body (ADR-0022 decision 1, resolution step 1).
var mintedShape = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// hexAtom is one storage atom: 64 lowercase hex characters.
var hexAtom = regexp.MustCompile(`^[0-9a-f]{64}$`)

// MintToken returns a fresh raw token and its stored hash. The raw value is
// returned exactly once, to the send operation's caller; it is unrecoverable
// afterwards.
func MintToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("customerquote: mint token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashToken(raw), nil
}

// HashToken is the stored form of a raw token: sha256 hex.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// TokenWellFormed reports whether a presented raw token has the mint's shape.
func TokenWellFormed(raw string) bool {
	return mintedShape.MatchString(strings.TrimSpace(raw))
}

// TokenState is the parsed content of the share_token_hash column: the live
// hash plus the append-only dead list (ADR-0022 decision 1). The zero value is
// the empty state: no live token, nothing retired.
type TokenState struct {
	// Live is the hash of the current raw token, or "" when the live slot is
	// empty (between a revise and the next send).
	Live string
	// Dead holds the hashes of every retired raw token, in retirement order,
	// deduplicated.
	Dead []string
}

// ErrUnparsableStoredToken names a stored value that fails the v1 grammar: it
// matches no token, is counted loudly, and never changes a response by itself
// (ADR-0022 decision 1, the parse-failure rule).
var ErrUnparsableStoredToken = errors.New("customerquote: stored share token value fails the v1 grammar")

// liveEmpty is the sentinel for the empty live slot.
const liveEmpty = "-"

// deadEmpty is the sentinel for an empty dead list.
const deadEmpty = "-"

// Encode renders the state into the column's structured value:
// v1:<live>:<dead>[,<dead>...]. Only hashes are ever at rest.
func (ts TokenState) Encode() string {
	live := liveEmpty
	if ts.Live != "" {
		live = ts.Live
	}
	dead := deadEmpty
	if len(ts.Dead) > 0 {
		dead = strings.Join(ts.Dead, ",")
	}
	return "v1:" + live + ":" + dead
}

// ParseTokenState parses a stored value under the pinned grammar. Every
// deviation is a parse failure: a missing v1 marker, wrong shape, non-hex or
// short atoms, empty list elements, or a duplicated dead entry (duplicates are
// impossible through the write path, which dedupes, so a duplicate means the
// row was corrupted by hand).
func ParseTokenState(stored string) (TokenState, error) {
	rest, ok := strings.CutPrefix(stored, "v1:")
	if !ok {
		return TokenState{}, fmt.Errorf("%w: missing v1 marker", ErrUnparsableStoredToken)
	}
	livePart, deadPart, ok := strings.Cut(rest, ":")
	if !ok {
		return TokenState{}, fmt.Errorf("%w: missing dead separator", ErrUnparsableStoredToken)
	}
	ts := TokenState{}
	switch livePart {
	case liveEmpty:
		// empty live slot
	default:
		if !hexAtom.MatchString(livePart) {
			return TokenState{}, fmt.Errorf("%w: live atom is not 64 lowercase hex", ErrUnparsableStoredToken)
		}
		ts.Live = livePart
	}
	if deadPart != deadEmpty {
		seen := make(map[string]bool)
		for _, atom := range strings.Split(deadPart, ",") {
			if !hexAtom.MatchString(atom) {
				return TokenState{}, fmt.Errorf("%w: dead atom is not 64 lowercase hex", ErrUnparsableStoredToken)
			}
			if seen[atom] {
				return TokenState{}, fmt.Errorf("%w: duplicated dead atom", ErrUnparsableStoredToken)
			}
			seen[atom] = true
			ts.Dead = append(ts.Dead, atom)
		}
	}
	return ts, nil
}

// RetireLive moves the live hash to the end of the dead list (deduplicated)
// and empties the live slot: the revise rotation. The list keeps every earlier
// retirement; a single-tombstone variant here is the exact mutation M2c in the
// test plan exists to kill.
func (ts TokenState) RetireLive() TokenState {
	out := TokenState{Live: "", Dead: append([]string(nil), ts.Dead...)}
	if ts.Live == "" {
		return out
	}
	for _, d := range out.Dead {
		if d == ts.Live {
			return out
		}
	}
	out.Dead = append(out.Dead, ts.Live)
	return out
}

// InstallLive retires whatever is live and makes the minted hash live: the
// send rotation.
func (ts TokenState) InstallLive(hash string) TokenState {
	out := ts.RetireLive()
	out.Live = hash
	return out
}

// ContainsDead reports exact element membership of a hash in the dead list.
// Membership is by element, never by substring: every atom is exactly 64 hex
// characters, so equality of the whole atom is the only match (the M2d
// boundary companion).
func (ts TokenState) ContainsDead(hash string) bool {
	for _, d := range ts.Dead {
		if d == hash {
			return true
		}
	}
	return false
}

// IsLive reports whether the state's live slot holds exactly this hash.
func (ts TokenState) IsLive(hash string) bool {
	return ts.Live != "" && ts.Live == hash
}

// Matches reports whether a presented hash is live or dead on this state.
func (ts TokenState) Matches(hash string) (live, dead bool) {
	return ts.IsLive(hash), ts.ContainsDead(hash)
}

// DefectCounter counts loud data defects: unparsable stored values, NULL link
// bounds on live hashes, and multi-match resolutions. It exists so the defects
// are visible to monitoring; it never changes a response alone (ADR-0022
// decision 1).
type DefectCounter struct {
	unparsable    atomic.Int64
	nullBound     atomic.Int64
	multiMatch    atomic.Int64
	tombstoneGone atomic.Int64
}

// UnparsableStoredValue records one unparsable stored value.
func (d *DefectCounter) UnparsableStoredValue() { d.unparsable.Add(1) }

// NullLinkBound records one live hash with no link bound.
func (d *DefectCounter) NullLinkBound() { d.nullBound.Add(1) }

// MultiMatch records one presented hash matched by more than one row.
func (d *DefectCounter) MultiMatch() { d.multiMatch.Add(1) }

// TombstoneGone records one token answered 410 because the quote row is
// tombstoned.
func (d *DefectCounter) TombstoneGone() { d.tombstoneGone.Add(1) }

// Counts returns the loud-defect counts by class, for the run report.
func (d *DefectCounter) Counts() (unparsable, nullBound, multiMatch, tombstoneGone int64) {
	return d.unparsable.Load(), d.nullBound.Load(), d.multiMatch.Load(), d.tombstoneGone.Load()
}
