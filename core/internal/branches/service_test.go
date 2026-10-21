package branches

import (
	"context"
	"testing"
)

// fakeStore is an in-memory branchStore for the unit tests.
type fakeStore struct {
	list      []Branch
	defaultID string
	existsErr error
}

func (f *fakeStore) List(context.Context) ([]Branch, error)              { return f.list, nil }
func (f *fakeStore) ListInOrg(context.Context, string) ([]Branch, error) { return f.list, nil }
func (f *fakeStore) DefaultBranchID(context.Context) (string, error)     { return f.defaultID, nil }

// Exists answers off the same fixture list, so a test cannot accidentally set up a branch that the
// list has and the existence read does not.
func (f *fakeStore) Exists(_ context.Context, id string) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	for _, b := range f.list {
		if b.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// Names answers off the same fixture list, the display name the list carries.
func (f *fakeStore) Names(_ context.Context, ids []string) (map[string]string, error) {
	names := make(map[string]string, len(ids))
	for _, id := range ids {
		for _, b := range f.list {
			if b.ID == id {
				names[id] = b.Name
				break
			}
		}
	}
	return names, nil
}

// TestService_ListPassesThrough proves the service returns both branches from the store
// unchanged (the list is a thin read seam).
func TestService_ListPassesThrough(t *testing.T) {
	fs := &fakeStore{list: []Branch{
		{ID: "b-trt", Code: "TRT", Name: "Trenton", IsDefault: true},
		{ID: "b-kgn", Code: "KGN", Name: "Kingston"},
	}, defaultID: "b-trt"}
	svc := NewService(fs)

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d branches, want 2", len(got))
	}
	if !got[0].IsDefault || got[1].IsDefault {
		t.Fatalf("default flag not carried through: %+v", got)
	}
}

// TestService_DefaultResolves proves DefaultBranchID surfaces the configured default.
func TestService_DefaultResolves(t *testing.T) {
	svc := NewService(&fakeStore{defaultID: "b-trt"})
	id, err := svc.DefaultBranchID(context.Background())
	if err != nil {
		t.Fatalf("DefaultBranchID: %v", err)
	}
	if id != "b-trt" {
		t.Fatalf("default = %q, want b-trt", id)
	}
}
