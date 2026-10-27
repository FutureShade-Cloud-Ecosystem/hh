package main

import (
	"os"
	"testing"
)

func TestFW1BranchCensusRedMapNamesRealWriters(t *testing.T) {
	known := make(map[string]bool, len(fw1BranchCensusWriters))
	for _, w := range fw1BranchCensusWriters {
		if known[w] {
			t.Fatalf("duplicate writer %q in fw1BranchCensusWriters", w)
		}
		known[w] = true
	}
	for writer, owner := range fw1BranchCensusRed {
		if !known[writer] {
			t.Errorf("fw1BranchCensusRed names unknown writer %q (stale or misspelled entry)", writer)
		}
		if owner != "FW1-05" && owner != "FW1-08b" {
			t.Errorf("fw1BranchCensusRed[%q] has owner %q, want FW1-05 or FW1-08b", writer, owner)
		}
	}
}

func TestFW1BranchCensusRedEmptyOnWaveFinal(t *testing.T) {
	if os.Getenv("FW1_WAVE_FINAL") != "1" {
		return
	}
	if len(fw1BranchCensusRed) > 0 {
		t.Fatalf("FW1_WAVE_FINAL=1 requires fw1BranchCensusRed to be empty, still has %d entries: %v", len(fw1BranchCensusRed), fw1BranchCensusRed)
	}
}
