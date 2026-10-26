package order

// reapplied_seam.go, SF-2r round 2: the order's activity shows the money a reversal held and a manager put on one of the
// order's invoices. Re-application writes payment_reapplications and no payment_allocations row, so the "applied a
// payment" rows of the stream, which read allocations, omit it. The payments module owns the table; this file reads it
// only through that module's own read (the billing register's seam is the precedent).

import (
	"context"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// ReappliedSlice is one slice applied to an invoice of the order.
type ReappliedSlice struct {
	ID          string
	PaymentNo   string
	Method      string
	InvoiceNo   string
	AmountCents int64
	AppliedAt   time.Time
	ActorID     string
	ActorName   string
}

// ReappliedReader is the payments module's read of the slices applied to an order's invoices.
type ReappliedReader interface {
	ReappliedToOrder(ctx context.Context, orderID string) ([]ReappliedSlice, error)
}

// PaymentsReappliedReader adapts *payments.Repository onto ReappliedReader.
type PaymentsReappliedReader struct{ repo *payments.Repository }

// NewPaymentsReappliedReader binds the order's activity to the payments module's re-application read.
func NewPaymentsReappliedReader(repo *payments.Repository) *PaymentsReappliedReader {
	return &PaymentsReappliedReader{repo: repo}
}

// ReappliedToOrder translates the payments module's answer onto the order's own type.
func (w *PaymentsReappliedReader) ReappliedToOrder(ctx context.Context, orderID string) ([]ReappliedSlice, error) {
	rows, err := w.repo.ReappliedToOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	out := make([]ReappliedSlice, 0, len(rows))
	for _, r := range rows {
		out = append(out, ReappliedSlice{ID: r.ID, PaymentNo: r.PaymentNo, Method: r.Method, InvoiceNo: r.InvoiceNo,
			AmountCents: r.AmountCents, AppliedAt: r.AppliedAt, ActorID: r.AppliedBy, ActorName: r.AppliedName})
	}
	return out, nil
}

// WithReapplied binds the re-applied slices read. Nil leaves the stream reading allocations alone.
func (s *Service) WithReapplied(r ReappliedReader) *Service { s.reapplied = r; return s }
