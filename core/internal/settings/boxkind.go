package settings

// The box kind mark (W2-1, W2-2): one system_settings key naming what a box
// is, written once by the guarded box setup commands and read back by every
// guarded command before it acts. It is not part of DealerConfig: it names
// the deployment, not the dealer, and the dealer configuration's five key
// write never creates or changes it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// KeyBoxKind is the system_settings key holding the box kind word.
const KeyBoxKind = "box_kind"

// boxKindShape is the alphabet of a box kind word: a short lowercase word.
var boxKindShape = legacyImportShape

// parseBoxKind answers the stored box kind word, or "" for an absent, blank
// or malformed value. bad, when set, is told of a malformed value so the
// reader can log the key, never the value.
func parseBoxKind(raw json.RawMessage, bad func()) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		if bad != nil {
			bad()
		}
		return ""
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !boxKindShape.MatchString(s) {
		if bad != nil {
			bad()
		}
		return ""
	}
	return s
}

// BoxKind is the box's kind word, uncached: "" when the key is absent, blank
// or malformed (a malformed value is logged by key, never by value). A nil
// reader or pool answers "".
func (r *DealerConfigReader) BoxKind(ctx context.Context) (string, error) {
	if r == nil || r.pool == nil {
		return "", nil
	}
	var v []byte
	err := r.pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, KeyBoxKind).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return parseBoxKind(json.RawMessage(v), func() {
		r.log.Warn("settings: the box kind is malformed; treating the box as unmarked", "key", KeyBoxKind)
	}), nil
}

// WriteBoxKindTx validates and upserts the box kind mark on the caller's
// transaction: the seam a guarded box setup command writes it through, so
// the mark commits or rolls back with the rest of the setup. A blank or
// malformed kind is refused; a box with no mark has no row.
//
// The shape is checked before the handle, so a malformed kind refuses as
// malformed on any handle and the refusal never reads as the missing
// handle's refusal wearing the shape's name. A well shaped kind on no
// handle refuses as the missing handle it is.
func WriteBoxKindTx(ctx context.Context, tx DealerConfigExecer, kind string) error {
	if !boxKindShape.MatchString(kind) {
		return fmt.Errorf("settings: box kind must be a short lowercase word")
	}
	if tx == nil {
		return errors.New("settings: a box kind write requires a database handle")
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
