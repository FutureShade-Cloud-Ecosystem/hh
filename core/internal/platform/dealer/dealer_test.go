package dealer

import "testing"

func TestOr_ConfiguredNameWinsAndBlankFallsBack(t *testing.T) {
	t.Cleanup(func() { Set("") })

	Set("")
	if got := Or("your dealer"); got != "your dealer" {
		t.Fatalf("unset: Or = %q, want the neutral word", got)
	}
	Set("   ")
	if got := Or("your dealer"); got != "your dealer" {
		t.Fatalf("blank: Or = %q, want the neutral word", got)
	}
	Set("  Gable Landscape Supply ")
	if got := Name(); got != "Gable Landscape Supply" {
		t.Fatalf("Name = %q, want the trimmed configured name", got)
	}
	if got := Or("your dealer"); got != "Gable Landscape Supply" {
		t.Fatalf("set: Or = %q, want the configured name", got)
	}
}
