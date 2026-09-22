package customerquote

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func fixtureFields() ArtifactFields {
	at := time.Date(2026, 1, 2, 10, 20, 30, 123456700, time.UTC)
	return ArtifactFields{
		AcceptanceID:          "3ef10000-0000-4000-8000-00000000a0a1",
		QuoteID:               "3ef10000-0000-4000-8000-00000000a0a2",
		OrgID:                 "3ef10000-0000-4000-8000-000000000001",
		TenantID:              "3ef10000-0000-4000-8000-000000000021",
		SignedName:            "Fixture Homeowner One",
		Affirmative:           true,
		ConsentText:           ConsentText,
		AcceptedTotalCents:    385000,
		AcceptedAt:            at,
		SignatureAttachmentID: "",
	}
}

// TestCanonicalBytesArePinned is the golden: the exact bytes, in the pinned
// order, with the version inside, six-digit stamp, no whitespace.
func TestCanonicalBytesArePinned(t *testing.T) {
	b, err := CanonicalArtifactBytes(fixtureFields())
	if err != nil {
		t.Fatalf("canonical bytes: %v", err)
	}
	want := `{"v":"1","acceptance_id":"3ef10000-0000-4000-8000-00000000a0a1",` +
		`"quote_id":"3ef10000-0000-4000-8000-00000000a0a2",` +
		`"org_id":"3ef10000-0000-4000-8000-000000000001",` +
		`"tenant_id":"3ef10000-0000-4000-8000-000000000021",` +
		`"signed_name":"Fixture Homeowner One","affirmative":true,` +
		`"consent_text":"` + jsonEscape(ConsentText) + `",` +
		`"accepted_total_cents":385000,` +
		`"accepted_at":"2026-01-02T10:20:30.123456Z",` +
		`"signature_attachment_id":""}`
	if string(b) != want {
		t.Fatalf("canonical bytes:\n%s\nwant:\n%s", b, want)
	}
}

// jsonEscape mirrors encoding/json string escaping with HTML escaping
// disabled, for building goldens in tests.
func jsonEscape(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return ""
	}
	return strings.TrimSuffix(buf.String(), "\n")[1 : len(buf.String())-2]
}

// TestCanonicalWriterMatchesEncoderNoHTML is the mutual pin: the hand-built
// writer must produce exactly what encoding/json produces with HTML escaping
// disabled (F-4's serializer pin), for names carrying HTML-escapable
// characters, quotes, controls, the actual U+2028 and U+2029 code points,
// and invalid UTF-8.
func TestCanonicalWriterMatchesEncoderNoHTML(t *testing.T) {
	// The two line separators as real code points, not the words: U+2028 is
	// UTF-8 e2 80 a8 and U+2029 is e2 80 a9, verified here by byte so the
	// fixture can never silently regress to an ASCII lookalike (the review's
	// R4: the earlier fixture spelled them out in words and the writer's two
	// escape branches ran unexercised).
	lineSeparators := "Line separator\u2028and next line\u2029end"
	if !bytes.Contains([]byte(lineSeparators), []byte{0xe2, 0x80, 0xa8}) {
		t.Fatalf("the line-separator fixture does not carry the U+2028 bytes e2 80 a8")
	}
	if !bytes.Contains([]byte(lineSeparators), []byte{0xe2, 0x80, 0xa9}) {
		t.Fatalf("the line-separator fixture does not carry the U+2029 bytes e2 80 a9")
	}
	names := []string{
		"Plain Name",
		"Angle <Brackets> & Ampersand",
		`Quote " and \ Backslash`,
		"Control\x01\x1f chars",
		"Tab\there",
		lineSeparators,
		"Ünïcödé 名前",
	}
	// The live-encoder mutual pin runs over VALID UTF-8 fixtures only:
	// encoding/json's rendering of invalid bytes differs between Go
	// minors (one emits the raw replacement rune, another the \ufffd
	// escape), so the invalid cases are pinned as fixed bytes below,
	// against the writer's own definition, which is the version
	// independent one.
	for _, name := range names {
		f := fixtureFields()
		f.SignedName = name
		got, err := CanonicalArtifactBytes(f)
		if err != nil {
			t.Fatalf("canonical bytes for %q: %v", name, err)
		}
		// The encoder reference: a struct with the pinned order, encoded
		// compactly with HTML escaping off.
		want, err := encoderReference(f)
		if err != nil {
			t.Fatalf("encoder reference: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("writer and encoder disagree for %q:\nwriter:   %s\nencoder: %s", name, got, want)
		}
		// The pin is HTML escaping DISABLED: angle brackets and ampersands
		// stay literal in the canonical bytes on both paths, which is what
		// makes sign-time and verify-time agree; the escape set is the JSON
		// minimum (quote, backslash, controls, the two line separators).
		if bytes.Contains(got, []byte("\\u003c")) || bytes.Contains(got, []byte("\\u0026")) {
			t.Fatalf("canonical bytes for %q HTML-escaped; the pin disables that", name)
		}
		// The two separator escape branches run and fire on this fixture,
		// so their coverage is real rather than incidental.
		if name == lineSeparators {
			if !bytes.Contains(got, []byte(`\u2028`)) || !bytes.Contains(got, []byte(`\u2029`)) {
				t.Fatalf("the canonical bytes did not escape U+2028 and U+2029: %s", got)
			}
		}
	}
}

func encoderReference(f ArtifactFields) ([]byte, error) {
	ref := struct {
		V                     string `json:"v"`
		AcceptanceID          string `json:"acceptance_id"`
		QuoteID               string `json:"quote_id"`
		OrgID                 string `json:"org_id"`
		TenantID              string `json:"tenant_id"`
		SignedName            string `json:"signed_name"`
		Affirmative           bool   `json:"affirmative"`
		ConsentText           string `json:"consent_text"`
		AcceptedTotalCents    int64  `json:"accepted_total_cents"`
		AcceptedAt            string `json:"accepted_at"`
		SignatureAttachmentID string `json:"signature_attachment_id"`
	}{
		V: ArtifactVersion, AcceptanceID: lower(f.AcceptanceID), QuoteID: lower(f.QuoteID),
		OrgID: lower(f.OrgID), TenantID: lower(f.TenantID), SignedName: f.SignedName,
		Affirmative: f.Affirmative, ConsentText: f.ConsentText,
		AcceptedTotalCents:    f.AcceptedTotalCents,
		AcceptedAt:            f.AcceptedAt.UTC().Truncate(time.Microsecond).Format(artifactStampLayout),
		SignatureAttachmentID: f.SignatureAttachmentID,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ref); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func lower(s string) string { return strings.ToLower(s) }

// TestTimestampPrecisionRoundTrip pins the accepted_at rendering: UTC, always
// six fractional digits, and byte-identical after the database's microsecond
// truncation, so rebuild-from-row reproduces the signed bytes.
func TestTimestampPrecisionRoundTrip(t *testing.T) {
	f := fixtureFields()
	f.AcceptedAt = time.Date(2026, 3, 4, 5, 6, 7, 999999999, time.FixedZone("FX", 2*3600))
	b1, err := CanonicalArtifactBytes(f)
	if err != nil {
		t.Fatalf("bytes: %v", err)
	}
	var probe struct {
		AcceptedAt string `json:"accepted_at"`
	}
	if err := json.Unmarshal(b1, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasSuffix(probe.AcceptedAt, "Z") {
		t.Fatalf("stamp %q is not UTC", probe.AcceptedAt)
	}
	if !strings.HasSuffix(probe.AcceptedAt, ".999999Z") {
		t.Fatalf("stamp %q does not carry exactly six fractional digits from the truncated nanoseconds", probe.AcceptedAt)
	}
	// The database round trip truncates to microsecond; the rebuild matches.
	stored := f.AcceptedAt.Truncate(time.Microsecond)
	f2 := f
	f2.AcceptedAt = stored
	b2, err := CanonicalArtifactBytes(f2)
	if err != nil {
		t.Fatalf("bytes: %v", err)
	}
	// Sub-microsecond input truncates at signing, so both paths agree.
	b3, _ := CanonicalArtifactBytes(f)
	if !bytes.Equal(b2, b3) {
		t.Fatalf("truncated and signed bytes differ")
	}
}

func TestDigestAndConstantTimeCompare(t *testing.T) {
	key := bytes.Repeat([]byte{0x0a}, 32)
	f := fixtureFields()
	hashHex, sigHex, err := ArtifactDigest(key, f)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if len(hashHex) != 64 || len(sigHex) != 64 {
		t.Fatalf("hex lengths %d/%d, want 64/64", len(hashHex), len(sigHex))
	}
	// An independent recompute over the same bytes.
	b, _ := CanonicalArtifactBytes(f)
	sum := sha256.Sum256(b)
	mac := hmac.New(sha256.New, key)
	mac.Write(b)
	if hashHex != hex.EncodeToString(sum[:]) || sigHex != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatalf("digest disagrees with an independent recompute")
	}
	if !CompareMAC(sigHex, sigHex) {
		t.Fatalf("identical MACs must compare equal")
	}
	other, _, _ := ArtifactDigest(bytes.Repeat([]byte{0x0b}, 32), f)
	if CompareMAC(sigHex, other) {
		t.Fatalf("MACs under different keys must compare unequal")
	}
	if CompareMAC("not hex!", sigHex) {
		t.Fatalf("a non-hex stored signature must compare unequal, never panic")
	}
	if CompareMAC(sigHex[:32], sigHex) {
		t.Fatalf("a short stored signature must compare unequal")
	}
	// One flipped field changes both hash and signature.
	f.SignedName = "Fixture Homeowner Two"
	h2, s2, _ := ArtifactDigest(key, f)
	if h2 == hashHex || s2 == sigHex {
		t.Fatalf("a mutated field must change the digest and the signature")
	}
}

func TestLoadSigningKeysValidation(t *testing.T) {
	goodKey := hex.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	if _, err := LoadSigningKeys(func(string) string { return "" }); err == nil {
		t.Fatalf("spawn without a key must fail")
	}
	if _, err := LoadSigningKeys(mapGet(map[string]string{
		EnvKeyID: "fx-accept-key", EnvKey: "abcd",
	})); err == nil {
		t.Fatalf("a short key must fail the entropy floor")
	}
	if _, err := LoadSigningKeys(mapGet(map[string]string{
		EnvKeyID: "FX-BAD", EnvKey: goodKey,
	})); err == nil {
		t.Fatalf("an ill-shaped key id must fail")
	}
	if _, err := LoadSigningKeys(mapGet(map[string]string{
		EnvKeyID: "k-1234567", EnvKey: goodKey,
	})); err == nil {
		t.Fatalf("a key id carrying a seven-digit run must fail (it could trip the audit chokepoint)")
	}
	if _, err := LoadSigningKeys(mapGet(map[string]string{
		EnvKeyID: "fx-accept-key", EnvKey: goodKey, EnvPreviousKey: goodKey,
	})); err == nil {
		t.Fatalf("a previous key without its id must fail")
	}
	keys, err := LoadSigningKeys(mapGet(map[string]string{
		EnvKeyID: "fx-accept-key", EnvKey: goodKey,
		EnvPreviousKeyID: "fx-accept-prev", EnvPreviousKey: hex.EncodeToString(bytes.Repeat([]byte{0x22}, 32)),
	}))
	if err != nil {
		t.Fatalf("valid schedule failed: %v", err)
	}
	if _, err := keys.KeyFor("fx-accept-key"); err != nil {
		t.Fatalf("current key must resolve")
	}
	if _, err := keys.KeyFor("fx-accept-prev"); err != nil {
		t.Fatalf("previous key must resolve while configured")
	}
	if _, err := keys.KeyFor("fx-withdrawn"); err == nil {
		t.Fatalf("an unknown key id must fail, naming itself")
	}
}

func mapGet(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestSeparatorEncodingSurvivesChokepoint is the F-1 companion: a signature
// containing a seven-digit run, which the carried audit chokepoint redacts
// whole-value, rides the seam separator-encoded and survives verbatim, while
// the raw hex would not.
func TestSeparatorEncodingSurvivesChokepoint(t *testing.T) {
	// A hex string with a deliberate long digit run.
	hostile := ""
	for i := 0; i < 6; i++ {
		hostile += "1234567890"
	}
	hostile += strings.Repeat("ab", 2)
	if !HasSevenDigitRun(hostile) {
		t.Fatalf("fixture hex must carry a seven-digit run")
	}
	encoded := separatorEncodeHex(hostile)
	if HasSevenDigitRun(encoded) {
		t.Fatalf("encoded value %q still carries a seven-digit run", encoded)
	}
	// The chokepoint semantics, copied from the carried audit service: a
	// seven-digit run redacts the whole value.
	if got := chokepointRedact(hostile); got != "[redacted]" {
		t.Fatalf("raw hex survived the chokepoint as %q; the hazard is real", got)
	}
	if got := chokepointRedact(encoded); got != encoded {
		t.Fatalf("encoded value did not survive the chokepoint verbatim: %q", got)
	}
	// Round trip.
	decoded, err := separatorDecodeHex(encoded)
	if err != nil || decoded != hostile {
		t.Fatalf("round trip failed: %v %q", err, decoded)
	}
	// Corrupted encodings fail closed.
	for _, bad := range []string{"1234567", "abcdef:12345", "abc", "12:34:5:6:7", "abc:def"} {
		if _, err := separatorDecodeHex(bad); err == nil {
			t.Fatalf("decode(%q) accepted an ill-formed value", bad)
		}
	}
	// A full sha256-hex artifact hash encodes and decodes whole.
	sum := sha256.Sum256([]byte("fixture"))
	h := hex.EncodeToString(sum[:])
	if d, err := separatorDecodeHex(separatorEncodeHex(h)); err != nil || d != h {
		t.Fatalf("artifact hash round trip failed")
	}
	// A realistic hostile signature: HMAC output containing a digit run is
	// found by search, proving the class exists, then survives encoded.
	key := bytes.Repeat([]byte{0x33}, 32)
	for i := 0; i < 5000; i++ {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(fmt.Sprintf("fixture-%d", i)))
		sig := hex.EncodeToString(mac.Sum(nil))
		if HasSevenDigitRun(sig) {
			enc := separatorEncodeHex(sig)
			if HasSevenDigitRun(enc) || chokepointRedact(enc) != enc {
				t.Fatalf("a hostile signature (digit run present) did not survive encoded")
			}
			if d, err := separatorDecodeHex(enc); err != nil || d != sig {
				t.Fatalf("hostile signature round trip failed")
			}
			return
		}
	}
	t.Fatalf("no hostile signature found in the search window; widen it")
}

// chokepointRedact copies the carried audit boundary's string rule (ERP
// backend/internal/audit/service.go): a run of seven or more consecutive
// digits redacts the whole value. It exists only to prove the encoding's
// survival against the real rule's semantics.
func chokepointRedact(s string) string {
	if HasSevenDigitRun(s) {
		return "[redacted]"
	}
	return s
}

// TestCanonicalWriterInvalidUTF8FixedBytes pins the writer's invalid-UTF-8
// output as fixed bytes: each invalid byte maps to the six-character
// \ufffd escape, independent of the Go toolchain (the live encoder's
// rendering of invalid bytes differs between minors, which is exactly why
// the canonical form is defined here and not by the toolchain).
func TestCanonicalWriterInvalidUTF8FixedBytes(t *testing.T) {
	f := fixtureFields()
	f.SignedName = "Bytes\xff\xfe invalid"
	got, err := CanonicalArtifactBytes(f)
	if err != nil {
		t.Fatalf("canonical bytes: %v", err)
	}
	if !bytes.Contains(got, []byte("\\ufffd\\ufffd")) {
		t.Fatalf("invalid bytes must map per byte to the \\ufffd escape; got: %s", got)
	}
	if bytes.Contains(got, []byte{0xef, 0xbf, 0xbd}) {
		t.Fatalf("the raw replacement rune must not appear for invalid bytes (it would collide with a typed U+FFFD and vary by toolchain); got: %s", got)
	}
	// A TYPED U+FFFD stays raw and distinguishable from the escape.
	f2 := fixtureFields()
	f2.SignedName = "Typed\ufffd replacement"
	got2, err := CanonicalArtifactBytes(f2)
	if err != nil {
		t.Fatalf("canonical bytes: %v", err)
	}
	if !bytes.Contains(got2, []byte{0xef, 0xbf, 0xbd}) {
		t.Fatalf("a typed U+FFFD must pass through as raw bytes; got: %s", got2)
	}
	if bytes.Contains(got2, []byte("\\ufffd")) {
		t.Fatalf("a typed U+FFFD must not be escaped; got: %s", got2)
	}
}
