package till

import "testing"

// DIB-513. The AR console's payment inspector is keyed by whatever a clerk types, and what a clerk
// physically holds is the payment NUMBER on the receipt (PAY-2026-0007), not the row's UUID. Before
// this, `WHERE p.id = $1` on a uuid column turned that number into SQLSTATE 22P02, which reached the
// operator as HTTP 500 "internal error": a fault report for a lookup that simply missed.
//
// The lookup now takes the id arm only when the key HAS a uuid's shape, so the uuid cast never sees
// a number. These cases pin that decision, which is the whole mechanism.
func TestUUIDLikeSelectsTheIDArm(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		want bool
	}{
		{"a real uuid", "a5130000-0000-4000-9000-000000000001", true},
		{"uppercase hex is still a uuid", "A5130000-0000-4000-9000-00000000000F", true},
		{"a payment number is not", "PAY-2026-0007", false},
		{"an invoice number is not", "INV-2026-0002", false},
		{"empty is not", "", false},
		{"right length, wrong separators", "a5130000+0000+4000+9000+000000000001", false},
		{"right length, non-hex payload", "z5130000-0000-4000-9000-000000000001", false},
		{"a uuid with no dashes is not (wrong length)", "a5130000000040009000000000000001", false},
		{"a uuid with trailing space is not", "a5130000-0000-4000-9000-000000000001 ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := uuidLike(tc.key); got != tc.want {
				t.Fatalf("uuidLike(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

// uuidOrNil is what actually rides the wire as $2: the uuid itself, or NULL so the cast is applied
// to nothing. A non-uuid key that arrived as a NON-nil pointer would put the 500 straight back.
func TestUUIDOrNilKeepsNonUUIDKeysAwayFromTheCast(t *testing.T) {
	if got := uuidOrNil("PAY-2026-0007"); got != nil {
		t.Fatalf("a payment number must ride as NULL::uuid, got %q", *got)
	}
	const id = "a5130000-0000-4000-9000-000000000001"
	got := uuidOrNil(id)
	if got == nil || *got != id {
		t.Fatalf("a uuid must ride as itself, got %v", got)
	}
}
