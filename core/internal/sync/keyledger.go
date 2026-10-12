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
//     no key on any row, so the client may retry such a key with a
//     corrected body, as the module that refused it intended. The ledger
//     is written after the module applied, never before.
//   - The ledger is consulted for EVERY operation that carries a key, not
//     only when the module's dedup finds it. Most Stores answer FindApplied
//     from the row's single D30 column, which holds the LATEST key, so once
//     a row has moved on an older key no longer dedups in the module while
//     the ledger still holds it. Stored and different: key_reused, whatever
//     FindApplied says. Stored and equal while FindApplied misses: the key
//     already applied and must not apply again; it answers applied from the
//     row's current state, found by the entity id stored beside the hash
//     (the server assigned id of a create is known to no one else).
//   - A key applied before the ledger existed has no stored hash, and the
//     ledger cannot bind it: the engine cannot know that the body in hand
//     is the one that first applied, so it stores nothing for it. While the
//     owning module still dedups that key (the row still holds it) it is a
//     plain replay, applied with the row's current seq whatever body
//     arrives, as before the ledger. Once the row has moved on the module no
//     longer finds the key and it applies again, which is the behaviour
//     before the ledger, unchanged. This is the migration's documented seam;
//     it closes by itself as old keys age out of every client outbox.
//   - A key is one key whatever its letter case: the engine canonicalizes
//     it once (lower case uuid text) before the lock, the lookup, the
//     module's dedup and the remember, so every one of them sees one
//     spelling and the two spellings share one lock. The seam: a key that
//     was sent in upper case and applied before this ledger was stored as
//     sent, and a module that dedups by comparing text (the tasks event
//     note) no longer matches it against the lower case spelling, so that
//     one legacy key applies again. The uuid typed columns are unaffected,
//     the money scopes refuse every push, and the client mints lower case
//     keys, so it takes a caller supplied upper case key older than the
//     ledger. It closes with the rest of the pre ledger keys, above.
//   - Two pushes of one key race: find, apply and remember are separate
//     statements, so without serialization both bodies could apply. The
//     engine takes the ledger's per slot Lock around all three, so the
//     second push sees the first one's hash (or its refusal) before it
//     applies anything. A holder keeps a pool connection for the whole
//     push, so the Postgres ledger caps holders at half the pool. It does
//     not refuse a small pool at construction: Lock refuses a pool of fewer
//     than two connections with ErrLedgerPoolTooSmall, and serve refuses it
//     at boot (RequireLedgerPool). Pushes of one hot key queue behind one
//     another by design.
//   - The ledger write after the module's commit runs on a context detached
//     from the request: a client that drops its connection cancels the
//     request context but not the binding. What can still leave one applied
//     key unbound is a fault of the ledger write itself (the database
//     refusing it), the process dying between the module's commit and the
//     insert, or a client that drops while the module's own COMMIT round
//     trip is in flight: the stores commit on the request context, so the
//     driver can return the cancellation although the server committed, and
//     the push fails before the ledger write runs. That last window is one
//     database round trip wide. In each case the push fails whole, the
//     client retries it whole, and the retry sees the applied key with no
//     hash (a legacy key, above) while the row still holds it. The guarantee is weakest for exactly
//     that one key per such fault, never silently wrong for a key that was
//     stored.

// ErrKeyReused is the refusal for a key reused with a different body. The
// reason word leads the message so a client can branch on it: it is the
// same word the offline store's outbox uses for the same fault found
// locally (web/packages/offline-store/src/outbox.ts, reason "key_reused").
var ErrKeyReused = errors.New("key_reused: this idempotency key already names a different operation")

// auditKeyReused is the audit action for a refused reuse, distinct from
// SYNC_PUSH so an operator can filter on action alone for the one event
// that is either a client defect or a probe.
const auditKeyReused = "SYNC_KEY_REUSED"

// LedgerEntry is what the ledger holds per applied key: the hash of the
// body that first applied, and the id of the row it touched (the created
// row's id for a create, the payload's id for an update or delete).
type LedgerEntry struct {
	Hash     string
	EntityID string
}

// KeyLedger is the seam holding, per applied idempotency key, the hash of
// the body that first applied under it. Every half runs under the caller's
// ScopeFilter: a key is only ever compared with keys of the same org, the
// same tenant (a tenant caller) and the same scope, so one dealer's keys
// never refuse another's and existence never leaks across them.
type KeyLedger interface {
	// Lock serializes everything done for one key in one scope under f: the
	// holder alone may look up, apply and remember that key until release
	// runs. A holder that vanishes (a lost connection) releases by itself.
	Lock(ctx context.Context, f ScopeFilter, scope, key string) (release func(), err error)
	// Lookup returns the entry stored when key first applied in scope under
	// f, and whether one is stored.
	Lookup(ctx context.Context, f ScopeFilter, scope, key string) (LedgerEntry, bool, error)
	// Remember stores the entry for a key that has just applied. The first
	// write wins: a second Remember for the same slot changes nothing.
	Remember(ctx context.Context, f ScopeFilter, scope, key string, entry LedgerEntry) error
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

// jsSeparators puts back the two characters Go's encoder always escapes and
// JavaScript's JSON.stringify writes literally (U+2028 and U+2029), so the
// common text matches the client's canonical string. It walks the encoded
// string escape by escape, so a backslash that is itself escaped (a payload
// holding the text backslash u 2 0 2 8) is never mistaken for the escape.
func jsSeparators(s string) string {
	if !strings.Contains(s, sepEscapePrefix) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch {
		case strings.HasPrefix(s[i:], lineSepEscape):
			b.WriteRune(0x2028)
			i += len(lineSepEscape) - 1
		case strings.HasPrefix(s[i:], paraSepEscape):
			b.WriteRune(0x2029)
			i += len(paraSepEscape) - 1
		default:
			b.WriteByte(s[i])
			b.WriteByte(s[i+1])
			i++
		}
	}
	return b.String()
}

// The six character escapes Go's encoder writes for the two separators.
const (
	sepEscapePrefix = "\\" + "u202"
	lineSepEscape   = sepEscapePrefix + "8"
	paraSepEscape   = sepEscapePrefix + "9"
)

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
		b.WriteString(jsSeparators(strings.TrimSuffix(buf.String(), "\n")))
	}
	return nil
}
