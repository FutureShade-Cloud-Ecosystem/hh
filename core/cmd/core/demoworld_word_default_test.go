//go:build !demo

package main

// worldWord is the word the seeded demo world reads for a plan literal: in the default build
// the plan's own literal, in the demo build the Gable Landscape Supply word (the
// demoworld_word_demo_test.go side). The world proofs that run in both builds name the plan's
// SKUs through it, so one proof holds in both.
func worldWord(s string) string { return s }
