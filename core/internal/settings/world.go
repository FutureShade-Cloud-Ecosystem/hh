package settings

// The finance world sha256 record (W2-1b): one system_settings key holding
// the sha256 of the world dump the finance box's reset last restored, written
// by the reset itself after the restore succeeds, in the same transaction as
// the reset's audit row, and read back by the reset's -once flag so a boot
// that already sits on the named world changes nothing. It is not part of
// DealerConfig: it names the deployment's world revision, not the dealer,
// and the dealer configuration's five key write never creates or changes it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// KeyFinanceWorldSHA256 is the system_settings key holding the recorded
// world dump's sha256.
const KeyFinanceWorldSHA256 = "finance_world_sha256"

// financeWorldSHA256Shape is the alphabet of a recorded world digest: the
// lowercase hex spelling of sixty four bytes.
var financeWorldSHA256Shape = regexp.MustCompile(`^[0-9a-f]{64}$`)

// parseFinanceWorldSHA256 answers the stored world digest, or "" for an
// absent, blank or malformed value. bad, when set, is told of a malformed
// value so the reader can log the key, never the value.
func parseFinanceWorldSHA256(raw json.RawMessage, bad func()) string {
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
	if !financeWorldSHA256Shape.MatchString(s) {
		if bad != nil {
			bad()
		}
		return ""
	}
	return s
}

// FinanceWorldSHA256 is the recorded world dump's sha256, uncached: "" when
// the key is absent, blank or malformed (a malformed value is logged by key,
// never by value). A nil reader or pool answers "".
func (r *DealerConfigReader) FinanceWorldSHA256(ctx context.Context) (string, error) {
	if r == nil || r.pool == nil {
		return "", nil
	}
	var v []byte
	err := r.pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, KeyFinanceWorldSHA256).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return parseFinanceWorldSHA256(json.RawMessage(v), func() {
		r.log.Warn("settings: the recorded world sha256 is malformed; treating the box as unrecorded", "key", KeyFinanceWorldSHA256)
	}), nil
}

// WriteFinanceWorldSHA256Tx validates and upserts the recorded world digest
// on the caller's transaction: the seam the finance reset writes it through,
// so the record commits or rolls back with the reset's audit row. A blank or
// malformed digest is refused; a box the reset has never run has no row. The
// stored word is lowercase however the caller spelled the digest, so the
// recorded world is one spelling and the -once comparison is one comparison.
//
// The shape is checked before the handle, so a malformed digest refuses as
// malformed on any handle and the refusal never reads as the missing
// handle's refusal wearing the shape's name. A well shaped digest on no
// handle refuses as the missing handle it is.
func WriteFinanceWorldSHA256Tx(ctx context.Context, tx DealerConfigExecer, sha string) error {
	sha = strings.ToLower(strings.TrimSpace(sha))
	if !financeWorldSHA256Shape.MatchString(sha) {
		return fmt.Errorf("settings: the world sha256 must be 64 hex characters")
	}
	if tx == nil {
		return errors.New("settings: a world sha256 write requires a database handle")
	}
	b, err := json.Marshal(sha)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ($1, $2::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
		 WHERE system_settings.value IS DISTINCT FROM EXCLUDED.value`, KeyFinanceWorldSHA256, string(b))
	return err
}
