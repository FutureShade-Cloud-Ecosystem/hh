package settings

// dealer_config.go, the dealer's own configuration: the one seam every module reads the dealer's
// currency, country, tax jurisdiction and business day zone through. The five keys live in system_settings
// (migration 0014 planted currency, tax_region and hst_rate_bps; business_zone and country are absent
// until a dealer sets them). settings owns that table, so settings owns the read and the write, and a
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The five system_settings keys the dealer's configuration reads.
const (
	KeyCurrency     = "currency"
	KeyTaxRegion    = "tax_region"
	KeyBusinessZone = "business_zone"
	KeyHSTRateBps   = "hst_rate_bps"
	KeyCountry      = "country"
)

// KeyLegacyImportSource is the optional system_settings key naming the legacy system a dealer imports
// from (a short lowercase word such as the first dealer's). It is not part of DealerConfig: absent or
// blank means the dealer has none, there is no default, and the dealer configuration's five key write
// never creates it. No migration plants it; it is set for the dealers that have one.
const KeyLegacyImportSource = "legacy_import_source"

// The Ontario defaults an absent key answers.
const (
	DefaultCurrency     = "CAD"
	DefaultTaxRegion    = "ON"
	DefaultBusinessZone = "America/Toronto"
	DefaultHSTRateBps   = 1300
	DefaultCountry      = "CA"
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
	// Country is the ISO 3166-1 alpha-2 country of the dealer's addresses ("CA" or "US"): the
	// address rules and the words that fit it. TaxRegion is the tax jurisdiction inside it.
	Country string
}

// DefaultDealerConfig is the Ontario dealer: what a dealer with no configuration rows is.
func DefaultDealerConfig() DealerConfig {
	return DealerConfig{
		Currency:     DefaultCurrency,
		TaxRegion:    DefaultTaxRegion,
		BusinessZone: DefaultBusinessZone,
		HSTRateBps:   DefaultHSTRateBps,
		Country:      DefaultCountry,
	}
}

var (
	currencyShape = regexp.MustCompile(`^[A-Z]{3}$`)
	regionShape   = regexp.MustCompile(`^[A-Z][A-Z0-9_-]{0,15}$`)
	countryShape  = regexp.MustCompile(`^[A-Z]{2}$`)
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
	if !countryShape.MatchString(c.Country) {
		return fmt.Errorf("settings: country must be a two letter uppercase ISO 3166-1 code")
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
	if s, ok := str(KeyCountry); ok {
		if countryShape.MatchString(s) {
			cfg.Country = s
		} else {
			flag(KeyCountry)
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

// DealerConfigQuerier is the one read capability the dealer configuration needs: pgxpool.Pool and
// pgx.Tx both satisfy it, so the same read runs on the pool or on a caller's open transaction.
type DealerConfigQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// DealerConfig reads the five keys in one query, uncached. A malformed stored value falls back to
// its key's default and is logged by key, never by value.
func (r *DealerConfigReader) DealerConfig(ctx context.Context) (DealerConfig, error) {
	if r == nil || r.pool == nil {
		return DefaultDealerConfig(), nil
	}
	return r.read(ctx, r.pool)
}

// DealerConfigTx is DealerConfig on the caller's open transaction. A caller that holds a connection
// for the length of its work (the card rail's sale transaction) reads through this and never the
// pool: a pool read there makes the holder wait for a second connection, which a full pool of such
// holders never frees (the payment store's activeConfigTx discipline). A nil reader or pool still
// answers the Ontario default.
func (r *DealerConfigReader) DealerConfigTx(ctx context.Context, tx DealerConfigQuerier) (DealerConfig, error) {
	if r == nil || r.pool == nil || tx == nil {
		return DefaultDealerConfig(), nil
	}
	return r.read(ctx, tx)
}

// BusinessZone is the dealer's business day zone alone, uncached, for a surface that serves it
// (the settings kit's config read). An absent or malformed stored zone answers the Ontario default.
func (r *DealerConfigReader) BusinessZone(ctx context.Context) (string, error) {
	c, err := r.DealerConfig(ctx)
	if err != nil {
		return "", err
	}
	return c.BusinessZone, nil
}

var legacyImportShape = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// parseLegacyImportSource answers the stored source word, or "" for an absent, blank or malformed
// value. bad, when set, is told of a malformed value so the reader can log the key.
func parseLegacyImportSource(raw json.RawMessage, bad func()) string {
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
	if !legacyImportShape.MatchString(s) {
		if bad != nil {
			bad()
		}
		return ""
	}
	return s
}

// LegacyImportSource is the dealer's legacy import source, uncached: "" when the key is absent, blank
// or malformed (a malformed value is logged by key, never by value). A nil reader or pool answers "".
func (r *DealerConfigReader) LegacyImportSource(ctx context.Context) (string, error) {
	if r == nil || r.pool == nil {
		return "", nil
	}
	var v []byte
	err := r.pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, KeyLegacyImportSource).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return parseLegacyImportSource(json.RawMessage(v), func() {
		r.log.Warn("settings: the legacy import source is malformed; treating the dealer as having none", "key", KeyLegacyImportSource)
	}), nil
}

// WriteLegacyImportSourceTx validates and upserts the legacy import source on the caller's
// transaction: the seam a dealer's configuration (and a test fixture) writes it through. A blank
// source is refused; a dealer with none has no row.
func WriteLegacyImportSourceTx(ctx context.Context, tx DealerConfigExecer, source string) error {
	if !legacyImportShape.MatchString(source) {
		return fmt.Errorf("settings: legacy import source must be a short lowercase word")
	}
	b, err := json.Marshal(source)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ($1, $2::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
		 WHERE system_settings.value IS DISTINCT FROM EXCLUDED.value`, KeyLegacyImportSource, string(b))
	return err
}

func (r *DealerConfigReader) read(ctx context.Context, q DealerConfigQuerier) (DealerConfig, error) {
	rows, err := q.Query(ctx,
		`SELECT key::text, value FROM system_settings WHERE key IN ('currency','tax_region','business_zone','hst_rate_bps','country')`)
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
	return dealerConfigFromRows(raw, func(key string) {
		r.log.Warn("settings: a dealer configuration value is malformed; using the default for the key", "key", key)
	})
}

// ErrMalformedTaxRegion refuses a stored tax region that is present but unusable. Every other key
// falls back to its own default; the tax region does not, because the fallback is Ontario's 13
// percent and a dealer whose row is damaged must not ring it in silence. An ABSENT region is still
// the Ontario dealer.
var ErrMalformedTaxRegion = errors.New("settings: the stored tax region is malformed; refusing to assume Ontario")

// dealerConfigFromRows parses the rows with per-key fallbacks and refuses a malformed tax region.
func dealerConfigFromRows(rows map[string]json.RawMessage, bad func(key string)) (DealerConfig, error) {
	regionBad := false
	cfg := parseDealerConfig(rows, func(key string) {
		if key == KeyTaxRegion {
			regionBad = true
		}
		if bad != nil {
			bad(key)
		}
	})
	if regionBad {
		return DealerConfig{}, ErrMalformedTaxRegion
	}
	return cfg, nil
}

// DealerConfigExecer is the one write capability WriteDealerConfigTx needs: pgx.Tx and the demo
// build's apply transaction both satisfy it.
type DealerConfigExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// WriteDealerConfigTx validates and upserts the five keys on the caller's transaction, the seam the
// demo build's first build writes the dealer through so the configuration commits or rolls back with
// the rest of the build.
func WriteDealerConfigTx(ctx context.Context, tx DealerConfigExecer, c DealerConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	for _, kv := range []struct {
		key string
		val any
	}{
		{KeyCurrency, c.Currency},
		{KeyTaxRegion, c.TaxRegion},
		{KeyBusinessZone, c.BusinessZone},
		{KeyHSTRateBps, c.HSTRateBps},
		{KeyCountry, c.Country},
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
	return nil
}

// WriteDealerCurrencyTx validates and upserts the currency key alone on the caller's transaction. The
// demo populate writes the currency with its card rail row, so the world it builds is internally
// consistent before the build's seed writes the whole configuration; it must not write a jurisdiction
// of its own, which would need rate rows it does not own.
func WriteDealerCurrencyTx(ctx context.Context, tx DealerConfigExecer, currency string) error {
	if !currencyShape.MatchString(currency) {
		return fmt.Errorf("settings: currency must be a three letter uppercase ISO 4217 code")
	}
	b, err := json.Marshal(currency)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ($1, $2::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
		 WHERE system_settings.value IS DISTINCT FROM EXCLUDED.value`, KeyCurrency, string(b))
	return err
}

// WriteDealerConfig validates and upserts the five keys in one transaction: the seam a dealer admin
// surface writes through.
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
	if err := WriteDealerConfigTx(ctx, tx, c); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
