// history.go, Q-044 payment attribution reads. The client-confirmed requirement (2026-07-07):
// a payment must show WHO took it and WHO applied/matched it to WHICH invoices WHEN. The till owns
// payments + payment_allocations, so the read side lives here: two staff-gated endpoints answer the
// two directions of the question -
//
//	GET /api/v1/ar/payments/{id}/history   → who took it, when, how + full application history
//	GET /api/v1/ar/invoices/{id}/payments  → which payments/credits paid this invoice, by whom, when
//
// Display names come from a LEFT JOIN to users on the 0043 allocated_by / 0010 received_by columns -
// a portal (customer-initiated) payment has no staff attribution and surfaces with empty by/name.
// Everything in this file is additive; no money write path changes.
package till

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// AuditTrailReader is the READ side of the audit seam (satisfied by *audit.Service.ReopenTransaction):
// the chronological trail for one entity. PaymentHistory uses it to answer the forensic question NSF's
// hard-delete of payment_allocations otherwise erases, "what did this bounced payment USED to pay?".
type AuditTrailReader interface {
	ReopenTransaction(ctx context.Context, entityType, entityID string) ([]audit.Entry, error)
}

// AllocationRecord is one payment→invoice application with attribution (Q-044).
type AllocationRecord struct {
	InvoiceID       string    `json:"invoice_id"`
	InvoiceNo       string    `json:"invoice_no"`
	AmountCents     int64     `json:"amount_cents"`
	AllocatedBy     string    `json:"allocated_by,omitempty"`      // users.id; "" for a portal/customer payment
	AllocatedByName string    `json:"allocated_by_name,omitempty"` // users.display_name
	AllocatedAt     time.Time `json:"allocated_at"`                // payment_allocations.created_at
	// ReversedByNSF marks a HISTORICAL allocation reconstructed from the PAYMENT_ALLOCATED audit trail
	// after an NSF reversal hard-deleted the live payment_allocations rows. Such records carry
	// invoice_id + amount from the audit Extra (invoice_no is not in the trail and stays "").
	ReversedByNSF bool `json:"reversed_by_nsf,omitempty"`
}

// PaymentHistory is one payment header (who took it, when, how) + its application history.
type PaymentHistory struct {
	PaymentID      string    `json:"payment_id"`
	PaymentNo      string    `json:"payment_no"`
	CustomerID     string    `json:"customer_id,omitempty"`
	BranchID       string    `json:"branch_id"`
	Method         string    `json:"method"`
	Status         string    `json:"status"`
	AmountCents    int64     `json:"amount_cents"`
	GatewayRef     string    `json:"gateway_ref,omitempty"`
	ReceivedAt     time.Time `json:"received_at"`
	ReceivedBy     string    `json:"received_by,omitempty"`      // WHO took the tender (users.id)
	ReceivedByName string    `json:"received_by_name,omitempty"` // display name
	// Reversed is set when the payment BOUNCED (NSF): its live allocations were reversed, and any
	// Allocations entries flagged reversed_by_nsf are historical, reconstructed from the audit trail.
	Reversed    bool               `json:"reversed,omitempty"`
	Allocations []AllocationRecord `json:"allocations"`
}

// InvoicePaymentRecord is one payment/credit applied to an invoice, with both attributions
// (who took the money, who matched it to this invoice) and both timestamps.
type InvoicePaymentRecord struct {
	PaymentID          string    `json:"payment_id"`
	PaymentNo          string    `json:"payment_no"`
	Method             string    `json:"method"`
	Status             string    `json:"status"`
	AmountCents        int64     `json:"amount_cents"`         // the slice applied to THIS invoice
	PaymentAmountCents int64     `json:"payment_amount_cents"` // the whole payment
	AllocatedBy        string    `json:"allocated_by,omitempty"`
	AllocatedByName    string    `json:"allocated_by_name,omitempty"`
	AllocatedAt        time.Time `json:"allocated_at"`
	ReceivedBy         string    `json:"received_by,omitempty"`
	ReceivedByName     string    `json:"received_by_name,omitempty"`
	ReceivedAt         time.Time `json:"received_at"`
}

// InvoicePayments is the per-invoice settlement view: every payment applied to it, attributed.
type InvoicePayments struct {
	InvoiceID string                 `json:"invoice_id"`
	InvoiceNo string                 `json:"invoice_no"`
	BranchID  string                 `json:"branch_id"`
	Items     []InvoicePaymentRecord `json:"items"`
}

// - repository reads (pool; read-only projections over till-owned tables + users/invoices joins) -

// PaymentHistory loads one payment header + its allocations with attribution names.
func (r *Repository) PaymentHistory(ctx context.Context, paymentID string) (PaymentHistory, bool, error) {
	if r.pool == nil {
		return PaymentHistory{}, false, errNoDB
	}
	var h PaymentHistory
	// DIB-513: the key may be the payment's UUID or the payment NUMBER printed on the receipt
	// (PAY-2026-0007). `p.id = $1` alone made the number a 500, Postgres raises invalid-uuid-syntax
	// on the cast, and a clerk typing the only identifier they physically hold got "internal error"
	// for doing the obvious thing.
	//
	// The uuid arm is selected by a NULL $2 rather than a boolean, because SQL's OR does not
	// short-circuit: `($2 AND id = $1::uuid)` still casts $1 and still raises 22P02 on a number.
	// Passing the uuid itself (or NULL) means the only value ever cast to uuid is one that already
	// has a uuid's shape. An unknown key of either shape falls through to pgx.ErrNoRows and the
	// service's existing 404 refusal.
	err := r.pool.QueryRow(ctx,
		`SELECT p.id::text, p.payment_no::text, COALESCE(p.customer_id::text, ''), p.branch_id::text,
		        p.method, p.status, p.amount_cents, COALESCE(p.gateway_ref, ''), p.received_at,
		        COALESCE(p.received_by::text, ''), COALESCE(u.display_name, '')
		 FROM payments p
		 LEFT JOIN users u ON u.id = p.received_by
		 WHERE ($2::uuid IS NOT NULL AND p.id = $2::uuid)
		    OR ($2::uuid IS NULL     AND p.payment_no = $1::citext)`,
		paymentID, uuidOrNil(paymentID),
	).Scan(&h.PaymentID, &h.PaymentNo, &h.CustomerID, &h.BranchID, &h.Method, &h.Status,
		&h.AmountCents, &h.GatewayRef, &h.ReceivedAt, &h.ReceivedBy, &h.ReceivedByName)
	if errors.Is(err, pgx.ErrNoRows) {
		return PaymentHistory{}, false, nil
	}
	if err != nil {
		return PaymentHistory{}, false, fmt.Errorf("till: payment history header: %w", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT pa.invoice_id::text, COALESCE(i.invoice_no::text, ''), pa.amount_cents,
		        COALESCE(pa.allocated_by::text, ''), COALESCE(u.display_name, ''), pa.created_at
		 FROM payment_allocations pa
		 LEFT JOIN invoices i ON i.id = pa.invoice_id
		 LEFT JOIN users u ON u.id = pa.allocated_by
		 WHERE pa.payment_id = $1
		 ORDER BY pa.created_at, pa.invoice_id`, h.PaymentID)
	if err != nil {
		return PaymentHistory{}, false, fmt.Errorf("till: payment history allocations: %w", err)
	}
	defer rows.Close()
	h.Allocations = []AllocationRecord{}
	for rows.Next() {
		var a AllocationRecord
		if err := rows.Scan(&a.InvoiceID, &a.InvoiceNo, &a.AmountCents,
			&a.AllocatedBy, &a.AllocatedByName, &a.AllocatedAt); err != nil {
			return PaymentHistory{}, false, fmt.Errorf("till: scan allocation record: %w", err)
		}
		h.Allocations = append(h.Allocations, a)
	}
	if err := rows.Err(); err != nil {
		return PaymentHistory{}, false, err
	}
	return h, true, nil
}

// InvoicePayments loads every payment applied to one invoice, with attribution names.
func (r *Repository) InvoicePayments(ctx context.Context, invoiceID string) (InvoicePayments, bool, error) {
	if r.pool == nil {
		return InvoicePayments{}, false, errNoDB
	}
	var out InvoicePayments
	// DIB-513, the same rule as PaymentHistory above: an invoice is identified in the yard by its
	// NUMBER (INV-2026-0002), and that is what a clerk has in front of them.
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, invoice_no::text, branch_id::text FROM invoices
		  WHERE ($2::uuid IS NOT NULL AND id = $2::uuid)
		     OR ($2::uuid IS NULL     AND invoice_no = $1::citext)`,
		invoiceID, uuidOrNil(invoiceID),
	).Scan(&out.InvoiceID, &out.InvoiceNo, &out.BranchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvoicePayments{}, false, nil
	}
	if err != nil {
		return InvoicePayments{}, false, fmt.Errorf("till: invoice payments header: %w", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT p.id::text, p.payment_no::text, p.method, p.status, pa.amount_cents, p.amount_cents,
		        COALESCE(pa.allocated_by::text, ''), COALESCE(ua.display_name, ''), pa.created_at,
		        COALESCE(p.received_by::text, ''), COALESCE(ur.display_name, ''), p.received_at
		 FROM payment_allocations pa
		 JOIN payments p ON p.id = pa.payment_id
		 LEFT JOIN users ua ON ua.id = pa.allocated_by
		 LEFT JOIN users ur ON ur.id = p.received_by
		 WHERE pa.invoice_id = $1
		 ORDER BY pa.created_at, p.payment_no`, out.InvoiceID)
	if err != nil {
		return InvoicePayments{}, false, fmt.Errorf("till: invoice payments: %w", err)
	}
	defer rows.Close()
	out.Items = []InvoicePaymentRecord{}
	for rows.Next() {
		var it InvoicePaymentRecord
		if err := rows.Scan(&it.PaymentID, &it.PaymentNo, &it.Method, &it.Status,
			&it.AmountCents, &it.PaymentAmountCents,
			&it.AllocatedBy, &it.AllocatedByName, &it.AllocatedAt,
			&it.ReceivedBy, &it.ReceivedByName, &it.ReceivedAt); err != nil {
			return InvoicePayments{}, false, fmt.Errorf("till: scan invoice payment: %w", err)
		}
		out.Items = append(out.Items, it)
	}
	if err := rows.Err(); err != nil {
		return InvoicePayments{}, false, err
	}
	return out, true, nil
}

// - service (caller guard + branch scope, same posture as the other till reads) -

// PaymentHistory returns the attributed history of one payment (staff-gated, branch-scoped). For a
// BOUNCED payment, whose live payment_allocations the NSF reversal hard-deleted, the application
// history is reconstructed from the PAYMENT_ALLOCATED audit trail and returned flagged reversed_by_nsf,
// so "what did this cheque used to pay?" stays answerable for the forensic case.
func (s *Service) PaymentHistory(ctx context.Context, paymentID string) (PaymentHistory, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return PaymentHistory{}, err
	}
	if paymentID == "" {
		return PaymentHistory{}, unprocessable("till: payment id is required")
	}
	h, found, err := s.store.PaymentHistory(ctx, paymentID)
	if err != nil {
		return PaymentHistory{}, err
	}
	if !found {
		return PaymentHistory{}, errNotFound
	}
	if !caller.CanAccessBranch(h.BranchID) {
		return PaymentHistory{}, forbidden("till: caller cannot access branch %s", h.BranchID)
	}
	if h.Status == "BOUNCED" {
		h.Reversed = true
		if reversed, err := s.reversedAllocations(ctx, paymentID); err == nil {
			h.Allocations = append(h.Allocations, reversed...)
		}
		// A trail-read failure degrades to the live (empty) allocations rather than failing the whole
		// history read, the header facts still answer who took the payment.
	}
	return h, nil
}

// reversedAllocations reconstructs a bounced payment's historical applications from the audit trail's
// PAYMENT_ALLOCATED rows (entity = payment). invoice_id + amount come from the audit Extra; the trail
// carries no invoice_no, so that stays "" (resolvable via the invoice_id when needed).
func (s *Service) reversedAllocations(ctx context.Context, paymentID string) ([]AllocationRecord, error) {
	if s.auditTrail == nil {
		return nil, nil
	}
	entries, err := s.auditTrail.ReopenTransaction(ctx, "payment", paymentID)
	if err != nil {
		return nil, err
	}
	var out []AllocationRecord
	for _, e := range entries {
		if e.Action != audit.ActPaymentAllocated {
			continue
		}
		invoiceID, _ := e.Changes["invoice_id"].(string)
		rec := AllocationRecord{
			InvoiceID:     invoiceID,
			AmountCents:   auditAmountCents(e.Changes["amount_cents"]),
			AllocatedBy:   e.ActorID,
			ReversedByNSF: true,
		}
		if at, perr := time.Parse(time.RFC3339Nano, e.CreatedAt); perr == nil {
			rec.AllocatedAt = at
		}
		out = append(out, rec)
	}
	return out, nil
}

// uuidOrNil returns s when it has a UUID's shape and nil otherwise, so a caller can hand one
// parameter to a query that must look up by id OR by a human-readable number. nil arrives as
// NULL::uuid, which casts cleanly; a number never reaches the uuid cast at all.
func uuidOrNil(s string) *string {
	if !uuidLike(s) {
		return nil
	}
	return &s
}

// uuidLike reports whether s has the shape of a UUID (8-4-4-4-12 hex). It is deliberately a SHAPE
// test rather than a parse: its only job is to decide which arm of the lookup to take, and a value
// that looks like a UUID but is not simply finds no row, the same 404 an unknown number gets.
//
// DIB-513: without it, `WHERE id = $1` on a uuid column turns a mistyped or human-readable key into
// a Postgres syntax error, which reaches the operator as HTTP 500 "internal error", a fault report
// for a lookup that simply missed.
func uuidLike(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// auditAmountCents reads an amount_cents out of an audit changes blob: recorded in-process it is an
// int64; read back from the DB's jsonb it is a float64 (or json.Number under other decoders).
func auditAmountCents(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

// InvoicePayments returns the payments applied to one invoice (staff-gated, branch-scoped).
func (s *Service) InvoicePayments(ctx context.Context, invoiceID string) (InvoicePayments, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return InvoicePayments{}, err
	}
	if invoiceID == "" {
		return InvoicePayments{}, unprocessable("till: invoice id is required")
	}
	out, found, err := s.store.InvoicePayments(ctx, invoiceID)
	if err != nil {
		return InvoicePayments{}, err
	}
	if !found {
		return InvoicePayments{}, errNotFound
	}
	if !caller.CanAccessBranch(out.BranchID) {
		return InvoicePayments{}, forbidden("till: caller cannot access branch %s", out.BranchID)
	}
	return out, nil
}

// - HTTP (staff-gated; registered from Handler.RegisterRoutes with one line) -

// registerHistoryRoutes wires the two Q-044 attribution reads. Staff-gated: "who took this payment /
// what did it pay" is a counter-level question (unlike the manager-gated financial-distress reads).
func (h *Handler) registerHistoryRoutes(mux *http.ServeMux, g *guards.Guards) {
	mux.Handle("GET /api/v1/ar/payments/{id}/history", g.RequireStaff(h.staff(h.paymentHistory)))
	mux.Handle("GET /api/v1/ar/invoices/{id}/payments", g.RequireStaff(h.staff(h.invoicePayments)))
}

// paymentHistory answers: who took this payment, when, how, and which invoices it was applied to,
// by whom, when.
func (h *Handler) paymentHistory(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.PaymentHistory(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// invoicePayments answers: which payments/credits paid this invoice, by whom, when.
func (h *Handler) invoicePayments(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	out, err := h.svc.InvoicePayments(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
