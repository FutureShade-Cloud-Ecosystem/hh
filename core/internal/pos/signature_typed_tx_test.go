// signature_typed_tx_test.go, the unit half of the tx-riding capture: what can be proven without a
// database is that BOTH capture paths refuse a given attestation IDENTICALLY (one shared gate), and
// what the tx capture demands of its wiring and of the audit write. The database half, the rows
// actually riding the caller's transaction and rolling back with it, is the dispatch legs
// (syncscope_pickups_sigcapture_tx_*_integration_test.go) against the real repository and audit
// service.
package pos

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// captureTx is a transaction handle the unit tests can pass: the tx capture's insert scans one
// string column, which the fake row answers without a database.
type captureTx struct{ pgx.Tx }

func (captureTx) QueryRow(context.Context, string, ...any) pgx.Row { return captureRow{} }

type captureRow struct{ pgx.Row }

func (captureRow) Scan(dest ...any) error {
	p, ok := dest[0].(*string)
	if !ok {
		return errors.New("captureRow: unexpected scan destination")
	}
	*p = "sig-row-1"
	return nil
}

// txCaptureStore opens the bound-store gate; the tx path's insert goes through the store seam now
// (Repository.InsertSignatureTx's fake twin answers it without a database), so only that method is
// overridden and every other posStore call would land on the embedded interface.
type txCaptureStore struct{ posStore }

func (s txCaptureStore) InsertSignatureTx(context.Context, pgx.Tx, signatureRow) (string, error) {
	return "sig-row-1", nil
}

// poolCaptureStore takes the pool path's insert without a database.
type poolCaptureStore struct {
	posStore
	inserted int
}

func (s *poolCaptureStore) InsertSignature(context.Context, signatureRow) (string, error) {
	s.inserted++
	return "sig-row-2", nil
}

// poolOnlyAudit is a recorder wired before the tx seam existed: Record only, no RecordTx.
type poolOnlyAudit struct {
	records int
}

func (a *poolOnlyAudit) Record(context.Context, audit.SensitiveAccess) error { a.records++; return nil }

// failingTxAudit can ride the transaction and fails when it does.
type failingTxAudit struct{}

func (failingTxAudit) Record(context.Context, audit.SensitiveAccess) error { return nil }
func (failingTxAudit) RecordTx(context.Context, audit.Execer, audit.SensitiveAccess) error {
	return errors.New("audit: the tx write failed")
}

// recordingTxAudit captures what the tx capture wrote and on what.
type recordingTxAudit struct {
	txWrites int
	entry    audit.SensitiveAccess
}

func (*recordingTxAudit) Record(context.Context, audit.SensitiveAccess) error { return nil }
func (r *recordingTxAudit) RecordTx(_ context.Context, _ audit.Execer, a audit.SensitiveAccess) error {
	r.txWrites++
	r.entry = a
	return nil
}

func txTestAttestation() TypedAttestation {
	return TypedAttestation{
		Purpose: "PROOF_OF_DELIVERY", RefType: "will_call_pickup",
		RefID:     "4b0e0000-0000-4000-8000-00000000c001",
		TypedName: "Leg Actor", Affirmed: true,
	}
}

func TestCaptureTypedRefusalsIdenticalOnBothPaths(t *testing.T) {
	p := &TypedNameSignatureProvider{store: txCaptureStore{}, audit: &recordingTxAudit{}}
	tooLong := strings.Repeat("n", maxTypedNameLen+1)
	cases := map[string]TypedAttestation{
		"blank name":        {Purpose: "PROOF_OF_DELIVERY", RefType: "will_call_pickup", RefID: "4b0e0000-0000-4000-8000-00000000c001", TypedName: "   ", Affirmed: true},
		"absent name":       {Purpose: "PROOF_OF_DELIVERY", RefType: "will_call_pickup", RefID: "4b0e0000-0000-4000-8000-00000000c001", Affirmed: true},
		"name too long":     {Purpose: "PROOF_OF_DELIVERY", RefType: "will_call_pickup", RefID: "4b0e0000-0000-4000-8000-00000000c001", TypedName: tooLong, Affirmed: true},
		"no affirmative":    {Purpose: "PROOF_OF_DELIVERY", RefType: "will_call_pickup", RefID: "4b0e0000-0000-4000-8000-00000000c001", TypedName: "Leg Actor"},
		"unknown purpose":   {Purpose: "SIGN_THE_THING", RefType: "will_call_pickup", RefID: "4b0e0000-0000-4000-8000-00000000c001", TypedName: "Leg Actor", Affirmed: true},
		"blank ref id":      {Purpose: "PROOF_OF_DELIVERY", RefType: "will_call_pickup", TypedName: "Leg Actor", Affirmed: true},
		"whitespace ref id": {Purpose: "PROOF_OF_DELIVERY", RefType: "will_call_pickup", RefID: "   ", TypedName: "Leg Actor", Affirmed: true},
	}
	for name, in := range cases {
		_, poolErr := p.CaptureTyped(context.Background(), in)
		_, txErr := p.CaptureTypedTx(context.Background(), nil, in)
		if poolErr == nil || txErr == nil {
			t.Fatalf("%s: both paths must refuse, got pool %v, tx %v", name, poolErr, txErr)
		}
		if poolErr.Error() != txErr.Error() {
			t.Fatalf("%s: the two paths must refuse identically, pool %q, tx %q", name, poolErr.Error(), txErr.Error())
		}
	}
}

func TestCaptureTypedTxWiringRefusals(t *testing.T) {
	in := txTestAttestation()
	t.Run("no transaction, no capture", func(t *testing.T) {
		p := &TypedNameSignatureProvider{store: txCaptureStore{}, audit: &recordingTxAudit{}}
		if _, err := p.CaptureTypedTx(context.Background(), nil, in); err == nil {
			t.Fatal("a tx capture without the transaction must refuse")
		}
	})
	t.Run("no bound store, no capture", func(t *testing.T) {
		p := &TypedNameSignatureProvider{audit: &recordingTxAudit{}}
		if _, err := p.CaptureTypedTx(context.Background(), captureTx{}, in); err == nil {
			t.Fatal("an unbound provider must refuse rather than answer success for a capture that persisted nothing")
		}
	})
	t.Run("a recorder that cannot ride the transaction is refused, never fallen back to the pool", func(t *testing.T) {
		rec := &poolOnlyAudit{}
		p := &TypedNameSignatureProvider{store: txCaptureStore{}, audit: rec}
		if _, err := p.CaptureTypedTx(context.Background(), captureTx{}, in); err == nil {
			t.Fatal("a pool-only recorder must refuse the tx capture")
		}
		if rec.records != 0 {
			t.Fatalf("the refusal must land before any write, the pool recorder saw %d writes", rec.records)
		}
	})
}

func TestCaptureTypedTxFailedAuditWriteFailsTheCapture(t *testing.T) {
	p := &TypedNameSignatureProvider{store: txCaptureStore{}, audit: failingTxAudit{}}
	_, err := p.CaptureTypedTx(context.Background(), captureTx{}, txTestAttestation())
	if err == nil {
		t.Fatal("a failed audit write on the transaction must fail the capture so the caller's transaction rolls back")
	}
	if err.Error() != "audit: the tx write failed" {
		t.Fatalf("the audit error must propagate unwrapped, got %q", err.Error())
	}
}

func TestCaptureTypedTxSuccessRecordsTheAuditRowOnTheTx(t *testing.T) {
	rec := &recordingTxAudit{}
	p := &TypedNameSignatureProvider{store: txCaptureStore{}, audit: rec}
	in := txTestAttestation()
	ref, err := p.CaptureTypedTx(context.Background(), captureTx{}, in)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if !strings.HasPrefix(ref, "sig_") {
		t.Fatalf("a capture returns its sig_ reference, got %q", ref)
	}
	if rec.txWrites != 1 {
		t.Fatalf("exactly one audit write on the transaction, got %d", rec.txWrites)
	}
	if rec.entry.Action != audit.ActSignatureCapture || rec.entry.EntityType != "signature" || rec.entry.EntityID != in.RefID {
		t.Fatalf("the audit row must name the signature and its subject, got %+v", rec.entry)
	}
	if rec.entry.MaskedRef != ref || rec.entry.Extra["ref_type"] != in.RefType {
		t.Fatalf("the audit row must carry the capture ref and ref_type, got %+v", rec.entry)
	}
}

func TestCaptureTypedPoolPathStaysBestEffortOnTheAuditRow(t *testing.T) {
	// A pool-only recorder whose Record FAILS: the standalone capture must still succeed, its
	// caller has no transaction to abort and this path's semantics are unchanged.
	store := &poolCaptureStore{}
	p := &TypedNameSignatureProvider{store: store, audit: failingPoolAudit{}}
	if _, err := p.CaptureTyped(context.Background(), txTestAttestation()); err != nil {
		t.Fatalf("the pool capture's audit row stays best effort, got %v", err)
	}
	if store.inserted != 1 {
		t.Fatalf("the pool capture still inserts its row through the store, got %d inserts", store.inserted)
	}
}

type failingPoolAudit struct{}

func (failingPoolAudit) Record(context.Context, audit.SensitiveAccess) error {
	return errors.New("audit: the pool write failed")
}
