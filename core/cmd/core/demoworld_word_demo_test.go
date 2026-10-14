//go:build demo

package main

// worldWord is the word the seeded demo world reads for a plan literal: the Gable Landscape
// Supply word in the demo build, the plan's own literal in the default build (the
// demoworld_word_default_test.go side).
func worldWord(s string) string { return glsApply(s) }
