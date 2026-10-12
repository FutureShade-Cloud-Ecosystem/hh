package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// The engine's fourth guarantee (plan B.3, guarantee 2): the same
// idempotency key with a different body is refused. Dedup finds a key by
// the idempotency column each owning module stamps on its own row, and
// those rows hold the key and nothing of the body, so a replay under the
// same key but a different body would answer "applied" and the client
// would believe its changed operation had landed. The ledger closes that:
// the first APPLICATION of a key stores a hash of the operation's body
// beside the key, and a later operation under that key must carry the
// same hash.
//
// Why a ledger of its own and not a column on each module's row: some
// twenty five Stores answer FindApplied over their own tables, and a key
// can name a row several operations have since moved on. The body belongs
// to the operation, the key is the operation's identity, and the engine
// owns the protocol, so the engine owns this one table (sync_op_hashes)
// behind the KeyLedger seam and no module table changes.
//
// What a hash binds, and what it does not:
//   - Only an APPLIED key is bound (create, update, delete that wrote, and
//     a last write wins merge that wrote). A rejected operation, a
//     conflict that applied nothing and an already tombstoned delete stamp
//     no key on any row, so FindApplied does not see them and the client
//     may retry such a key with a corrected body, as the module that
//     refused it intended.
//   - A key applied before the ledger existed has no stored hash. It is
//     treated as a plain replay (applied with the row's current seq, as
//     before the ledger) whatever body arrives, and the replay stores
//     nothing: the engine cannot know that the body in hand is the one
//     that first applied. This is the migration's documented seam, and it
//     closes by itself as old keys age out of every client outbox.
//   - A ledger write that fails after the module applied is an operational
//     fault: the push fails whole, and the client retries it whole, where
//     the applied key now dedups as a legacy key (no hash). The guarantee
//     is therefore weakest for exactly one key per ledger fault, never
//     silently wrong for a key that was stored.

// ErrKeyReused is the refusal for a key reused with a different body. The
// reason word leads the message so a client can branch on it: it is the
// same word the offline store's outbox uses for the same fault found
// locally (web/packages/offline-store/src/outbox.ts, reason "key_reused").
var ErrKeyReused = errors.New("key_reused: this idempotency key already names a different operation")

// auditKeyReused is the audit action for a refused reuse, distinct from
// SYNC_PUSH so an operator can filter on action alone for the one event
// that is either a client defect or a probe.
const auditKeyReused = "SYNC_KEY_REUSED"

// KeyLedger is the seam holding, per applied idempotency key, the hash of
// the body that first applied under it. Both halves run under the caller's
// ScopeFilter: a key is only ever compared with keys of the same org, the
// same tenant (a tenant caller) and the same scope, so one dealer's keys
// never refuse another's and existence never leaks across them.
type KeyLedger interface {
	// BodyHash returns the hash stored when key first applied in scope
	// under f, and whether one is stored.
	BodyHash(ctx context.Context, f ScopeFilter, scope, key string) (string, bool, error)
	// Remember stores the hash for a key that has just applied. The first
	// write wins: a second Remember for the same slot changes nothing.
	Remember(ctx context.Context, f ScopeFilter, scope, key, hash string) error
}

// WithKeyLedger wires the ledger seam. nil or unwired means no body check
// (as an unwired audit means no audit rows): the engine keeps its first
// three guarantees and the composition root decides whether the fourth is
// on.
func (e *Engine) WithKeyLedger(l KeyLedger) *Engine {
	e.ledger = l
	return e
}

// canonicalOp is the exact byte string an operation's hash covers:
// canonical JSON (object keys sorted at every depth, no whitespace, no
// HTML escaping) of
//
//	{"baseSeq": <int|null>, "entity": <string|null>, "op": <string>,
//	 "payload": <object>, "scope": <string>}
//
// The same five facts, with the same names, that the offline store's
// outbox hashes (canonical.ts and hashInput in outbox.ts), so the two sides
// agree on what "the same operation" means. The idempotency key is not in
// the body (it is the identity the body is compared under). The server
// computes its own hash from what it received; the client's hash is never
// sent and never trusted, so nothing on the wire changes. Byte identity
// with the client's string is NOT relied on (JSON number text and
// unpaired surrogates can differ between the two runtimes): only this
// server's consistency with itself matters, one operation arriving twice
// through the same decoder.
func canonicalOp(scope string, op apigen.SyncPushOperation) (string, error) {
	doc := map[string]any{
		"scope":   scope,
		"op":      op.Op,
		"entity":  nil,
		"payload": map[string]any(op.Payload),
		"baseSeq": nil,
	}
	if op.Payload == nil {
		doc["payload"] = map[string]any{}
	}
	if op.Entity != nil {
		doc["entity"] = *op.Entity
	}
	if op.BaseSeq != nil {
		doc["baseSeq"] = *op.BaseSeq
	}
	var b strings.Builder
	if err := writeCanonical(&b, doc); err != nil {
		return "", err
	}
	return b.String(), nil
}

// opHash is the lowercase hex SHA-256 of canonicalOp.
func opHash(scope string, op apigen.SyncPushOperation) (string, error) {
	c, err := canonicalOp(scope, op)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(c))
	return hex.EncodeToString(sum[:]), nil
}

// jsStringFixups put back the two characters Go's encoder always escapes
// and JavaScript's JSON.stringify does not, so the common text matches the
// client's canonical string byte for byte.
var jsStringFixups = strings.NewReplacer(` `, " ", ` `, " ")

func writeCanonical(b *strings.Builder, v any) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, k); err != nil {
				return err
			}
			b.WriteByte(':')
			if err := writeCanonical(b, x[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	default:
		// Strings, numbers and booleans: the encoder's own text (numbers
		// in the shortest form, as JavaScript writes them) with HTML
		// escaping off. A value JSON cannot carry exactly (NaN, an
		// infinity, a channel) is an error, never a guess.
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(x); err != nil {
			return fmt.Errorf("sync: canonical: %w", err)
		}
		b.WriteString(jsStringFixups.Replace(strings.TrimSuffix(buf.String(), "\n")))
	}
	return nil
}
