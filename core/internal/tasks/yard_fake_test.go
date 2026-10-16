package tasks

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// yard_fake_test.go, the in-memory taskStore's W2 yard half. It mirrors yard_repository.go move for
// move: the same shared canTransition matrix guards STAGE, the same "already resolved" check guards a
// double-resolve, and the same shortage counts are recomputed on the task after every shortage write.
// That is the point of writing it this way, the legality and counting rules are exercised without a
// database and cannot drift from the real writer, because both call the same functions.

func (f *fakeStore) Stage(_ context.Context, in StageInput) (Task, error) {
	t, ok := f.tasks[in.TaskID]
	if !ok {
		return Task{}, ErrNotFound
	}
	if t.Status == StatusStaged {
		return Task{}, ErrNoTransition
	}
	if !canTransition(t.Status, StatusStaged) {
		return Task{}, ErrIllegalTransition
	}
	now := time.Now()
	t.Status = StatusStaged
	t.StagedAt = &now
	t.StageLocation = in.Location
	t.StageRef = in.Ref
	t.UpdatedAt = now
	f.tasks[in.TaskID] = t
	return t, nil
}

func (f *fakeStore) InsertShortage(_ context.Context, in ShortageInput) (Shortage, Task, error) {
	if _, ok := f.tasks[in.TaskID]; !ok {
		return Shortage{}, Task{}, ErrNotFound
	}
	f.shSeq++
	sh := Shortage{
		ID:          "33333333-3333-4333-8333-" + padID(f.shSeq),
		TaskID:      in.TaskID,
		OrderLineNo: in.OrderLineNo,
		ProductID:   in.ProductID,
		SKU:         in.SKU,
		Description: in.Description,
		QtyShort:    in.QtyShort,
		UOM:         in.UOM,
		Note:        in.Note,
		ReportedBy:  in.ReportedBy,
		ReportedAt:  time.Now(),
	}
	f.shortages[in.TaskID] = append(f.shortages[in.TaskID], sh)
	return sh, f.recount(in.TaskID), nil
}

func (f *fakeStore) ListShortages(_ context.Context, taskID string, openOnly bool) ([]Shortage, error) {
	out := []Shortage{}
	for _, s := range f.shortages[taskID] {
		if openOnly && !s.Open() {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeStore) ResolveShortage(_ context.Context, in ResolveShortageInput) (Shortage, Task, error) {
	list := f.shortages[in.TaskID]
	for i, s := range list {
		if s.ID != in.ShortageID {
			continue
		}
		if !s.Open() {
			return Shortage{}, Task{}, ErrShortageResolved
		}
		now := time.Now()
		s.ResolvedAt = &now
		s.ResolvedBy = in.ActorID
		s.Resolution = in.Resolution
		list[i] = s
		f.shortages[in.TaskID] = list
		return s, f.recount(in.TaskID), nil
	}
	return Shortage{}, Task{}, ErrShortageNotFound
}

func (f *fakeStore) StalePickTasks(_ context.Context, cutoff time.Time, limit int) ([]Task, error) {
	out := []Task{}
	for _, t := range f.tasks {
		if t.TaskType != TypePick || t.Status != StatusQueued || !t.CreatedAt.Before(cutoff) {
			continue
		}
		out = append(out, t)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// recount refreshes the task's shortage counters exactly as the SQL sub-selects do, and returns the
// refreshed task, so the fake hands the caller the same "counts already include this write" task the
// repository does.
func (f *fakeStore) recount(taskID string) Task {
	t := f.tasks[taskID]
	open, total := 0, 0
	for _, s := range f.shortages[taskID] {
		total++
		if s.Open() {
			open++
		}
	}
	t.OpenShortages, t.TotalShortages = open, total
	f.tasks[taskID] = t
	return t
}

// - 0187 · the in-memory pick record -
//
// It upserts by (task, line) exactly as the SQL does, because a fake whose uniqueness rule differs
// from the schema's lets a service test pass on a shape the database would refuse.

func (f *fakeStore) RecordPickLocked(ctx context.Context, taskID, actorID string, apply func(context.Context, Task) ([]PickedLineInput, error)) ([]PickedLine, error) {
	t, ok := f.tasks[taskID]
	if !ok {
		return nil, ErrNotFound
	}
	if err := notAPick(t.TaskType); err != nil {
		return nil, err
	}
	if err := pickClosed(t.Status); err != nil {
		return nil, err
	}
	lines, err := apply(ctx, t)
	if err != nil {
		return nil, err
	}
	return f.UpsertPickedLines(ctx, taskID, actorID, lines)
}

func (f *fakeStore) UpsertPickedLines(_ context.Context, taskID, actorID string, lines []PickedLineInput) ([]PickedLine, error) {
	if _, ok := f.tasks[taskID]; !ok {
		return nil, ErrNotFound
	}
	if f.picked == nil {
		f.picked = map[string]map[int]PickedLine{}
	}
	if f.picked[taskID] == nil {
		f.picked[taskID] = map[int]PickedLine{}
	}
	for _, l := range lines {
		f.picked[taskID][l.OrderLineNo] = PickedLine{
			ID:          fmt.Sprintf("pl-%s-%d", taskID, l.OrderLineNo),
			TaskID:      taskID,
			OrderLineNo: l.OrderLineNo,
			ProductID:   l.ProductID,
			SKU:         l.SKU,
			Description: l.Description,
			QtyOrdered:  l.QtyOrdered,
			QtyPicked:   l.QtyPicked,
			UOM:         l.UOM,
			PickedBy:    actorID,
			ShortReason: l.ShortReason,
			ShortNote:   l.ShortNote,
			PickedAt:    time.Now().UTC(),
		}
	}
	return f.ListPickedLines(context.Background(), taskID)
}

func (f *fakeStore) ListPickedLines(_ context.Context, taskID string) ([]PickedLine, error) {
	out := []PickedLine{}
	for _, l := range f.picked[taskID] {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OrderLineNo < out[j].OrderLineNo })
	return out, nil
}
