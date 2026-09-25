package sync

// conflict.go is the conflict engine: what happens when a pushed write is
// based on a seq the row has moved past (the client's base_seq is older
// than the row's current seq, someone wrote in between). Detection is the
// engine's; resolution is the scope's policy, and every policy resolves
// into one of the three outcome shapes the contract's SyncConflict envelope
// pins: server_wins returns the server state and applies nothing,
// last_write_wins_by_field merges per field and reports the merged state,
// manual never overwrites and leaves the entity waiting on a review card.
// The envelope travels per operation inside the 200 push response (the
// pack's protocol section), never as a failed request.

// conflictOutcome is one resolved conflict: the envelope's legs, plus the
// write the engine must still perform (nil when the policy applies
// nothing). The outcome's fields map one to one onto apigen.SyncConflict.
type conflictOutcome struct {
	// Policy is the scope's policy, the envelope's policy leg verbatim.
	Policy Policy
	// ServerState is the row the client is told to show.
	ServerState map[string]any
	// Applied is the envelope's applied leg: false for server_wins and
	// manual (nothing of the client's operation reached the row), true
	// for last_write_wins_by_field (the merge wrote).
	Applied bool
	// MergedFields is the field set the engine writes when the policy
	// merges (last_write_wins_by_field); nil otherwise.
	MergedFields map[string]any
	// ReviewMarker names the manual policy's review fact: the client's
	// operation is recorded as waiting, and the audit seam carries the
	// marker server-side. False for every other policy.
	ReviewMarker bool
}

// resolveConflict resolves one stale write under the scope's policy. row is
// the server row as loaded (the client is told to show it under
// server_wins and manual); clientFields is the operation's declared
// payload, used only by the merging policy. The server row stays
// authoritative on every field the payload does not declare: that is the
// whole content of "per field".
func resolveConflict(policy Policy, row RowState, clientFields map[string]any) conflictOutcome {
	switch policy {
	case PolicyServerWins:
		// The server row is the outcome. The client's stale write is
		// discarded whole, never merged, never partially applied.
		return conflictOutcome{
			Policy:      PolicyServerWins,
			ServerState: row.Data,
			Applied:     false,
		}
	case PolicyLWWByField:
		// Merge per field: begin from the server row (authoritative on
		// undeclared fields), overwrite exactly the declared fields.
		merged := make(map[string]any, len(row.Data)+len(clientFields))
		for k, v := range row.Data {
			merged[k] = v
		}
		for k, v := range clientFields {
			merged[k] = v
		}
		return conflictOutcome{
			Policy:       PolicyLWWByField,
			ServerState:  merged,
			Applied:      true,
			MergedFields: clientFields,
		}
	case PolicyManual:
		// Never overwrites: the server row is returned unchanged, the
		// operation's payload applies nothing, and the review marker is
		// written so the review surface can see the waiting client edit.
		return conflictOutcome{
			Policy:       PolicyManual,
			ServerState:  row.Data,
			Applied:      false,
			ReviewMarker: true,
		}
	}
	// An unlisted policy cannot resolve: fail closed as server_wins, the
	// policy that applies nothing. The registry test pins every scope to
	// a listed policy, so this arm is unreachable by construction.
	return conflictOutcome{
		Policy:      PolicyServerWins,
		ServerState: row.Data,
		Applied:     false,
	}
}
