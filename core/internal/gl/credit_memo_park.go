package gl

// credit_memo_park.go: THE PARKED JOURNAL A CREDIT MEMO WAITS ON.
//
// -- WHY THIS EXISTS -----------------------------------------------------------------------------------
// gl's own rule is that only a MANUAL journal may park (journal.go, CreateJournal): a non-manual source
// that parked would have no approval request behind it and could never post, so gl refuses it. That
// refusal is right for a source that cannot file a request.
//
// A credit memo over the approval threshold is the one case that files its own request. The memo
// document, the reversing journal parked here, and the approval request in the approvals queue are
// written in ONE transaction, so a half-parked credit cannot exist, and the approvals rail posts this
// journal when a second pair of eyes signs it.
//
// -- WHAT A PARKED JOURNAL MOVES -----------------------------------------------------------------------
// NOTHING. The journal is a DRAFT, so no account balance moves and no period is closed by it. The AR
// subledger is parked with it, as a DRAFT memo document, so the customer is credited at the moment the
// credit is approved and not before. Both sides of the credit therefore move together or not at all.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// ParkExternalJournalTx parks an external journal as a DRAFT on the CALLER's transaction and returns
// its id.
//
// The caller owns the transaction because the parked journal and the memo it belongs to and the
// approval request that releases it are three different modules' rows, and only the composition root
// may write them together.
func (s *Service) ParkExternalJournalTx(ctx context.Context, tx pgx.Tx, in ExternalJournal) (string, error) {
	if tx == nil {
		return "", errNilTx
	}
	draft, err := s.externalDraft(in)
	if err != nil {
		return "", err
	}
	// The one line that differs from a posted external journal: this one waits.
	draft.Status = StatusDraft
	id, _, err := s.store.InsertJournalTx(ctx, tx, draft)
	if err != nil {
		return "", err
	}
	return id, nil
}
