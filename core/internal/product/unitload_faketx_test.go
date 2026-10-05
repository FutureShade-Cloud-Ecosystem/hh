package product

// unitload_faketx_test.go drives the unit writer's insert arm and the
// set record's row shapes without a database (HH-LOAD-1 LD-3 round 2).
// The fake transaction answers the writer's two reads the concurrent
// insert race answers them for real: the FOR UPDATE row read finds
// nothing, and by the time the insert runs another load's row is
// standing, so the RETURNING answers the stored row's code or affects
// zero rows. The records are invented. Pure tests: no database, no pool.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeInsertArmTx answers the unit writer's reads: the row read (the
// FOR UPDATE select) always finds nothing, and the insert's RETURNING
// answers with the stored code when one is planted and with ErrNoRows
// when the statement affects zero rows. Any other call is a test fault.
type fakeInsertArmTx struct {
	storedCode string
	sawSQL     []string
}

func (t *fakeInsertArmTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	t.sawSQL = append(t.sawSQL, sql)
	if !strings.Contains(sql, "INSERT") {
		return fakeRow{err: pgx.ErrNoRows}
	}
	if t.storedCode == "" {
		return fakeRow{err: pgx.ErrNoRows}
	}
	return fakeRow{code: t.storedCode}
}

// fakeRow is one pgx.Row answer.
type fakeRow struct {
	code string
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 1 {
		if p, ok := dest[0].(*string); ok {
			*p = r.code
			return nil
		}
	}
	return errors.New("fake row: unexpected scan destination")
}

func (t *fakeInsertArmTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("fake tx: Begin is not part of this test")
}
func (t *fakeInsertArmTx) Commit(context.Context) error {
	return errors.New("fake tx: Commit is not part of this test")
}
func (t *fakeInsertArmTx) Rollback(context.Context) error {
	return errors.New("fake tx: Rollback is not part of this test")
}
func (t *fakeInsertArmTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("fake tx: CopyFrom is not part of this test")
}
func (t *fakeInsertArmTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}
func (t *fakeInsertArmTx) LargeObjects() pgx.LargeObjects {
	panic("fake tx: LargeObjects is not part of this test")
}
func (t *fakeInsertArmTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("fake tx: Prepare is not part of this test")
}
func (t *fakeInsertArmTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (t *fakeInsertArmTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("fake tx: Query is not part of this test")
}
func (t *fakeInsertArmTx) Conn() *pgx.Conn { return nil }

// TestUnitLoadInsertArmAnswersTheStoredRowsID: the insert arm's answer
// id comes from the row's stored code (RETURNING), never from the
// request's spelling. A concurrent insert of a case variant lands
// between this transaction's read and its insert, and the row the
// catalogue carries spells the code its own way; the registry must name
// that row once, under its own spelling's derived id.
func TestUnitLoadInsertArmAnswersTheStoredRowsID(t *testing.T) {
	tx := &fakeInsertArmTx{storedCode: "FXU-STORED"}
	id, attached, err := UnitLoadWriter{}.ApplyAttached(context.Background(), tx, load.Write{
		OrgID:    "4542a800-0000-4000-8000-00000000d101",
		LegacyID: "UU-STORE",
		Record:   map[string]any{"code": "fxu-stored", "name": "Fixture Stored Unit", "dimension": "count"},
	})
	if err != nil {
		t.Fatalf("insert arm: %v", err)
	}
	if attached {
		t.Fatalf("the insert arm answered attached")
	}
	if want := UnitRowID("FXU-STORED"); id != want {
		t.Fatalf("the insert arm answered %s, want the stored row's derived id %s", id, want)
	}
}

// TestUnitLoadInsertArmRefusesWhenTheConflictFitsNobody: the insert's
// ON CONFLICT DO UPDATE carries the org guard, and when the concurrent
// row belongs to another org the statement affects zero rows. The arm
// refuses as a key conflict then, never answering created over a row
// this transaction never saw.
func TestUnitLoadInsertArmRefusesWhenTheConflictFitsNobody(t *testing.T) {
	tx := &fakeInsertArmTx{}
	id, attached, err := UnitLoadWriter{}.ApplyAttached(context.Background(), tx, load.Write{
		OrgID:    "4542a800-0000-4000-8000-00000000d101",
		LegacyID: "UU-RACE",
		Record:   map[string]any{"code": "FXU-RACE", "name": "Fixture Race Unit", "dimension": "count"},
	})
	if err == nil {
		t.Fatalf("the zero rows insert answered id %q attached %v: the arm must refuse the conflict", id, attached)
	}
	var r *load.Refusal
	if !asRefusal(err, &r) {
		t.Fatalf("the zero rows refusal is not a *load.Refusal: %v", err)
	}
	if r.Code != load.ReasonKeyConflict {
		t.Fatalf("the refusal code is %q, want key_conflict", r.Code)
	}
	if r.Field != "code" {
		t.Fatalf("the refusal names field %q, want code", r.Field)
	}
}

// TestSetRowsReadsTheFactorAsDecimalTextOnly: a factor that arrives as
// a JSON number (or any numeric Go type a caller carries) is refused
// naming factor_type, never re-spelled into text and never answered
// with a value.
func TestSetRowsReadsTheFactorAsDecimalTextOnly(t *testing.T) {
	numbers := []any{
		float64(12345678901.12345678), float64(30), float32(1.5),
		int(2), int8(2), int16(2), int32(2), int64(2),
		uint(2), uint8(2), uint16(2), uint32(2), uint64(2),
		json.Number("12345678901.12345678"),
	}
	for _, n := range numbers {
		_, refused := setRows(load.Write{Record: map[string]any{"rows": []any{
			map[string]any{"code": "SF", "qty_in_base": n, "is_base": true},
		}}})
		if refused == nil {
			t.Fatalf("a %T factor passed the rows read: the door must refuse a numeric factor, never re-spell it", n)
		}
		if refused.Code != "factor_type" || refused.Field != "factor_type" {
			t.Fatalf("the %T factor refused as %q on %q, want factor_type on factor_type", n, refused.Code, refused.Field)
		}
		if containsAnyDigit(refused.Detail) {
			t.Fatalf("the %T factor refusal carries a value: %q", n, refused.Detail)
		}
	}
	// The text form still lands.
	rows, refused := setRows(load.Write{Record: map[string]any{"rows": []any{
		map[string]any{"code": "SF", "qty_in_base": "1", "is_base": true},
	}}})
	if refused != nil || len(rows) != 1 || rows[0].qty != "1" {
		t.Fatalf("the text factor refused or misread: %+v %v", rows, refused)
	}
}

// TestSetRowsReadsRoleHintsAsTextSlices: a Go caller carries role hints
// as []string and the wire carries []any; both land.
func TestSetRowsReadsRoleHintsAsTextSlices(t *testing.T) {
	asTextSlice, refused := setRows(load.Write{Record: map[string]any{"rows": []any{
		map[string]any{"code": "PLT", "qty_in_base": "60", "role_hints": []string{"sell", "buy"}},
	}}})
	if refused != nil {
		t.Fatalf("the []string hints refused: %v", refused)
	}
	if got := asTextSlice[0].roleHints; len(got) != 2 || got[0] != "sell" || got[1] != "buy" {
		t.Fatalf("the []string hints read as %v, want sell and buy", got)
	}
	asAnySlice, refused := setRows(load.Write{Record: map[string]any{"rows": []any{
		map[string]any{"code": "PLT", "qty_in_base": "60", "role_hints": []any{"sell", "buy"}},
	}}})
	if refused != nil {
		t.Fatalf("the []any hints refused: %v", refused)
	}
	if got := asAnySlice[0].roleHints; len(got) != 2 || got[0] != "sell" || got[1] != "buy" {
		t.Fatalf("the []any hints read as %v, want sell and buy", got)
	}
}

// containsAnyDigit reports text carrying a digit.
func containsAnyDigit(s string) bool {
	for _, c := range s {
		if c >= '0' && c <= '9' {
			return true
		}
	}
	return false
}
