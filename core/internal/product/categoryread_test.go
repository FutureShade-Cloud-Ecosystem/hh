package product

// categoryread_test.go, the category list's paging rule without a database: the limit+1 walk, the
// org the caller's own reaching the reader, and the fail closed answer of an unwired service.

import (
	"context"
	"errors"
	"testing"
)

type fakeCategories struct {
	rows     []Category
	gotOrg   string
	gotLimit int
	gotAfter string
}

func (f *fakeCategories) ListCategories(_ context.Context, orgID string, limit int, after string) ([]Category, error) {
	f.gotOrg, f.gotLimit, f.gotAfter = orgID, limit, after
	var out []Category
	for _, c := range f.rows {
		if after == "" || c.Code > after {
			out = append(out, c)
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func TestCategoriesPage_WalksByCodeAndCarriesTheOrg(t *testing.T) {
	f := &fakeCategories{rows: []Category{{Code: "A"}, {Code: "B"}, {Code: "C"}}}
	svc := &Service{categories: f}
	rows, next, err := svc.CategoriesPage(context.Background(), "org-1", 2, "")
	if err != nil || len(rows) != 2 || next != "B" {
		t.Fatalf("first page rows=%v next=%q err=%v, want A,B and next B", rows, next, err)
	}
	if f.gotOrg != "org-1" || f.gotLimit != 3 {
		t.Fatalf("the reader was asked org %q limit %d, want the caller's org and limit+1", f.gotOrg, f.gotLimit)
	}
	rows, next, err = svc.CategoriesPage(context.Background(), "org-1", 2, next)
	if err != nil || len(rows) != 1 || rows[0].Code != "C" || next != "" {
		t.Fatalf("last page rows=%v next=%q err=%v, want C and no next", rows, next, err)
	}
}

func TestCategoriesPage_UnwiredIsNotAnEmptyList(t *testing.T) {
	if _, _, err := (&Service{}).CategoriesPage(context.Background(), "org-1", 10, ""); !errors.Is(err, errNoCategoryReader) {
		t.Fatalf("err = %v, want errNoCategoryReader", err)
	}
}
