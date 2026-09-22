package dispatch

// transitions.go, the lifecycle matrix, as DATA. Both the service and its tests read this table, so
// "which moves are legal" is stated exactly once and cannot drift between the guard and the test that
// claims to cover it (order/service.go manualRules, same reasoning).
//
// WHY FORWARD-ONLY. Every backward move someone asks for is really a correction of a mistake, "I
// marked it loaded and it wasn't". A backward transition makes the timestamp columns lie (loaded_at
// survives an un-load) and makes the audit trail describe a sequence that never happened. The
// correction path is CANCEL and re-schedule, which leaves both facts on the record: it was planned,
// and it was withdrawn. That is what a dispatcher can defend three weeks later.

// stopTransitions maps a stop's current status to every status it may legally move to.
//
// DELIVERED, FAILED and CANCELLED map to EMPTY sets, deliberately and not by omission: a terminal
// state is terminal. A stop that arrived cannot un-arrive, and one that was cancelled is re-scheduled
// as a NEW stop rather than resurrected, the partial unique index in 0090 permits exactly that.
var stopTransitions = map[string][]string{
	StopReady:     {StopLoaded, StopCancelled},
	StopLoaded:    {StopEnRoute, StopCancelled},
	StopEnRoute:   {StopDelivered, StopFailed},
	StopDelivered: {},
	StopFailed:    {},
	StopCancelled: {},
}

// runTransitions maps a run's current status to its legal successors.
//
// DISPATCHED has NO path to CANCELLED. Once the truck has left, "cancelled" is not a thing that can
// be true about the run, the stops on it fail or arrive individually, and the run completes. Letting
// a dispatcher cancel a departed run would erase a morning of real movement from the record.
var runTransitions = map[string][]string{
	RunPlanned:    {RunDispatched, RunCancelled},
	RunDispatched: {RunCompleted},
	RunCompleted:  {},
	RunCancelled:  {},
}

// stopCanTransition reports whether from→to is on the stop matrix. An unknown `from` (a status this
// build does not know, e.g. a row written by a newer deploy) is refused rather than waved through:
// the safe answer to "I don't recognise this state" is not "go ahead".
func stopCanTransition(from, to string) bool {
	return contains(stopTransitions[from], to)
}

// runCanTransition reports whether from→to is on the run matrix.
func runCanTransition(from, to string) bool {
	return contains(runTransitions[from], to)
}

// StopIsTerminal reports whether a stop status admits no further movement. The run-completion guard
// reads it, and so does the board (a terminal stop draws no action buttons).
func StopIsTerminal(status string) bool {
	next, known := stopTransitions[status]
	return known && len(next) == 0
}

// StopNextStates returns the legal successors of a stop status, for the UI's action affordances. A
// copy is returned so a caller cannot mutate the matrix through the slice it was handed.
func StopNextStates(status string) []string {
	next := stopTransitions[status]
	out := make([]string, len(next))
	copy(out, next)
	return out
}

// RunNextStates returns the legal successors of a run status.
func RunNextStates(status string) []string {
	next := runTransitions[status]
	out := make([]string, len(next))
	copy(out, next)
	return out
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
