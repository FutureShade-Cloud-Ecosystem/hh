package attachments

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// The untagged legs are the pure guards: the D10 enums, the uuid
// boundary, the link-set validation, and the writable-set refusals (the
// field-set checks run before any database access, so a nil-pool store
// proves them without a database). The database-backed battery, GATE-7
// and the replay property live in the sync package's integration suite.

func TestValidKindAndOwnerKind(t *testing.T) {
	for _, k := range []string{KindPhoto, KindProofOfDelivery, KindDocument, KindSignature, KindProductImage} {
		if !validKind(k) {
			t.Fatalf("kind %q must be valid", k)
		}
	}
	for _, k := range []string{"", "video", "PHOTO", "proof"} {
		if validKind(k) {
			t.Fatalf("kind %q must be refused", k)
		}
	}
	for _, k := range []string{OwnerOrder, OwnerPlan, OwnerStop, OwnerReceiving, OwnerQuote} {
		if !validOwnerKind(k) {
			t.Fatalf("owner kind %q must be valid", k)
		}
	}
	for _, k := range []string{"", "task", "ORDER", "attachment"} {
		if validOwnerKind(k) {
			t.Fatalf("owner kind %q must be refused", k)
		}
	}
}

func TestLooksLikeUUID(t *testing.T) {
	if !looksLikeUUID("3ef10000-0000-4000-8000-000000000073") {
		t.Fatal("a canonical uuid must pass")
	}
	for _, bad := range []string{"", "not-a-uuid", "3ef10000-0000-4000-8000-00000000007", "3EF10000-0000-4000-8000-0000000000733"} {
		if looksLikeUUID(bad) {
			t.Fatalf("%q must be refused", bad)
		}
	}
}

func TestLinksOf(t *testing.T) {
	links, err := linksOf([]any{
		map[string]any{"owner_kind": "order", "owner_id": "3ef10000-0000-4000-8000-000000000073", "role": "evidence"},
		map[string]any{"owner_kind": "quote", "owner_id": "3ef10000-0000-4000-8000-000000000075", "role": ""},
	})
	if err != nil {
		t.Fatalf("valid link set: %v", err)
	}
	if len(links) != 2 || links[0].OwnerKind != OwnerOrder || links[1].Role != "" {
		t.Fatalf("link set: %+v", links)
	}
	if _, err := linksOf(map[string]any{}); err == nil {
		t.Fatal("a non-array links leg must be refused")
	}
	if _, err := linksOf([]any{map[string]any{"owner_kind": "task", "owner_id": "3ef10000-0000-4000-8000-000000000073"}}); err == nil {
		t.Fatal("an unknown owner_kind must be refused")
	}
	if _, err := linksOf([]any{map[string]any{"owner_kind": "order", "owner_id": "nope"}}); err == nil {
		t.Fatal("a non-uuid owner_id must be refused")
	}
	// The same edge declared twice is the unique index's own defect,
	// refused here rather than at the insert.
	dup := []any{
		map[string]any{"owner_kind": "order", "owner_id": "3ef10000-0000-4000-8000-000000000073", "role": "pod"},
		map[string]any{"owner_kind": "order", "owner_id": "3ef10000-0000-4000-8000-000000000073", "role": "pod"},
	}
	if _, err := linksOf(dup); err == nil {
		t.Fatal("a duplicate edge must be refused before the insert")
	}
}

// TestCreateRefusesContentShapedFields: bytes never ride the sync
// payload. Anything shaped like content (bytes, data, base64, a data URL)
// is outside the writable create set and refuses whole, before any
// database access.
func TestCreateRefusesContentShapedFields(t *testing.T) {
	s := NewSyncStore(nil) // no database: the guard runs first
	f := sync.ScopeFilter{OrgID: "3ef10000-0000-4000-8000-000000000001", Actor: "fxstaff-yard"}
	for _, field := range []string{"bytes", "data", "base64", "url", "content", "thumbnail"} {
		_, err := s.Create(context.Background(), f, "00000000-0000-4000-8000-0000000000aa",
			map[string]any{"kind": "photo", "appwrite_file_id": "f1", "bucket": "hh", field: "x"})
		if !errors.Is(err, sync.ErrOpRefused) {
			t.Fatalf("field %q: err %v, want the classified refusal", field, err)
		}
		if !strings.Contains(err.Error(), "not writable through sync") {
			t.Fatalf("field %q: the refusal did not name the writable set: %v", field, err)
		}
	}
}

// TestWriteRefusesIdentityLegs: the merge cannot move the record's
// address (kind, appwrite_file_id, bucket point at the bytes in Storage);
// a payload declaring any of them refuses whole, as does a resolved
// field outside the writable set.
func TestWriteRefusesIdentityLegs(t *testing.T) {
	s := NewSyncStore(nil)
	f := sync.ScopeFilter{OrgID: "3ef10000-0000-4000-8000-000000000001"}
	for _, field := range []string{"kind", "appwrite_file_id", "bucket", "tenant_id", "org_id", "created_by"} {
		_, err := s.Write(context.Background(), f, "00000000-0000-4000-8000-000000000073",
			"00000000-0000-4000-8000-0000000000ab", map[string]any{field: "x"})
		if !errors.Is(err, sync.ErrOpRefused) {
			t.Fatalf("field %q: err %v, want the classified refusal", field, err)
		}
	}
}

// TestCreateRequiredLegs: kind and the Appwrite address are required;
// the refusal is the classified per-operation shape.
func TestCreateRequiredLegs(t *testing.T) {
	s := NewSyncStore(nil)
	f := sync.ScopeFilter{OrgID: "3ef10000-0000-4000-8000-000000000001"}
	cases := []map[string]any{
		{"appwrite_file_id": "f1", "bucket": "hh"},                  // no kind
		{"kind": "photo", "bucket": "hh"},                           // no file id
		{"kind": "photo", "appwrite_file_id": "f1"},                 // no bucket
		{"kind": "video", "appwrite_file_id": "f1", "bucket": "hh"}, // bad kind
		{"kind": "photo", "appwrite_file_id": "f1", "bucket": "hh", "size_bytes": 1.5},
		{"kind": "photo", "appwrite_file_id": "f1", "bucket": "hh", "tenant_id": "not-a-uuid"},
	}
	for i, fields := range cases {
		_, err := s.Create(context.Background(), f, "00000000-0000-4000-8000-0000000000ac", fields)
		if !errors.Is(err, sync.ErrOpRefused) {
			t.Fatalf("case %d (%v): err %v, want the classified refusal", i, fields, err)
		}
	}
}
