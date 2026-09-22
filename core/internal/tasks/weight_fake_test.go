package tasks

import (
	"context"
	"time"
)

// weight_fake_test.go, the in-memory taskStore's W3 half (DIB-324 / DIB-325), mirroring
// weight_repository.go move for move, for the reason yard_fake_test.go's header states: the legality
// rule is exercised without a database and CANNOT drift from the real writer, because both walk the
// same pathToDone over the same allowedTransitions matrix.

// weightRules lets a test override the advance ruling per task type. Empty = every type disabled,
// which is exactly what 0108 seeds, the fake's default and production's default are the same
// default, so a test that forgets to configure it is testing the shipped behaviour.
type weightRules map[string]AdvanceRule

func (f *fakeStore) RecordWeight(_ context.Context, in WeightWrite) (Task, error) {
	t, ok := f.tasks[in.TaskID]
	if !ok {
		return Task{}, ErrNotFound
	}
	if t.TaskType != TypeNeedWeight {
		return Task{}, ErrNotWeightTask
	}
	if t.Status == StatusDone {
		return Task{}, ErrNoTransition
	}
	chain := pathToDone(t.Status)
	if len(chain) == 0 {
		return Task{}, ErrIllegalTransition
	}
	now := time.Now()
	t.Status = StatusDone
	t.WeightEntered = in.Weight
	t.WeightUOM = in.UOM
	t.DoneAt = &now
	if t.ClaimedBy == "" {
		t.ClaimedBy = in.ActorID
		t.ClaimedAt = &now
	}
	t.UpdatedAt = now
	f.tasks[in.TaskID] = t
	f.weightHops = append(f.weightHops, chain...)
	return t, nil
}

func (f *fakeStore) MarkWeightSync(_ context.Context, taskID string, at *time.Time, errText string) error {
	t, ok := f.tasks[taskID]
	if !ok {
		return ErrNotFound
	}
	if at != nil {
		errText = ""
	}
	t.WeightSyncedAt = at
	t.WeightSyncError = errText
	f.tasks[taskID] = t
	return nil
}

func (f *fakeStore) AdvanceRule(_ context.Context, taskType string) (AdvanceRule, error) {
	if f.advanceErr != nil {
		return AdvanceRule{}, f.advanceErr
	}
	if r, ok := f.rules[taskType]; ok {
		r.TaskType = taskType
		return r, nil
	}
	// An unknown type is a DISABLED rule and no error, the real repository's contract, verbatim.
	return AdvanceRule{TaskType: taskType}, nil
}
