//go:build demo

package main

import "testing"

func TestCI1ProofPlantedFailure(t *testing.T) {
	t.Fatal("CI-1 proof: planted failure in a demo-tagged test")
}
