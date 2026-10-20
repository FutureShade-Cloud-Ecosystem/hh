package tasks

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// - fakes -

// fakeStore is an in-memory taskStore. Its Transition mirrors the pgx Repository exactly, it enforces
// the state machine through the SAME shared canTransition function and applies the same field effects
// (claimed_by/at on ACCEPTED, done_at/photo_url on DONE), so the legality matrix is exercised without a
// database and can never drift from the real writer.
type fakeStore struct {
	tasks     map[string]Task
	seq       int
	createErr error

	// W2 yard state (the store methods live in yard_fake_test.go, mirroring yard_repository.go).
	shortages map[string][]Shortage // task id → its shortages, in report order
	shSeq     int

	// W3 state (the store methods live in weight_fake_test.go, mirroring weight_repository.go).
	rules      weightRules // task type → advance ruling; empty = every type OFF, as 0108 seeds
	weightHops []string    // every status hop RecordWeight walked, in order, the audit assertion
	advanceErr error       // when set, the rule read fails (the "gate unreadable" path)
	// picked is the 0187 record, keyed task -> line -> row (the schema's UNIQUE, in memory).
	picked map[string]map[int]PickedLine
}

func newFakeStore() *fakeStore {
	return &fakeStore{tasks: map[string]Task{}, shortages: map[string][]Shortage{}, rules: weightRules{}}
}

func (f *fakeStore) CreateTask(_ context.Context, in NewTaskInput) (Task, error) {
	if f.createErr != nil {
		return Task{}, f.createErr
	}
	f.seq++
	id := "00000000-0000-4000-8000-" + padID(f.seq)
	now := time.Now()
	t := Task{
		ID: id, TaskType: in.TaskType, OrderID: in.OrderID, BranchID: in.BranchID,
		// W3: order_line_no is carried, mirroring the real INSERT. The W2 create path never set it
		// (a PICK task is whole-order), so it was harmless to drop; a NEED_WEIGHT task IS a line.
		OrderLineNo: in.OrderLineNo,
		Priority:    in.Priority, Status: StatusQueued, CreatedBy: in.CreatedBy,
		Source: in.Source, SourceMessageID: in.SourceMessageID, CreatedAt: now, UpdatedAt: now,
	}
	f.tasks[id] = t
	return t, nil
}

func (f *fakeStore) GetTask(_ context.Context, id string) (Task, error) {
	t, ok := f.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	return t, nil
}

func (f *fakeStore) ListTasks(_ context.Context, flt TaskFilter) ([]Task, error) {
	var out []Task
	for _, t := range f.tasks {
		if flt.Status != "" && t.Status != flt.Status {
			continue
		}
		if flt.BranchID != "" && t.BranchID != flt.BranchID {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeStore) Transition(_ context.Context, in TransitionInput) (Task, error) {
	t, ok := f.tasks[in.TaskID]
	if !ok {
		return Task{}, ErrNotFound
	}
	if t.Status == in.To {
		return Task{}, ErrNoTransition
	}
	if !canTransition(t.Status, in.To) {
		return Task{}, ErrIllegalTransition
	}
	now := time.Now()
	t.Status = in.To
	t.UpdatedAt = now
	switch in.To {
	case StatusAccepted:
		t.ClaimedBy = in.ActorID
		t.ClaimedAt = &now
	case StatusDone:
		t.DoneAt = &now
		if in.PhotoURL != "" {
			t.PhotoURL = in.PhotoURL
		}
	}
	f.tasks[in.TaskID] = t
	return t, nil
}

func padID(n int) string {
	s := "000000000000"
	d := ""
	for n > 0 {
		d = string(rune('0'+n%10)) + d
		n /= 10
	}
	if d == "" {
		d = "0"
	}
	return s[:len(s)-len(d)] + d
}

// fakeOrderReader satisfies OrderReader: a known order resolves its brief; an unknown one returns
// ErrUnknownOrder (the contract the main.go adapter also honours by mapping order.ErrNotFound).
type fakeOrderReader struct{ orders map[string]OrderBrief }

func (f fakeOrderReader) OrderBrief(_ context.Context, id string) (OrderBrief, error) {
	ob, ok := f.orders[id]
	if !ok {
		return OrderBrief{}, ErrUnknownOrder
	}
	return ob, nil
}

// fakeNotifier records every posted EVENT so tests can assert create/done fire a ChatNotifier EVENT.
type fakeNotifier struct {
	posts []TaskEventRef
	err   error
}

func (f *fakeNotifier) PostTaskEvent(_ context.Context, ref TaskEventRef) error {
	f.posts = append(f.posts, ref)
	return f.err
}

// - helpers -

const (
	knownOrder = "11111111-1111-4111-8111-111111111111"
	branchA    = "22222222-2222-4222-8222-222222222222"
)

func newSvc(t *testing.T) (*Service, *fakeStore, *fakeNotifier) {
	t.Helper()
	store := newFakeStore()
	notifier := &fakeNotifier{}
	orders := fakeOrderReader{orders: map[string]OrderBrief{
		knownOrder: {ID: knownOrder, BranchID: branchA, Fulfillment: "PICKUP", Status: "CONFIRMED"},
	}}
	svc := NewService(store).WithOrderReader(orders).WithChatNotifier(notifier)
	return svc, store, notifier
}

// seedAt creates a task and drives it (via the service) to the requested starting status, returning its id.
func seedAt(t *testing.T, svc *Service, store *fakeStore, status string) string {
	t.Helper()
	task, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder, CreatedBy: "actor-1"})
	if err != nil {
		t.Fatalf("seed create: %v", err)
	}
	switch status {
	case StatusQueued:
	case StatusPushed:
		// No endpoint drives QUEUED→PUSHED (gated); set it directly on the fake to reach the state.
		task.Status = StatusPushed
		store.tasks[task.ID] = task
	case StatusAccepted:
		mustMove(t, svc.Claim, task.ID)
	case StatusInProgress:
		mustMove(t, svc.Claim, task.ID)
		mustMove(t, svc.Accept, task.ID)
	case StatusDone:
		mustMove(t, svc.Claim, task.ID)
		mustMove(t, svc.Accept, task.ID)
		if _, err := svc.Done(context.Background(), task.ID, "actor-1", ""); err != nil {
			t.Fatalf("seed done: %v", err)
		}
	case StatusCancelled:
		if _, err := svc.Cancel(context.Background(), task.ID, "actor-1", ""); err != nil {
			t.Fatalf("seed cancel: %v", err)
		}
	default:
		t.Fatalf("seedAt: unsupported status %q", status)
	}
	return task.ID
}

func mustMove(t *testing.T, move func(context.Context, string, string) (Task, error), id string) {
	t.Helper()
	if _, err := move(context.Background(), id, "actor-1"); err != nil {
		t.Fatalf("move: %v", err)
	}
}

// - tests -

func TestCreateFromOrder_Success(t *testing.T) {
	svc, _, notifier := newSvc(t)
	got, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder, CreatedBy: "actor-1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.Status != StatusQueued || got.TaskType != TypePick {
		t.Fatalf("want QUEUED PICK, got %s/%s", got.Status, got.TaskType)
	}
	if got.Source != SourceChat {
		t.Fatalf("want default source CHAT, got %s", got.Source)
	}
	if got.BranchID != branchA {
		t.Fatalf("want branch copied from order (%s), got %q", branchA, got.BranchID)
	}
	// create posts a "task created" EVENT with a task:<id> entity.
	if len(notifier.posts) != 1 {
		t.Fatalf("want 1 chat EVENT on create, got %d", len(notifier.posts))
	}
	if p := notifier.posts[0]; p.Entity != "task:"+got.ID || p.Topic != "task.created" || p.DeepLink == "" {
		t.Fatalf("unexpected create EVENT ref: %+v", p)
	}
}

func TestCreateFromOrder_UnknownOrderRejected(t *testing.T) {
	svc, store, notifier := newSvc(t)
	_, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: "99999999-9999-4999-8999-999999999999"})
	if !errors.Is(err, ErrUnknownOrder) {
		t.Fatalf("want ErrUnknownOrder, got %v", err)
	}
	if len(store.tasks) != 0 {
		t.Fatalf("no task should be created for an unknown order, got %d", len(store.tasks))
	}
	if len(notifier.posts) != 0 {
		t.Fatalf("no chat EVENT should fire for an unknown order, got %d", len(notifier.posts))
	}
}

func TestCreateFromOrder_MissingOrderID(t *testing.T) {
	svc, _, _ := newSvc(t)
	if _, err := svc.CreateFromOrder(context.Background(), CreateInput{}); err == nil {
		t.Fatal("want bad-request for missing order_id, got nil")
	}
}

func TestDone_PostsCompletionEvent(t *testing.T) {
	svc, store, notifier := newSvc(t)
	id := seedAt(t, svc, store, StatusInProgress)
	notifier.posts = nil // drop the create EVENT; assert only the completion EVENT
	got, err := svc.Done(context.Background(), id, "actor-1", "https://cdn/proof.jpg")
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if got.Status != StatusDone || got.DoneAt == nil {
		t.Fatalf("want DONE with done_at set, got %s / %v", got.Status, got.DoneAt)
	}
	if got.PhotoURL != "https://cdn/proof.jpg" {
		t.Fatalf("want photo_url captured, got %q", got.PhotoURL)
	}
	if len(notifier.posts) != 1 || notifier.posts[0].Topic != "task.done" {
		t.Fatalf("want 1 task.done EVENT, got %+v", notifier.posts)
	}
}

func TestClaim_StampsClaimedBy(t *testing.T) {
	svc, store, _ := newSvc(t)
	id := seedAt(t, svc, store, StatusQueued)
	got, err := svc.Claim(context.Background(), id, "picker-7")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got.Status != StatusAccepted || got.ClaimedBy != "picker-7" || got.ClaimedAt == nil {
		t.Fatalf("want ACCEPTED claimed by picker-7, got %+v", got)
	}
}

// TestTransitionLegalityMatrix exercises EVERY (from, method) pair through the service. For each starting
// status it asserts the four transition endpoints (claim→ACCEPTED, accept→IN_PROGRESS, done→DONE,
// cancel→CANCELLED) either advance the task to the expected status (legal) or are rejected with
// ErrIllegalTransition / ErrNoTransition (illegal), matching the canTransition matrix exactly.
func TestTransitionLegalityMatrix(t *testing.T) {
	type move struct {
		name string
		to   string
		call func(svc *Service, id string) (Task, error)
	}
	moves := []move{
		{"claim", StatusAccepted, func(s *Service, id string) (Task, error) { return s.Claim(context.Background(), id, "a") }},
		{"accept", StatusInProgress, func(s *Service, id string) (Task, error) { return s.Accept(context.Background(), id, "a") }},
		{"done", StatusDone, func(s *Service, id string) (Task, error) { return s.Done(context.Background(), id, "a", "") }},
		{"cancel", StatusCancelled, func(s *Service, id string) (Task, error) { return s.Cancel(context.Background(), id, "a", "") }},
	}
	froms := []string{StatusQueued, StatusPushed, StatusAccepted, StatusInProgress, StatusDone, StatusCancelled}

	for _, from := range froms {
		for _, m := range moves {
			t.Run(from+"->"+m.name, func(t *testing.T) {
				svc, store, _ := newSvc(t)
				id := seedAt(t, svc, store, from)
				got, err := m.call(svc, id)
				legal := canTransition(from, m.to)
				if legal {
					if err != nil {
						t.Fatalf("%s->%s should be legal, got err %v", from, m.to, err)
					}
					if got.Status != m.to {
						t.Fatalf("%s->%s: want status %s, got %s", from, m.to, m.to, got.Status)
					}
					return
				}
				// Illegal: either a plain illegal jump, or a no-op when from == to.
				if from == m.to {
					if !errors.Is(err, ErrNoTransition) {
						t.Fatalf("%s->%s (no-op): want ErrNoTransition, got %v", from, m.to, err)
					}
					return
				}
				if !errors.Is(err, ErrIllegalTransition) {
					t.Fatalf("%s->%s should be illegal, got %v", from, m.to, err)
				}
			})
		}
	}
}

// TestLifecycleHappyPath walks QUEUED → ACCEPTED → IN_PROGRESS → DONE through the public endpoints.
func TestLifecycleHappyPath(t *testing.T) {
	svc, store, _ := newSvc(t)
	id := seedAt(t, svc, store, StatusQueued)
	for _, step := range []struct {
		call func() (Task, error)
		want string
	}{
		{func() (Task, error) { return svc.Claim(context.Background(), id, "a") }, StatusAccepted},
		{func() (Task, error) { return svc.Accept(context.Background(), id, "a") }, StatusInProgress},
		{func() (Task, error) { return svc.Done(context.Background(), id, "a", "") }, StatusDone},
	} {
		got, err := step.call()
		if err != nil {
			t.Fatalf("step to %s: %v", step.want, err)
		}
		if got.Status != step.want {
			t.Fatalf("want %s, got %s", step.want, got.Status)
		}
	}
}

func TestListTasks_RejectsBadStatus(t *testing.T) {
	svc, _, _ := newSvc(t)
	if _, err := svc.ListTasks(context.Background(), TaskFilter{Status: "BOGUS"}); err == nil {
		t.Fatal("want bad-request for unknown status filter, got nil")
	}
}

// UX-81 round 2: a task is never born with no branch. An order brief that answers none is refused, with words
// the caller can show, and no task is written.
func TestCreateFromOrderRefusesAnOrderWithNoBranch(t *testing.T) {
	svc, store, notifier := newSvc(t)
	svc.WithOrderReader(fakeOrderReader{orders: map[string]OrderBrief{knownOrder: {ID: knownOrder, BranchID: ""}}})
	_, err := svc.CreateFromOrder(context.Background(), CreateInput{OrderID: knownOrder})
	var br httpx.BadRequestError
	if !errors.As(err, &br) || !strings.Contains(br.Msg, "no branch") {
		t.Fatalf("an order with no branch must be refused in plain words, got %v", err)
	}
	if len(store.tasks) != 0 || len(notifier.posts) != 0 {
		t.Fatalf("a refused create writes nothing and tells no one: %d tasks, %d posts", len(store.tasks), len(notifier.posts))
	}
}

// UX-81 round 3: a task is created only for an order on a branch the caller reaches. The REST create answers an
// order outside the caller's reach EXACTLY as it answers an unknown order, 422 "order not found" (the door's own
// answer for an order it cannot use), so the refusal tells nothing: a 403 "branch out of scope" would confirm the
// order exists on another branch.
func TestCreateRefusesAnOrderOutsideTheCallersBranchReachAsUnknown(t *testing.T) {
	const otherBranch = "99999999-9999-4999-8999-999999999999"
	create := func(sc identity.StaffContext, order string) (*Handler, *fakeStore, int, string) {
		svc, store, _ := newSvc(t)
		h := NewHandler(svc, nil)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"order_id":"`+order+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(identity.ContextWithStaff(r.Context(), sc))
		w := httptest.NewRecorder()
		h.create(w, r, guards.StaffCaller{})
		return h, store, w.Code, strings.TrimSpace(w.Body.String())
	}
	scoped := func(branch string) identity.StaffContext {
		return identity.StaffContext{UserID: "eeeeeeee-0000-4000-8000-000000000001", RoleCode: "YARD_TEAM", Tier: identity.TierFrontline,
			Scope: identity.ScopeBranch, Status: identity.StatusActive, Branches: map[string]bool{branch: true}}
	}
	_, _, unknownCode, unknownBody := create(scoped(branchA), "33333333-3333-4333-8333-333333333333")
	if unknownCode != http.StatusUnprocessableEntity || !strings.Contains(unknownBody, "order not found") {
		t.Fatalf("the unknown order answer is the pattern: %d %s", unknownCode, unknownBody)
	}
	_, store, code, body := create(scoped(otherBranch), knownOrder)
	if code != unknownCode || body != unknownBody {
		t.Errorf("an order on another branch must answer exactly as an unknown order (%d %s), got %d %s", unknownCode, unknownBody, code, body)
	}
	if len(store.tasks) != 0 {
		t.Errorf("a refused create writes nothing: %d tasks", len(store.tasks))
	}
	if _, store, code, body := create(scoped(branchA), knownOrder); code != http.StatusCreated || len(store.tasks) != 1 {
		t.Errorf("the order's own branch still creates: %d %s, %d tasks", code, body, len(store.tasks))
	}
	global := scoped(branchA)
	global.Scope, global.Tier, global.Branches = identity.ScopeGlobal, identity.TierLeadership, nil
	if _, _, code, body := create(global, knownOrder); code != http.StatusCreated {
		t.Errorf("a GLOBAL caller still creates: %d %s", code, body)
	}
}

// orgOrderReader answers an order only inside the org that owns it, the shape of the real adapter (UX-81 round 4).
type orgOrderReader struct{ owner map[string]string } // order id -> owning org

func (o orgOrderReader) OrderBrief(_ context.Context, id string) (OrderBrief, error) {
	return OrderBrief{ID: id, BranchID: branchA}, nil // the unscoped read, which a caller of the org held store never reaches
}

func (o orgOrderReader) OrderBriefInOrg(_ context.Context, orgID, id string) (OrderBrief, error) {
	if o.owner[id] != orgID {
		return OrderBrief{}, ErrUnknownOrder
	}
	return OrderBrief{ID: id, BranchID: branchA}, nil
}

// The REST create hands its caller's org to the order read: another org's order is the 422 an unknown order gets,
// and no task is written (UX-81 round 4).
func TestCreateReadsTheOrderUnderTheCallersOrg(t *testing.T) {
	const north, south = "3ef10000-0000-4000-8000-000000000001", "3ef10000-0000-4000-8000-000000000002"
	svc, store, _ := newSvc(t)
	svc.WithOrderReader(orgOrderReader{owner: map[string]string{knownOrder: south}})
	h := NewHandler(svc, nil)
	global := identity.StaffContext{UserID: "eeeeeeee-0000-4000-8000-000000000001", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive}
	post := func(order, org string) (int, string) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"order_id":"`+order+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(identity.ContextWithStaff(r.Context(), global))
		w := httptest.NewRecorder()
		h.create(w, r, guards.StaffCaller{StaffScope: authz.StaffScope{OrgID: org}})
		return w.Code, strings.TrimSpace(w.Body.String())
	}
	code, body := post(knownOrder, north)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "order not found") || len(store.tasks) != 0 {
		t.Errorf("a GLOBAL caller of org north naming org south's order gets the unknown order 422 and writes nothing: %d %s, %d tasks", code, body, len(store.tasks))
	}
	if code, body := post(knownOrder, south); code != http.StatusCreated {
		t.Errorf("the owning org still creates: %d %s", code, body)
	}
}
