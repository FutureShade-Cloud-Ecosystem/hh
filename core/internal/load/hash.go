package load

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	b, err := json.Marshal(rec)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
