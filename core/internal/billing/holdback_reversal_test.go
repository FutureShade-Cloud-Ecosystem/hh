package billing

import (
	"context"
	"errors"
	"testing"
)

// RT-3 fix, item 4: only a Controller reverses a release, and only with a reason.
func TestHoldbackReversal_OnlyAControllerWithAReason(t *testing.T) {
	s := NewHoldbackReleaseService(nil)
	_, err := s.Reverse(context.Background(), HoldbackReversalInput{ReleaseID: "00000000-0000-4000-8000-000000000001", Reason: "x"})
	var refused HoldbackReleaseRefused
	if !errors.As(err, &refused) || refused.Kind != RefusedNotController {
		t.Fatalf("a non controller must be refused as such, got %v", err)
	}
	_, err = s.Reverse(context.Background(), HoldbackReversalInput{ReleaseID: "00000000-0000-4000-8000-000000000001", Reason: "  ", IsController: true})
	if err == nil || errors.As(err, &refused) {
		t.Fatalf("an empty reason must be a bad request, got %v", err)
	}
}
