package load

// hash_test.go pins the content hash's normalisation: the helper's replay
// decision (exit line 3) reads this hash, so two wire spellings of one
// record must hash alike and two records that differ in a field must not.

import "testing"

func TestHashRecordIsOrderIndependent(t *testing.T) {
	a := map[string]any{"code": "c-1", "payload": "p", "nested": map[string]any{"b": 2, "a": 1}}
	b := map[string]any{"nested": map[string]any{"a": 1, "b": 2}, "payload": "p", "code": "c-1"}
	if HashRecord(a) != HashRecord(b) {
		t.Fatalf("one record in two key orders hashed differently: %s vs %s", HashRecord(a), HashRecord(b))
	}
}

func TestHashRecordSeparatesRecords(t *testing.T) {
	a := map[string]any{"code": "c-1", "payload": "p"}
	b := map[string]any{"code": "c-1", "payload": "changed"}
	if HashRecord(a) == HashRecord(b) {
		t.Fatal("two records differing in one field hashed alike")
	}
}

func TestHashRecordShape(t *testing.T) {
	h := HashRecord(map[string]any{"code": "c-1"})
	if len(h) != 64 {
		t.Fatalf("hash is %d characters, want the 64 of a hex sha256: %q", len(h), h)
	}
}
