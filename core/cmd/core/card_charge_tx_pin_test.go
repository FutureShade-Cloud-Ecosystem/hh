package main

// card_charge_tx_pin_test.go pins the production adapter's CAPTURE side to
// the pool path and its BIND side to the transaction path, the inverse of
// the card_refund_tx_pin_test.go mirror the old charge pin rode: the
// capture's pool shape is the seam's signature (no pgx.Tx parameter exists
// to hand it, asserted below so the seam cannot quietly regain one), and
// the bind rides the SALE's transaction over the same fake pgx.Tx shape,
// which counts the queries that rode it. The pos Sale transaction holds a
// pool connection for the whole atomic ring while the tender binds; an
// adapter whose bind reached for the pool would ask for a second connection
// the one-connection sale cannot grant, the stall the old pin kept out on
// the charge side and this pin keeps out on the bind side.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
)

// bindPinRow is every row the bind path scans: the FOR UPDATE verify read's
// four columns, in the order the adapter's SELECT names them.
type bindPinRow struct {
	status  string
	amount  int64
	session string
	legKey  string
}

func (r bindPinRow) Scan(dest ...any) error {
	vals := []any{r.status, r.amount, r.session, r.legKey}
	if len(dest) != len(vals) {
		return errors.New("bind pin: unexpected column count")
	}
	for i, d := range dest {
		switch out := d.(type) {
		case *string:
			*out = vals[i].(string)
		case *int64:
			*out = vals[i].(int64)
		default:
			return errors.New("bind pin: unsupported scan dest")
		}
	}
	return nil
}

// bindPinTx is the open sale transaction the pin rides. Embedding pgx.Tx
// satisfies the interface; QueryRow serves the verify read's row, and every
// call is counted: the verify and the guarded bind both ride the
// transaction.
type bindPinTx struct {
	pgx.Tx
	row     bindPinRow
	nextErr error
	queries []string
}

func (t *bindPinTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	t.queries = append(t.queries, sql)
	// The verify read answers its row; payment's guarded bind (the second
	// statement) answers no rows, the honest shape for a bind whose stamp
	// matched nothing over a fake that has no table behind it: the adapter
	// must then answer ErrAttemptNotCaptured, which is exactly what the pin
	// asserts the bind ran and guarded.
	if len(t.queries) > 1 {
		return errPinRow{}
	}
	return t.row
}

// errPinRow is the pgx.Row that answers ErrNoRows.
type errPinRow struct{}

func (errPinRow) Scan(_ ...any) error { return pgx.ErrNoRows }

// The bind rides the caller's transaction: the verify read and the guarded
// bind both land on the tx, none anywhere else (the store's pool is nil and
// cannot answer, so the passing answer is the pool-free proof).
func TestCardChargerAdapter_BindTx_RidesTheTransaction(t *testing.T) {
	adapter := cardChargerAdapter{svc: payment.NewService(payment.NewStore(nil, nil), payment.RunPaymentsStub{}), store: nil}
	tx := &bindPinTx{row: bindPinRow{
		status: "CAPTURED", amount: 12500, session: "sess-pin", legKey: "leg-pin",
	}}
	err := adapter.BindTx(context.Background(), tx, till.CardBind{
		AttemptID: "att-pin", PaymentID: "pay-pin", LegKey: "leg-pin",
		TillSessionID: "sess-pin", AmountCents: 12500,
	})
	if !errors.Is(err, payment.ErrAttemptNotCaptured) {
		t.Fatalf("BindTx over the fake tx = %v, want the guarded bind's ErrAttemptNotCaptured", err)
	}
	if len(tx.queries) != 2 {
		t.Fatalf("the bind must ride the transaction (the verify read, then the guarded move), got %d queries: %q", len(tx.queries), tx.queries)
	}
	for _, q := range tx.queries {
		if !strings.Contains(q, "card_charge_attempts") {
			t.Fatalf("a statement left the attempt table's path: %q", q)
		}
	}
}

// The capture stays OFF the transaction, pinned at the seam's own shape: the
// interface's Capture method has no pgx.Tx parameter to hand it (the
// compiler is the witness), and the bind's does. A seam that quietly
// regained a transaction parameter would fail here, not at the stall.
func TestCardChargerAdapter_Capture_TakesNoTransaction(t *testing.T) {
	txType := reflect.TypeOf((*pgx.Tx)(nil)).Elem()
	seam := reflect.TypeOf((*till.CardCharger)(nil)).Elem()
	cap, ok := seam.MethodByName("Capture")
	if !ok {
		t.Fatal("the till.CardCharger seam has no Capture method")
	}
	for i := 0; i < cap.Type.NumIn(); i++ {
		if cap.Type.In(i) == txType {
			t.Fatalf("Capture takes a pgx.Tx parameter (arg %d): the capture must stay off the transaction, on the pool", i)
		}
	}
	bind, ok := seam.MethodByName("BindTx")
	if !ok {
		t.Fatal("the till.CardCharger seam has no BindTx method")
	}
	hasTx := false
	for i := 0; i < bind.Type.NumIn(); i++ {
		if bind.Type.In(i) == txType {
			hasTx = true
		}
	}
	if !hasTx {
		t.Fatal("BindTx takes no pgx.Tx parameter: the bind must ride the sale's transaction")
	}
}
