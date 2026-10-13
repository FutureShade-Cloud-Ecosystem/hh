package settings

// dealer_config.go, the dealer's own configuration: the one seam every module reads the dealer's
// currency, tax jurisdiction and business day zone through. The four keys live in system_settings
// (migration 0014 planted currency, tax_region and hst_rate_bps; business_zone is new and absent
// until a dealer sets it). settings owns that table, so settings owns the read and the write, and a
// consumer declares a narrow interface of its own and receives an adapter over DealerConfigReader
// at the composition root; no consumer reads system_settings for these facts itself.
//
// A dealer with no rows at all is the Ontario dealer, byte for byte as before the seam existed:
// each absent key answers its own default. The reader holds no state at package level and no cache
// (a cached rate source on money is a stale rate hazard), so two readers in one process answer
// their own databases and a write is live at the next read.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The four system_settings keys the dealer's configuration reads.
const (
	KeyCurrency     = "currency"
	KeyTaxRegion    = "tax_region"
	KeyBusinessZone = "business_zone"
	KeyHSTRateBps   = "hst_rate_bps"
)

// The Ontario defaults an absent key answers.
const (
	DefaultCurrency     = "CAD"
	DefaultTaxRegion    = "ON"
	DefaultBusinessZone = "America/Toronto"
	DefaultHSTRateBps   = 1300
)

// DealerConfig is the dealer's own configuration. TaxRegion is the jurisdiction code the tax engine
// binds rates by; HSTRateBps is the flat fallback the quote and receipt readers have always used
// (the tax_rates row is the authority for the engine). BusinessZone is the IANA zone of the
// dealer's business day.
type DealerConfig struct {
	Currency     string
	TaxRegion    string
	BusinessZone string
	HSTRateBps   int
}

// DefaultDealerConfig is the Ontario dealer: what a dealer with no configuration rows is.
func DefaultDealerConfig() DealerConfig {
	return DealerConfig{
		Currency:     DefaultCurrency,
		TaxRegion:    DefaultTaxRegion,
		BusinessZone: DefaultBusinessZone,
		HSTRateBps:   DefaultHSTRateBps,
	}
}

var (
	currencyShape = regexp.MustCompile(`^[A-Z]{3}$`)
	regionShape   = regexp.MustCompile(`^[A-Z][A-Z0-9_-]{0,15}$`)
)

// Validate refuses a malformed configuration in words, before any write could reach the table. A
// currency that disagrees with the jurisdiction (USD with ON) is allowed, not guessed at.
func (c DealerConfig) Validate() error {
	if !currencyShape.MatchString(c.Currency) {
		return fmt.Errorf("settings: currency must be a three letter uppercase ISO 4217 code")
	}
	if !regionShape.MatchString(c.TaxRegion) {
		return fmt.Errorf("settings: tax region must be an uppercase jurisdiction code of up to 16 letters, digits, dashes or underscores")
	}
	if _, err := time.LoadLocation(c.BusinessZone); err != nil || c.BusinessZone == "" || c.BusinessZone == "Local" {
		return fmt.Errorf("settings: business zone must be an IANA zone name such as America/Toronto")
	}
	if c.HSTRateBps < 0 || c.HSTRateBps > 10000 {
		return fmt.Errorf("settings: hst rate must be 0..10000 basis points")
	}
	return nil
}

// parseDealerConfig builds the configuration from the raw rows, each absent or malformed key
// answering its own default. bad, when set, is told the key of every malformed value, so the reader
// can log what it could not use: a malformed stored value never reaches a consumer.
func parseDealerConfig(rows map[string]json.RawMessage, bad func(key string)) DealerConfig {
	cfg := DefaultDealerConfig()
	flag := func(key string) {
		if bad != nil {
			bad(key)
		}
	}
	str := func(key string) (string, bool) {
		v, ok := rows[key]
		if !ok {
			return "", false
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			flag(key)
			return "", false
		}
		return strings.TrimSpace(s), true
	}
	if s, ok := str(KeyCurrency); ok {
		if currencyShape.MatchString(s) {
			cfg.Currency = s
		} else {
			flag(KeyCurrency)
		}
	}
	if s, ok := str(KeyTaxRegion); ok {
		if regionShape.MatchString(s) {
			cfg.TaxRegion = s
		} else {
			flag(KeyTaxRegion)
		}
	}
	if s, ok := str(KeyBusinessZone); ok {
		if _, err := time.LoadLocation(s); err == nil && s != "" && s != "Local" {
			cfg.BusinessZone = s
		} else {
			flag(KeyBusinessZone)
		}
	}
	if v, ok := rows[KeyHSTRateBps]; ok {
		n, err := strconv.ParseInt(strings.Trim(string(v), `" `), 10, 64)
		if err != nil || n < 0 || n > 10000 {
			flag(KeyHSTRateBps)
		} else {
			cfg.HSTRateBps = int(n)
		}
	}
	return cfg
}

// DealerConfigReader reads and writes the dealer's configuration over system_settings. A nil pool
// answers the Ontario default (the scaffold and offline compositions), as the tax engine's nil
// repository does.
type DealerConfigReader struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// NewDealerConfigReader builds the reader. The pool may be nil.
func NewDealerConfigReader(pool *pgxpool.Pool) *DealerConfigReader {
	return &DealerConfigReader{pool: pool, log: slog.Default()}
}

// DealerConfig reads the four keys in one query, uncached. A malformed stored value falls back to
// its key's default and is logged by key, never by value.
func (r *DealerConfigReader) DealerConfig(ctx context.Context) (DealerConfig, error) {
	if r == nil || r.pool == nil {
		return DefaultDealerConfig(), nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT key::text, value FROM system_settings WHERE key IN ('currency','tax_region','business_zone','hst_rate_bps')`)
	if err != nil {
		return DealerConfig{}, err
	}
	defer rows.Close()
	raw := map[string]json.RawMessage{}
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return DealerConfig{}, err
		}
		raw[k] = json.RawMessage(v)
	}
	if err := rows.Err(); err != nil {
		return DealerConfig{}, err
	}
	return parseDealerConfig(raw, func(key string) {
		r.log.Warn("settings: a dealer configuration value is malformed; using the default for the key", "key", key)
	}), nil
}

// WriteDealerConfig validates and upserts the four keys in one transaction: the seam the demo
// build's first build and a dealer admin surface write through.
func (r *DealerConfigReader) WriteDealerConfig(ctx context.Context, c DealerConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if r == nil || r.pool == nil {
		return errors.New("settings: reader has no database pool")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, kv := range []struct {
		key string
		val any
	}{
		{KeyCurrency, c.Currency},
		{KeyTaxRegion, c.TaxRegion},
		{KeyBusinessZone, c.BusinessZone},
		{KeyHSTRateBps, c.HSTRateBps},
	} {
		b, err := json.Marshal(kv.val)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO system_settings (key, value) VALUES ($1, $2::jsonb)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
			 WHERE system_settings.value IS DISTINCT FROM EXCLUDED.value`, kv.key, string(b)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
