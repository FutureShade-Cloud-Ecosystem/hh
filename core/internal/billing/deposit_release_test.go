package billing

// deposit_release_test.go, the release loop's failure behaviour: a failed
// document never drops the next one (review P2-1), driven over a fixed id set
// with a fake emitter because the SQL half is proved against the database.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReleaseEachContinuesPastAFailedDocument(t *testing.T) {
	var emitted []string
	err := releaseEach(context.Background(), []string{"d1", "d2"}, func(_ context.Context, id string) error {
		if id == "d1" {
			return errors.New("first boom")
		}
		emitted = append(emitted, id)
		return nil
	})
	if len(emitted) != 1 || emitted[0] != "d2" {
		t.Fatalf("a failed first document dropped the second: emitted %v", emitted)
	}
	if err == nil || !strings.Contains(err.Error(), "first boom") || !strings.Contains(err.Error(), "d1") {
		t.Fatalf("the failure must be reported naming the document, got %v", err)
	}
}

func TestReleaseEachReportsNothingWhenAllEmit(t *testing.T) {
	n := 0
	if err := releaseEach(context.Background(), []string{"d1", "d2"}, func(context.Context, string) error { n++; return nil }); err != nil || n != 2 {
		t.Fatalf("emitted %d, err %v; want both and no error", n, err)
	}
}
