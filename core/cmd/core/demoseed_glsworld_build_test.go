package main

// demoseed_glsworld_build_test.go, the build gate's proof for the demo world seam: the Gable
// Landscape Supply world is the demo build's, and the DEFAULT build of this binary carries
// none of it. The same shape as demoseed_scrubname_build_test.go: the rule is about what
// SHIPS, so the test reads both builds' binaries for the world's own literals, in both
// directions, so neither can pass vacuously.

import (
	"bytes"
	"testing"
)

// glsWorldSignatures are literals the demo world's dictionary and branch convergence carry and
// nothing else a default build compiles: the contractor account's and the vendors' invented
// names, a delivery site, and the dealer's yard name.
var glsWorldSignatures = []string{
	"Summit Ridge Builders",
	"Bluffside Builders",
	"Copperline Pavers",
	"Granite Run Quarry",
	"1180 Quarry Gate Road",
	"Gable Landscape Supply, Point Loma",
	// The money and tax words (demoseed_glsworld_tax_demo.go): the default build keeps the first
	// dealer's rate, currency and ledger words.
	"San Diego County combined sales tax rate.",
	"Retired exemption profile",
	"Sales tax relief recoverable",
}

func TestDefaultBuildCarriesNoGableLandscapeSupplyWorld(t *testing.T) {
	def := buildCoreBinary(t, "core-default-glsworld", "")
	for _, sig := range glsWorldSignatures {
		if bytes.Contains(def, []byte(sig)) {
			t.Errorf("the default build carries the demo world's %q; the world must compile only into the demo build", sig)
		}
	}
	demo := buildCoreBinary(t, "core-demo-glsworld", "-tags=demo")
	for _, sig := range glsWorldSignatures {
		if !bytes.Contains(demo, []byte(sig)) {
			t.Errorf("the demo build has lost the demo world's %q, so the default leg above passes vacuously", sig)
		}
	}
}
