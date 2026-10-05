// unitload.go is the product module's write path for the load doors'
// unit items (HH-LOAD-1 LD-3). The uoms table is this module's (0002,
// the deployment wide vocabulary of the plan's reading 3: one dealer per
// deployment and database, the catalogue keyed by code, no org
// dimension), so the writer lives here and the load frame hands it each
// item inside its own batch transaction, taken by constructor at the
// wiring.
//
// The unit's HH identity is its code: the table's primary key is the
// code, every reference in the core (products.base_uom and price_uom,
// product_uoms.uom_code, the pricing and stock columns) names the code,
// and the table carries no uuid column. The registry's hh_id for a unit
// is therefore the unit's derived row id (UnitRowID) over the code the
// ROW carries: a v5 style uuid derived from the stored code read back
// from the table, stable across deployments, one per row, so case
// variants of one code bind one name or refuse as a conflict, and the
// registry's two way uniqueness holds (a second legacy key naming the
// same code is refused as a key conflict). This reading is recorded in
// the item report.
//
// The attach rule (the plan's section 2): a unit that already exists
// with the same code and dimension is attached, never created twice; a
// code clash of another dimension is refused naming the field and the
// legacy key, never a value; a row another org owns refuses as
// org_mismatch. The writer reports the attach fact through the frame's
// AttachReporter seam so the create arm answers created or attached per
// item.
//
// The writer's rules, binding: it opens no connection of its own, it
// never commits or rolls back the transaction it is given, and its
// errors wrap the statement's own (%w) so a Postgres deadlock stays
// recognisable at the batch boundary. Its refusals come back as
// *load.Refusal and carry codes and fields, never a record value.
package product

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
)

// unitDimensions is the dimension vocabulary the uoms check carries
// (0002). The door refuses a record whose dimension is outside it,
// naming the field; the check widening of the plan's section 6 (a
// strict superset, migration 0323) was considered and not needed: no
// source unit type the analysis records maps outside these five.
var unitDimensions = map[string]bool{
	"count": true, "area": true, "weight": true, "volume": true, "length": true,
}

// maxUnitCodeLength bounds a unit code: the catalogue's codes are short
// words, and the bound keeps a stray record from landing a column long
// token. Over the bound refuses as a record shape, naming the field.
const maxUnitCodeLength = 64

// UnitLoadWriter is the load doors' unit writer, the one write path a
// load batch's unit items run through.
type UnitLoadWriter struct{}

// Apply answers one unit item through the plain Writer seam; the
// attach fact is not reported here (ApplyAttached is the door's entry).
func (w UnitLoadWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	id, _, err := w.ApplyAttached(ctx, tx, in)
	return id, err
}

// ApplyAttached answers one unit item: the record names the code, the
// name and the dimension. A code the catalogue does not carry inserts a
// new unit (attached false); the same code and dimension already
// standing attaches to that row and stamps it the load's own (attached
// true); the same code under another dimension refuses naming the
// field; a row another org owns refuses naming the kind and the key.
// Either arm answers the row's stored code read back from the table:
// the catalogue's code is CITEXT, the row spells the code its own way,
// and the registry's id names the row, never the request's spelling of
// it (two spellings of one code are one row, one registry name).
func (w UnitLoadWriter) ApplyAttached(ctx context.Context, tx pgx.Tx, in load.Write) (string, bool, error) {
	code, _ := in.Record["code"].(string)
	name, _ := in.Record["name"].(string)
	dimension, _ := in.Record["dimension"].(string)
	if code == "" {
		return "", false, &load.Refusal{Code: "field_required", Field: "code",
			Detail: "the record names no unit code"}
	}
	if len(code) > maxUnitCodeLength {
		return "", false, &load.Refusal{Code: "record_shape", Field: "code",
			Detail: "the unit code is over the bound the catalogue carries"}
	}
	if name == "" {
		return "", false, &load.Refusal{Code: "field_required", Field: "name",
			Detail: "the record names no unit name"}
	}
	if dimension == "" {
		return "", false, &load.Refusal{Code: "field_required", Field: "dimension",
			Detail: "the record names no dimension"}
	}
	if !unitDimensions[dimension] {
		return "", false, &load.Refusal{Code: "dimension_unknown", Field: "dimension",
			Detail: "the record names a dimension the catalogue does not carry"}
	}
	// The row the code names, under the row lock so two loads on one
	// code serialise here before the registry's name lock. The stored
	// spelling rides along: the answer names the row by the code the
	// catalogue carries, whatever spelling arrived.
	var storedCode, existingDimension string
	var existingOrg *string
	err := tx.QueryRow(ctx,
		`SELECT code::text, dimension, org_id::text FROM uoms WHERE code = $1 FOR UPDATE`, code).
		Scan(&storedCode, &existingDimension, &existingOrg)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// RETURNING code: the row's stored spelling answers the id, so a
		// concurrent insert this read missed (the insert converges on its
		// row) still names one row once, under the catalogue's spelling.
		var rowCode string
		err := tx.QueryRow(ctx, `
			INSERT INTO uoms (code, name, dimension, org_id, idempotency_key)
			VALUES ($1, $2, $3, $4::uuid, NULLIF($5, '')::uuid)
			ON CONFLICT (code) DO UPDATE SET
				name = EXCLUDED.name,
				org_id = EXCLUDED.org_id,
				idempotency_key = EXCLUDED.idempotency_key,
				updated_at = now()
			WHERE uoms.org_id IS NULL OR uoms.org_id = EXCLUDED.org_id
			RETURNING code::text`,
			code, name, dimension, in.OrgID, in.DerivedKey).Scan(&rowCode)
		if errors.Is(err, pgx.ErrNoRows) {
			// The statement's org guard fit nobody: the concurrent row
			// belongs to another org. Refuse the claim rather than answer
			// created over a row this transaction never saw.
			return "", false, &load.Refusal{Code: load.ReasonKeyConflict, Field: "code",
				Detail: fmt.Sprintf("unit legacy key %s landed on a code a concurrent load inserted first, and that row's org is not this load's", in.LegacyID)}
		}
		if err != nil {
			return "", false, fmt.Errorf("product: unit load write: %w", err)
		}
		return UnitRowID(rowCode), false, nil
	case err != nil:
		return "", false, fmt.Errorf("product: unit row read: %w", err)
	case existingOrg != nil && *existingOrg != in.OrgID:
		return "", false, &load.Refusal{Code: "org_mismatch", Field: "legacy_id",
			Detail: fmt.Sprintf("unit legacy key %s names a row another org owns", in.LegacyID)}
	case existingDimension != dimension:
		return "", false, &load.Refusal{Code: "dimension_clash", Field: "dimension",
			Detail: fmt.Sprintf("unit legacy key %s names a code the catalogue already carries under another dimension", in.LegacyID)}
	default:
		if _, err := tx.Exec(ctx, `
			UPDATE uoms SET name = $2, org_id = $3::uuid, idempotency_key = NULLIF($4, '')::uuid, updated_at = now()
			WHERE code = $1`, code, name, in.OrgID, in.DerivedKey); err != nil {
			return "", false, fmt.Errorf("product: unit load update: %w", err)
		}
		return UnitRowID(storedCode), true, nil
	}
}

// unitRowNamespace is the v5 derivation's namespace: the uuid the
// string names hashes to, fixed so the same code derives the same row
// id on every deployment.
const unitRowNamespace = "hh:load:unit:v1"

// UnitRowID derives the unit row's registry id from its code: a v5
// style uuid (sha1 over the namespace and the code, version and variant
// bits set), stable across deployments, one per code. The uoms table
// keys its rows by code and carries no uuid column, so this derived id
// is what the registry's hh_id names; the reading is recorded in the
// item report.
func UnitRowID(code string) string {
	h := sha1.New()
	h.Write([]byte(unitRowNamespace))
	h.Write([]byte{0})
	h.Write([]byte(code))
	sum := h.Sum(nil)
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(sum[:16])
	return strings.Join([]string{
		hexed[0:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:32],
	}, "-")
}

// unitFactor is one parsed conversion factor: the exact decimal as a
// rational, plus the text's shape facts the checks read.
type unitFactor struct {
	value *big.Rat
}

// positive reports a factor above zero.
func (f unitFactor) positive() bool {
	return f.value.Sign() > 0
}

// parseUnitFactor is the factor gate the unit set door runs on every
// conversion (the plan's section 8 point 3): strict decimal text (no
// exponent, no separators), positive, minimal scale eight or under, and
// within DECIMAL(19,8)'s width (eleven integer digits beside the eight
// decimals). A factor beyond the scale refuses naming the field, never
// a value, and is never rounded.
func parseUnitFactor(text string) (unitFactor, *load.Refusal) {
	f, refused := parseDecimalText(text)
	if refused != nil {
		return unitFactor{}, refused
	}
	return f, nil
}

// parseDecimalText parses strict decimal text into an exact rational
// with the column checks: the shape refusal names record_shape, the
// scale and width refusals name factor_scale, the sign refusal names
// factor_negative, all on qty_in_base.
func parseDecimalText(text string) (unitFactor, *load.Refusal) {
	refuseShape := func() (unitFactor, *load.Refusal) {
		return unitFactor{}, &load.Refusal{Code: "record_shape", Field: "qty_in_base",
			Detail: "the factor is not a decimal the quantity columns hold"}
	}
	body := text
	neg := false
	switch {
	case strings.HasPrefix(body, "-"):
		neg = true
		body = body[1:]
	case strings.HasPrefix(body, "+"):
		body = body[1:]
	}
	intPart, fracPart, ok := strings.Cut(body, ".")
	if !ok {
		fracPart = ""
	}
	if intPart == "" && fracPart == "" {
		return refuseShape()
	}
	for _, c := range intPart + fracPart {
		if c < '0' || c > '9' {
			return refuseShape()
		}
	}
	rat := new(big.Rat)
	value := intPart + "." + fracPart
	if value == "." {
		return refuseShape()
	}
	if _, ok := rat.SetString(value); !ok {
		return refuseShape()
	}
	if neg {
		rat.Neg(rat)
	}
	if rat.Sign() <= 0 {
		return unitFactor{}, &load.Refusal{Code: "factor_negative", Field: "qty_in_base",
			Detail: "the factor is not above zero"}
	}
	if scale := minimalDecimalScale(rat); scale > 8 {
		return unitFactor{}, &load.Refusal{Code: "factor_scale", Field: "qty_in_base",
			Detail: "the factor carries a ninth place or beyond; the door refuses it rather than rounding it"}
	}
	if integerDigits(intPart) > 11 {
		return unitFactor{}, &load.Refusal{Code: "factor_scale", Field: "qty_in_base",
			Detail: "the factor is wider than the column's integer digits; the door refuses it rather than truncating it"}
	}
	return unitFactor{value: rat}, nil
}

// minimalDecimalScale answers the value's exact decimal scale: how many
// fractional digits the value needs, trailing zeros not counted.
func minimalDecimalScale(rat *big.Rat) int {
	den := new(big.Int).Set(rat.Denom())
	twos, fives := 0, 0
	for d := new(big.Int); den.Bit(0) == 0; twos++ {
		d.Rsh(den, 1)
		den.Set(d)
	}
	five := big.NewInt(5)
	for {
		q, rem := new(big.Int).QuoRem(den, five, new(big.Int))
		if rem.Sign() != 0 {
			break
		}
		den.Set(q)
		fives++
	}
	if den.Cmp(big.NewInt(1)) != 0 {
		// The value is not a terminating decimal at all; the text shape
		// check above already refused such spellings, so this is a guard.
		return 1 << 30
	}
	if twos > fives {
		return twos
	}
	return fives
}

// integerDigits counts the digits left when leading zeros are gone.
func integerDigits(intPart string) int {
	trimmed := strings.TrimLeft(intPart, "0")
	return len(trimmed)
}

// decimalScale is the test-facing scale ruler: the value's exact
// decimal scale of the text, trailing zeros not counted, and -1 for
// text that names no decimal.
func decimalScale(text string) int {
	rat, ok := new(big.Rat).SetString(text)
	if !ok {
		return -1
	}
	return minimalDecimalScale(rat)
}

// decimalRoundTrip is the plan's round trip check on every conversion,
// read against the representation the price resolver actually reads
// (repository.go ConvertUOM parses the stored column with
// strconv.ParseFloat): the factor spelled at the base column's scale
// eight, read as a float64 and spelled back at scale eight, must be the
// stored text again. A factor of more than fifteen significant digits
// passes the scale gate and the column would hold it exactly, and the
// float read reads it back as another number; the door refuses it
// rather than storing a factor the resolver reads differently (the
// plan's risk 1, stated honestly: factors are exact only to what the
// resolver reads). The refusal names the field and carries no value.
func decimalRoundTrip(factor string) *load.Refusal {
	f, refused := parseUnitFactor(factor)
	if refused != nil {
		return refused
	}
	// The stored text: the value at the base column's scale, spelled
	// exactly (the gate above bound the scale to eight, so the spelling
	// rounds nothing).
	stored := f.value.FloatString(8)
	parsed, err := strconv.ParseFloat(stored, 64)
	if err != nil {
		return &load.Refusal{Code: "round_trip", Field: "qty_in_base",
			Detail: "the factor does not survive the float read the price resolver makes"}
	}
	if back := strconv.FormatFloat(parsed, 'f', 8, 64); back != stored {
		return &load.Refusal{Code: "round_trip", Field: "qty_in_base",
			Detail: "the factor does not survive the float read the price resolver makes; the door refuses it rather than storing a factor the resolver reads back differently"}
	}
	return nil
}
