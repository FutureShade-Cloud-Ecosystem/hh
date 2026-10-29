package main

// w3rw1_dataset_sources.go, the curated data set sources (W3-RW1) the reporting module reads its
// five sets through. It is the same shape as w6bRepAssignmentReader above: interfaces DECLARED BY
// THEIR CONSUMER over providers the consumer must not import; this adapter is the one place the two
// meet, and its bodies are field copies and nothing else.
//
// WHY EVERY SET GOES THROUGH ITS OWNER. The rule Colton approved is "no free queries", and its
// positive half is that each set's facts are read by the module that owns them: the AR aging lines
// from billing's own aging (the read the month-end console renders), the AP aging items from
// purchasing's own aging (the read the payables board renders), the journal lines from gl's own
// books on the trial balance's filed-period basis, the invoice lines from billing's own documents,
// the stock on hand from inventory's own valuation. Each owner fences its read to the stamped
// caller's reach itself, so a branch scoped caller is cut at the source no matter who calls. The
// ties that stand on these reads are the reporting module's named integration tests: AR to the
// aging, AP to the aging, GL by account to the trial balance, stock value to StockValueCents.
//
import (
	"context"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/reporting"
)

// Bound in wire_tops.go beside the reporting service it feeds.
type w3rw1DatasetSources struct {
	credit   *billing.CreditService
	invoices *billing.Repository
	ap       *purchasing.Service
	gl       *gl.Service
	stock    *inventory.Service
}

// The adapter is every source the reporting service asks for, at compile time.
var (
	_ reporting.ARAgingSource   = w3rw1DatasetSources{}
	_ reporting.APAgingSource   = w3rw1DatasetSources{}
	_ reporting.GLJournalSource = w3rw1DatasetSources{}
	_ reporting.SalesSource     = w3rw1DatasetSources{}
	_ reporting.StockSource     = w3rw1DatasetSources{}
)

func (a w3rw1DatasetSources) ReadARAging(ctx context.Context, asOf time.Time) ([]reporting.ARAgingLine, error) {
	rows, err := a.credit.Aging(ctx, asOf, "")
	if err != nil {
		return nil, err
	}
	out := make([]reporting.ARAgingLine, 0, len(rows))
	for _, r := range rows {
		out = append(out, reporting.ARAgingLine{
			CustomerID:       r.CustomerID,
			AccountNo:        r.AccountNo,
			CustomerName:     r.CustomerName,
			BranchID:         r.BranchID,
			TermsDays:        r.TermsDays,
			CreditLimitCents: r.CreditLimitCents,
			CurrentCents:     r.CurrentCents,
			Days30Cents:      r.Days30Cents,
			Days60Cents:      r.Days60Cents,
			Days60To90Cents:  r.Days60To90Cents,
			Days90PlusCents:  r.Days90PlusCents,
			TotalCents:       r.TotalCents,
		})
	}
	return out, nil
}

func (a w3rw1DatasetSources) ReadAPAging(ctx context.Context, asOf time.Time) ([]reporting.APAgingLine, error) {
	raw := ""
	if !asOf.IsZero() {
		raw = asOf.Format(time.RFC3339)
	}
	page, err := a.ap.APAgingForCaller(ctx, raw, "")
	if err != nil {
		return nil, err
	}
	out := make([]reporting.APAgingLine, 0)
	for _, row := range page.Items {
		for _, it := range row.OpenItems {
			out = append(out, reporting.APAgingLine{
				VendorID:   it.VendorID,
				VendorName: it.VendorName,
				BranchID:   it.BranchID,
				Kind:       it.Kind,
				Bucket:     it.Bucket,
				RefNo:      it.RefNo,
				BillDate:   it.BillDate,
				DueDate:    it.DueDate,
				TotalCents: it.TotalCents,
				PaidCents:  it.PaidCents,
				OpenCents:  it.OpenCents,
			})
		}
	}
	return out, nil
}

func (a w3rw1DatasetSources) ReadGLJournalLines(ctx context.Context, year, month int) ([]reporting.GLJournalLine, error) {
	lines, err := a.gl.DatasetJournalLines(ctx, year, month)
	if err != nil {
		return nil, err
	}
	out := make([]reporting.GLJournalLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, reporting.GLJournalLine{
			AccountCode: l.AccountCode,
			AccountName: l.AccountName,
			FiscalYear:  l.FiscalYear,
			FiscalMonth: l.FiscalMonth,
			BranchID:    l.BranchID,
			EntryNo:     l.EntryNo,
			JournalMemo: l.JournalMemo,
			DebitCents:  l.DebitCents,
			CreditCents: l.CreditCents,
		})
	}
	return out, nil
}

func (a w3rw1DatasetSources) ReadSalesLines(ctx context.Context, from, to time.Time) ([]reporting.SalesInvoiceLine, error) {
	lines, err := a.invoices.DatasetInvoiceLines(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]reporting.SalesInvoiceLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, reporting.SalesInvoiceLine{
			InvoiceID:      l.InvoiceID,
			InvoiceNo:      l.InvoiceNo,
			CustomerID:     l.CustomerID,
			BranchID:       l.BranchID,
			ProductID:      l.ProductID,
			SourceKind:     l.SourceKind,
			InvoiceDate:    l.InvoiceDate,
			Description:    l.Description,
			QtyBase:        l.QtyBase,
			UnitPriceCents: l.UnitPriceCents,
			LineTotalCents: l.LineTotalCents,
			LineTaxCents:   l.LineTaxCents,
		})
	}
	return out, nil
}

func (a w3rw1DatasetSources) ReadStockOnHand(ctx context.Context) ([]reporting.StockOnHandLine, error) {
	lines, err := a.stock.DatasetStockOnHand(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]reporting.StockOnHandLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, reporting.StockOnHandLine{
			ProductID:     l.ProductID,
			BranchID:      l.BranchID,
			OnHandBase:    l.OnHandBase,
			UnitCostCents: l.UnitCostCents,
			ValueCents:    l.ValueCents,
		})
	}
	return out, nil
}
