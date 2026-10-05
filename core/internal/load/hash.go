package load

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// HashRecord hashes the normalised record: the canonical JSON re-encode
// of the decoded record over sha256, hex. encoding/json walks map keys
// in sorted order, so the wire's field order never moves the hash; two
// spellings of one record hash alike, and two records that differ in any
// field do not. A record that cannot re-encode normalises to no hash at
// all, which the upsert refuses as record_shape before any write.
func HashRecord(rec map[string]any) string {
	if rec == nil {
		return ""
	}
	return canonicalHash(rec)
}

// HashItem hashes the whole named item, the shape the replay decision
// reads: the record plus the item's named references, kind and legacy id
// as named (never the resolved ids, so an upstream renumber churns no
// hash), the references sorted so their wire order never moves the hash.
// The named shape is the normalisation: an item whose record or
// references cannot re-encode normalises to no hash, refused as
// record_shape.
func HashItem(item Item) string {
	if item.Record == nil {
		return ""
	}
	refs := make([]Ref, len(item.Requires))
	copy(refs, item.Requires)
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		return refs[i].LegacyID < refs[j].LegacyID
	})
	return canonicalHash(map[string]any{
		"record":   item.Record,
		"requires": refs,
	})
}

// canonicalHash is the one hash: canonical JSON over sha256, hex.
func canonicalHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
