package landedcost

// repository_uuid_test.go, regression for sweep #46 finding #21: a malformed non-UUID {id} on the
// landed-cost routes reaches `WHERE id = $1` on a uuid column and raises SQLSTATE 22P02. scanLanded
// now folds that into errNotFound alongside pgx.ErrNoRows, so a garbage id and a missing doc are both
// a clean 404 (IDOR-safe) instead of an opaque 500. This pins the fold helper + the 404 mapping
// without a DB (mirrors procurement/service_test.go TestNotFoundMapping_InvalidUUID).

import (
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsInvalidUUID_FoldsTo404(t *testing.T) {
	invalidUUID := &pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type uuid: "not-a-uuid"`}
	if !isInvalidUUID(invalidUUID) {
		t.Fatalf("isInvalidUUID(22P02) = false, want true")
	}
	// A no-rows miss and a malformed id must be indistinguishable, both land in scanLanded's
	// not-found branch. pgx.ErrNoRows is handled by the sibling errors.Is check, not isInvalidUUID.
	if isInvalidUUID(pgx.ErrNoRows) {
		t.Fatalf("isInvalidUUID(ErrNoRows) = true, want false (ErrNoRows is handled separately)")
	}
	// An unrelated pg error must NOT be swallowed as not-found (else real 500s vanish).
	if isInvalidUUID(&pgconn.PgError{Code: "23505", Message: "unique_violation"}) {
		t.Fatalf("isInvalidUUID(23505) = true, want false")
	}
	if isInvalidUUID(errors.New("boom")) {
		t.Fatalf("isInvalidUUID(plain error) = true, want false")
	}

	// The sentinel scanLanded returns for a folded 22P02 must map to HTTP 404 at the handler.
	var ce clientError
	if !errors.As(error(errNotFound), &ce) || ce.status != http.StatusNotFound {
		t.Fatalf("errNotFound must be a clientError with status 404, got %+v", errNotFound)
	}
}
