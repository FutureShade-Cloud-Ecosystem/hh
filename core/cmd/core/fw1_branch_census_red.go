package main

// fw1_branch_census_red.go records the gl_journal_lines writers that still
// leave branch_id NULL on main, together with the wave 1 item that owns
// stamping each path (FW1-05 for bankrec paths, FW1-08b for the others).
//
// Each owner item deletes its own entries from fw1BranchCensusRed in the PR
// that stamps those writers. TestFW1JournalLinesCarryBranch runs every writer
// even when listed here and fails if a listed writer is already green (stale
// skip guard).

// fw1BranchCensusWriters is the complete list of gl_journal_lines writer keys
// exercised by TestFW1JournalLinesCarryBranch.
var fw1BranchCensusWriters = []string{
	"migration_0056_0221_setup_entries",
	"manual_journal_with_branch",
	"manual_journal_without_branch",
	"reverse_journal_with_branch",
	"reverse_journal_without_branch",
	"recurring_journal_run",
	"outbox_invoice_finalized",
	"outbox_payment_received",
	"outbox_tax_relief_assessed",
	"outbox_payment_bounced",
	"outbox_payment_reversed",
	"outbox_payment_reapplied",
	"outbox_order_fulfilled",
	"outbox_deposit_released",
	"outbox_deposit_cancelled",
	"outbox_pos_return_committed",
	"outbox_order_returned",
	"outbox_pos_return_written_off",
	"outbox_till_session_completed",
	"bankrec_auto_match_payment",
	"bankrec_auto_match_lodgement",
	"bankrec_manual_match_payment",
	"bankrec_manual_match_lodgement",
	"bankrec_unmatch_reversal",
	"external_credit_memo_poster",
	"external_post_external_tx",
	"year_end_close",
}

// fw1BranchCensusRed maps each still-failing writer key to its owning wave 1
// item ("FW1-05" for bankrec paths, "FW1-08b" for the others).
var fw1BranchCensusRed = map[string]string{
	"bankrec_manual_match_payment":      "FW1-05",
	"bankrec_manual_match_lodgement":    "FW1-05",
	"bankrec_unmatch_reversal":          "FW1-05",
	"migration_0056_0221_setup_entries": "FW1-08b",
	"manual_journal_without_branch":     "FW1-08b",
	"reverse_journal_without_branch":    "FW1-08b",
	"recurring_journal_run":             "FW1-08b",
	"outbox_tax_relief_assessed":        "FW1-08b",
	"year_end_close":                    "FW1-08b",
}

// fw1NonGLWriterSite identifies a non-GL journal writer call site or outbox
// emitter whose payload carries a branch, together with where its branch_id
// comes from at source.
type fw1NonGLWriterSite struct {
	File         string
	Func         string
	Kind         string
	BranchSource string
}

// fw1NonGLJournalWriterCallSites lists every non-GL journal writer call site
// (PostExternal, PostExternalTx, PostJournal, PostJournalTx,
// PostCreditMemoJournal) and every emitter of an outbox topic whose payload
// carries a branch outside core/internal/gl. Guarded by
// TestFW1NonGLJournalWriterCallSitesComplete.
var fw1NonGLJournalWriterCallSites = []fw1NonGLWriterSite{
	{
		File:         "core/cmd/core/tops_adapters.go",
		Func:         "(w6cCreditMemoPoster).PostCreditMemoJournal",
		Kind:         "PostExternal",
		BranchSource: "in.BranchID from billing.CreditMemoJournal.BranchID",
	},
	{
		File:         "core/cmd/core/wire_money.go",
		Func:         "(invGLPoster).PostJournal",
		Kind:         "PostExternal",
		BranchSource: "in.BranchID from inventory.GLJournal.BranchID",
	},
	{
		File:         "core/cmd/core/wire_operations.go",
		Func:         "(glPosterAdapter).PostJournal",
		Kind:         "PostExternal",
		BranchSource: "in.BranchID from bankrec gl.ExternalJournal.BranchID",
	},
	{
		File:         "core/cmd/core/wire_operations.go",
		Func:         "(lienGLPoster).PostJournal",
		Kind:         "PostExternal",
		BranchSource: "in.BranchID from lien.GLJournal.BranchID",
	},
	{
		File:         "core/cmd/core/wire_operations.go",
		Func:         "(invcountGLPoster).PostJournal",
		Kind:         "PostExternal",
		BranchSource: "in.BranchID from invcount.GLJournal.BranchID",
	},
	{
		File:         "core/cmd/core/wire_operations.go",
		Func:         "(landedGLPoster).PostJournal",
		Kind:         "PostExternal",
		BranchSource: "in.BranchID from landedcost.GLJournal.BranchID",
	},
	{
		File:         "core/cmd/core/wire_operations.go",
		Func:         "(landedGLPoster).PostJournalTx",
		Kind:         "PostExternalTx",
		BranchSource: "in.BranchID from landedcost.GLJournal.BranchID",
	},
	{
		File:         "core/cmd/core/wire_operations.go",
		Func:         "(warrantyGLPoster).PostJournal",
		Kind:         "PostExternal",
		BranchSource: "in.BranchID from warranty.GLJournal.BranchID",
	},
	{
		File:         "core/cmd/core/wire_operations.go",
		Func:         "(w4cGLPoster).PostJournal",
		Kind:         "PostExternal",
		BranchSource: "in.BranchID from purchasing.GLJournal.BranchID",
	},
	{
		File:         "core/cmd/core/wire_operations.go",
		Func:         "(w4cGLPoster).PostJournalTx",
		Kind:         "PostExternalTx",
		BranchSource: "in.BranchID from purchasing.GLJournal.BranchID",
	},
	{
		File:         "core/internal/bankrec/service.go",
		Func:         "(*Service).persistProposal",
		Kind:         "PostJournal",
		BranchSource: "prop.Journal.BranchID (CandidatePayment.BranchID for PAYMENT; Lodgement.BranchID for LODGEMENT)",
	},
	{
		File:         "core/internal/bankrec/service.go",
		Func:         "(*Service).ManualMatch",
		Kind:         "PostJournal",
		BranchSource: "manualJournal passes empty BranchID today (RED, owned by FW1-05)",
	},
	{
		File:         "core/internal/bankrec/service.go",
		Func:         "(*Service).UnmatchReach",
		Kind:         "PostJournal",
		BranchSource: "reversalJournal sets no BranchID today (RED, owned by FW1-05)",
	},
	{
		File:         "core/internal/billing/credit_memo.go",
		Func:         "(*InvoiceWriter).IssueCreditMemo",
		Kind:         "PostCreditMemoJournal",
		BranchSource: "in.BranchID (refused when empty at credit_memo.go:290)",
	},
	{
		File:         "core/internal/billing/credit_memo_repost.go",
		Func:         "(*InvoiceWriter).repostAll",
		Kind:         "PostCreditMemoJournal",
		BranchSource: "r.BranchID from invoices.branch_id (NOT NULL)",
	},
	{
		File:         "core/internal/invcount/service.go",
		Func:         "postSheetGL",
		Kind:         "PostJournal",
		BranchSource: "sheet.BranchID from count_sheets.branch_id (NOT NULL, refused when empty by authorizeBranch)",
	},
	{
		File:         "core/internal/inventory/service.go",
		Func:         "(*Service).applyReceipt",
		Kind:         "emit:po.received",
		BranchSource: "in.BranchID on po.received outbox payload",
	},
	{
		File:         "core/internal/inventory/service.go",
		Func:         "(*Service).postPOReceived",
		Kind:         "PostJournal",
		BranchSource: "p.BranchID from po.received outbox payload emitted at inventory/service.go:179",
	},
	{
		File:         "core/internal/inventory/service.go",
		Func:         "(*Service).postInventoryAdjusted",
		Kind:         "PostJournal",
		BranchSource: "p.BranchID from inventory.adjusted outbox payload (no producer in Go or SQL today)",
	},
	{
		File:         "core/internal/inventory/transfer.go",
		Func:         "(*Service).postTransferLeg",
		Kind:         "PostJournal",
		BranchSource: "p.BranchID (from_branch_id on ship, to_branch_id on receive; two-branch transfer question is FW1-07)",
	},
	{
		File:         "core/internal/landedcost/service.go",
		Func:         "(*Service).postJournal",
		Kind:         "PostJournal",
		BranchSource: "lc.BranchID from landed_costs.branch_id (NOT NULL)",
	},
	{
		File:         "core/internal/landedcost/service.go",
		Func:         "(*Service).postJournalTx",
		Kind:         "PostJournalTx",
		BranchSource: "lc.BranchID from landed_costs.branch_id (NOT NULL)",
	},
	{
		File:         "core/internal/lien/service.go",
		Func:         "(*Service).releaseHoldback",
		Kind:         "PostJournal",
		BranchSource: "job.BranchID from lien_jobs.branch_id (NOT NULL)",
	},
	{
		File:         "core/internal/payments/reversal.go",
		Func:         "(*Repository).ReversePayment",
		Kind:         "emit:payment.reversed",
		BranchSource: "orig.BranchID from payments.branch_id (NOT NULL) in payment.reversed outbox payload",
	},
	{
		File:         "core/internal/payments/reversal.go",
		Func:         "(*Repository).ReapplyHeld",
		Kind:         "emit:payment.reapplied",
		BranchSource: "p.BranchID from payments.branch_id (NOT NULL) in payment.reapplied outbox payload",
	},
	{
		File:         "core/internal/pos/returns.go",
		Func:         "(*Service).commitReturnTx",
		Kind:         "emit:order.returned",
		BranchSource: "ret.BranchID from pos_returns.branch_id (NOT NULL) in order.returned outbox payload",
	},
	{
		File:         "core/internal/pos/returns.go",
		Func:         "(*Service).commitReturnTx",
		Kind:         "emit:pos.return.written_off",
		BranchSource: "ret.BranchID from pos_returns.branch_id (NOT NULL) in pos.return.written_off outbox payload",
	},
	{
		File:         "core/internal/pos/returns.go",
		Func:         "(*Service).commitReturnTx",
		Kind:         "emit:pos.return.committed",
		BranchSource: "ret.BranchID from pos_returns.branch_id (NOT NULL) in pos.return.committed outbox payload",
	},
	{
		File:         "core/internal/pos/service.go",
		Func:         "(*Service).saleInTx",
		Kind:         "emit:order.fulfilled",
		BranchSource: "ord.BranchID from orders.branch_id (NOT NULL) in order.fulfilled outbox payload",
	},
	{
		File:         "core/internal/pos/service.go",
		Func:         "(*Service).saleInTx",
		Kind:         "emit:tax.relief.assessed",
		BranchSource: "in.BranchID on sale/order (buildTaxRelief in gl/posting.go:248-297 sets no BranchID today; RED, owned by FW1-08b)",
	},
	{
		File:         "core/internal/purchasing/service.go",
		Func:         "(*Service).postBillJournal",
		Kind:         "PostJournalTx",
		BranchSource: "bill.BranchID from purchasing_bills.branch_id (NOT NULL)",
	},
	{
		File:         "core/internal/till/repository.go",
		Func:         "(*Repository).SetCompleted",
		Kind:         "emit:till.session.completed",
		BranchSource: "s.BranchID from till_sessions.branch_id (NOT NULL) in till.session.completed outbox payload",
	},
	{
		File:         "core/internal/till/repository.go",
		Func:         "(*Repository).MarkNSF",
		Kind:         "emit:payment.bounced",
		BranchSource: "res.BranchID from payments.branch_id (NOT NULL) in payment.bounced outbox payload",
	},
	{
		File:         "core/internal/warranty/moneypath.go",
		Func:         "(*Service).runGLPath",
		Kind:         "PostJournal",
		BranchSource: "claim.BranchID from warranty_claims.branch_id (NOT NULL) via buildJournal",
	},
	{
		File:         "core/internal/warranty/moneypath.go",
		Func:         "(*Service).fallbackToGL",
		Kind:         "PostJournal",
		BranchSource: "claim.BranchID from warranty_claims.branch_id (NOT NULL) via buildJournal",
	},
}
