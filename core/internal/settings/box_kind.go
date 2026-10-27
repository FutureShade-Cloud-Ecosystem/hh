package settings

// box_kind.go, the finance box's own key in system_settings (the deploy spec
// W2-1, sections 3 and 6). The key records which box kind seeded the
// database; serve and worker on a box that says HH_BOX_KIND=finance refuse to
// boot against a non empty database whose box_kind is not finance, so a box
// pointed at the testers' copy, a staging world or a demo world stops before
// migrating, never writing into somebody else's data.
//
// settings owns system_settings (dealer_config.go above), so settings owns
// this read and this write, and every consumer goes through these two seams.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
)

// KeyBoxKind is the system_settings key the box kind is recorded under.
const KeyBoxKind = "box_kind"

// boxKindShape pins the kind vocabulary: a lowercase word, the same shape
// discipline the legacy import source carries.
var boxKindShape = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ReadBoxKind answers the recorded box kind, "" when the key is absent. A
// malformed stored value answers "" too: the guard treats a database that
// cannot say finance as not finance.
func ReadBoxKind(ctx context.Context, q DealerConfigQuerier) (string, error) {
	if q == nil {
		return "", fmt.Errorf("settings: reading %s requires a database handle", KeyBoxKind)
	}
	var v []byte
	err := q.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, KeyBoxKind).Scan(&v)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return "", nil
	}
	if !boxKindShape.MatchString(s) {
		return "", nil
	}
	return s, nil
}

// WriteBoxKindTx validates and upserts the box kind on the caller's
// transaction: the seam the finance seed writes it through, so the kind
// commits or rolls back with the seed itself.
func WriteBoxKindTx(ctx context.Context, tx DealerConfigExecer, kind string) error {
	if tx == nil {
		return fmt.Errorf("settings: writing %s requires a database handle", KeyBoxKind)
	}
	if !boxKindShape.MatchString(kind) {
		return fmt.Errorf("settings: box kind must be a lowercase word of up to 32 letters, digits or dashes")
	}
	b, err := json.Marshal(kind)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ($1, $2::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
		 WHERE system_settings.value IS DISTINCT FROM EXCLUDED.value`, KeyBoxKind, string(b))
	return err
}
