package main

// fw1_branch_census_red.go records the gl_journal_lines writers that still
// leave branch_id NULL on main, together with the wave 1 item that owns
// stamping each path (FW1-05 for the bankrec paths; FW1-08b's own paths, the
// Go writers and the 0056/0221 setup entries, are stamped and gone from the
// map).
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
	"parked_credit_memo_journal",
}

// fw1BranchCensusRed maps each still-failing writer key to its owning wave 1
// item (the two bankrec paths left are FW1-05's). Main's map is the base:
// 629 stamped every FW1-08b writer (the five entries this branch still listed
// are gone on the merged tree), and year_end_close left the map with the
// FW1-09b merge: the year end closes each branch's results in its own closing
// journal, every leg under its branch stamp and the company level lines under
// the head office branch, so it leaves no NULL branch_id line.
var fw1BranchCensusRed = map[string]string{
	"bankrec_manual_match_payment": "FW1-05",
	"bankrec_unmatch_reversal":     "FW1-05",
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
// or method-value selector (PostExternal, PostExternalTx, PostJournal,
// PostJournalTx, PostCreditMemoJournal, CreateJournal, ReverseJournal,
// RunRecurring, CloseYear), every emitter of an outbox topic whose payload
// carries a branch, and every dynamic-topic outbox helper outside
// core/internal/gl. Guarded by TestFW1NonGLJournalWriterCallSitesComplete.
var fw1NonGLJournalWriterCallSites = []fw1NonGLWriterSite{
	{
		File:         "core/cmd/core/credit_memo_wire.go",
		Func:         "(creditMemoParkerAdapter).ParkCreditMemo",
		Kind:         "ParkExternalJournalTx",
		BranchSource: "journal.BranchID from billing's parked credit memo journal, the branch the credit was filed at",
	},
	{
		File:         "core/cmd/core/order_events.go",
		Func:         "(orderChangeEvents).EnqueueTx",
		Kind:         "dynamic-topic:INSERT INTO events_outbox",
		BranchSource: "generic outbox SQL helper for order change events; all callers are scanned via EnqueueTx (order.change.applied has no GL journal consumer)",
	},
	{
		File:         "core/cmd/core/tops_adapters.go",
		Func:         "(w5cStatementQueue).EnqueueTx",
		Kind:         "dynamic-topic:EnqueueTx",
		BranchSource: "adapter forwarding billing.StatementNoticeQueue (StatementNoticeTopic = sms.ar.notice) to messagingprovider.Outbox.EnqueueTx; no GL journal consumer",
	},
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
		File:         "core/cmd/core/wire_money.go",
		Func:         "(invGLPoster).PostJournalTx",
		Kind:         "PostExternalTx",
		BranchSource: "in.BranchID from inventory.GLJournal.BranchID (FW1-07 round 2 sub item 5: the transfer leg's journal rides the move's transaction)",
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
		BranchSource: "manualJournal stamps the LODGEMENT path from the till's lodgement facts (FW1-06a) and passes empty BranchID for PAYMENT today (RED, owned by FW1-05)",
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
		BranchSource: "m.branchID loaded from invoices.branch_id (NOT NULL) by findCreditMemosWithoutJournals",
	},
	{
		File:         "core/internal/billing/invoice_writer.go",
		Func:         "emitOutbox",
		Kind:         "dynamic-topic:INSERT INTO events_outbox",
		BranchSource: "billing package outbox SQL helper; all callers are scanned via emitOutbox",
	},
	{
		File:         "core/internal/billing/statement_outbox.go",
		Func:         "(statementNoticeOutbox).EnqueueStatementTx",
		Kind:         "dynamic-topic:EnqueueTx",
		BranchSource: "emits o.topic (StatementNoticeTopic = sms.ar.notice at billing/statement_outbox.go:51, set at :90); notification topic with no GL journal consumer",
	},
	{
		File:         "core/internal/invcount/service.go",
		Func:         "postSheetGL",
		Kind:         "PostJournal",
		BranchSource: "sheet.BranchID from count_sheets.branch_id (NOT NULL, refused when empty by authorizeBranch)",
	},
	{
		File:         "core/internal/inventory/repository.go",
		Func:         "(*Repository).EmitEvent",
		Kind:         "dynamic-topic:INSERT INTO events_outbox",
		BranchSource: "inventory package outbox SQL helper; all callers are scanned via EmitEvent",
	},
	{
		File:         "core/internal/inventory/service.go",
		Func:         "(*Service).applyReceipt",
		Kind:         "emit:po.received",
		BranchSource: "hdr.BranchID from POHeaderForReceipt (purchase_orders.branch_id, NOT NULL) in po.received outbox payload",
	},
	{
		File:         "core/internal/inventory/service.go",
		Func:         "(*Service).fulfilLinesTx",
		Kind:         "emit:order.fulfilled",
		BranchSource: "branch_id on the order.fulfilled payload, the fulfilling branch the order's relief or group relief passes in (FW1-07)",
	},
	{
		File:         "core/internal/inventory/service.go",
		Func:         "(*Service).postPOReceived",
		Kind:         "PostJournal",
		BranchSource: "p.BranchID from po.received outbox payload emitted at inventory/service.go:179",
	},
	{
		File:         "core/internal/inventory/service.go",
		Func:         "(*Service).postPOReceivedRounding",
		Kind:         "PostJournal",
		BranchSource: "p.BranchID from po.received outbox payload (FW1-07 round 1 sub item 2: the WAC rounding adjustment is its own journal, so its own function)",
	},
	{
		File:         "core/internal/inventory/service.go",
		Func:         "(*Service).postPOReceivedTrueUp",
		Kind:         "PostJournal",
		BranchSource: "p.BranchID from po.received outbox payload (FW1-07 round 1 sub item 4, ruling 82.4: one true-up journal per line, so its own function)",
	},
	{
		File:         "core/internal/inventory/service.go",
		Func:         "(*Service).postInventoryAdjusted",
		Kind:         "PostJournal",
		BranchSource: "p.BranchID from inventory.adjusted outbox payload (no producer in Go or SQL today)",
	},
	{
		File:         "core/internal/inventory/transfer.go",
		Func:         "(*Service).postTransferLegTx",
		Kind:         "PostJournalTx",
		BranchSource: "p.BranchID (from_branch_id on ship, to_branch_id on receive; two-branch transfer question is FW1-07)",
	},
	{
		File:         "core/internal/inventory/transfer.go",
		Func:         "(*Service).postTransferLegCommitting",
		Kind:         "PostJournal",
		BranchSource: "p.BranchID (from_branch_id on ship, to_branch_id on receive; two-branch transfer question is FW1-07), passed through postTransferLegTx",
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
		File:         "core/internal/portal/repository.go",
		Func:         "emitOutbox",
		Kind:         "dynamic-topic:INSERT INTO events_outbox",
		BranchSource: "portal package outbox SQL helper; all callers are scanned via emitOutbox",
	},
	{
		File:         "core/internal/pos/repository.go",
		Func:         "(*Repository).EmitEvent",
		Kind:         "dynamic-topic:INSERT INTO events_outbox",
		BranchSource: "pos package outbox SQL helper; all callers are scanned via EmitEvent",
	},
	{
		File:         "core/internal/pos/returns.go",
		Func:         "(*Service).commitReturnTx",
		Kind:         "emit:order.returned",
		BranchSource: "retRow.BranchID from pos_returns.branch_id (branch where return is rung, FROM-PRINCIPAL 137) in order.returned outbox payload",
	},
	{
		File:         "core/internal/pos/returns.go",
		Func:         "(*Service).commitReturnTx",
		Kind:         "emit:pos.return.written_off",
		BranchSource: "retRow.BranchID from pos_returns.branch_id (branch where return is rung, FROM-PRINCIPAL 137) in pos.return.written_off outbox payload",
	},
	{
		File:         "core/internal/pos/returns.go",
		Func:         "(*Service).commitReturnTx",
		Kind:         "emit:pos.return.committed",
		BranchSource: "retRow.BranchID from pos_returns.branch_id (branch where return is rung, FROM-PRINCIPAL 137) in pos.return.committed outbox payload",
	},
	{
		File:         "core/internal/pos/service.go",
		Func:         "(*Service).saleInTx",
		Kind:         "emit:order.fulfilled",
		BranchSource: "in.BranchID (sale input branch, also written to orders.branch_id in the same tx) in order.fulfilled outbox payload",
	},
	{
		File:         "core/internal/pos/service.go",
		Func:         "(*Service).saleInTx",
		Kind:         "emit:tax.relief.assessed",
		BranchSource: "in.BranchID (sale input branch, also written to orders.branch_id in the same tx) in tax.relief.assessed outbox payload (FW1-08b)",
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
		BranchSource: "branchID parameter (passed as sess.BranchID from till_sessions.branch_id by till/service.go:731) in till.session.completed outbox payload",
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
