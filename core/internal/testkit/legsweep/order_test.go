package legsweep

import (
	"reflect"
	"testing"
)

// The sweep's delete order, with no database: children before parents, and on a foreign key
// cycle only the cycle's own tables are emitted when the order stalls.
func TestSweepOrder(t *testing.T) {
	// a <- b <- c (c points at b, b points at a); d is alone.
	got := sweepOrder([]string{"a", "b", "c", "d"}, map[string]map[string]bool{
		"b": {"a": true}, "c": {"b": true},
	})
	if want := []string{"c", "d", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("chain order = %v, want %v", got, want)
	}

	// x and y point at each other; p is a parent of x (above the cycle), k is a child of y
	// (downstream of the cycle, so it must be deleted before either cycle member).
	got = sweepOrder([]string{"k", "p", "x", "y"}, map[string]map[string]bool{
		"x": {"y": true, "p": true}, "y": {"x": true}, "k": {"y": true},
	})
	if want := []string{"k", "x", "y", "p"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cycle order = %v, want %v", got, want)
	}
}
