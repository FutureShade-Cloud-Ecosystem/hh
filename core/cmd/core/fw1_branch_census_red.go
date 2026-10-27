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
var fw1BranchCensusRed = map[string]string{}
