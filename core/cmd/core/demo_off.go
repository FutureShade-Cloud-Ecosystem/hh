//go:build !demo

// The default build's side of the entrypoint seam: this binary carries no
// demo role and no demo code. The stub imports nothing under
// core/internal/demo/ and answers false for every role, so main falls
// through to the standard role switch and behaves exactly as it always
// has. The default-build test beside this file (demo_build_guard_test.go)
// holds that shape in place: it fails if any package under
// core/internal/demo/ ever enters this build's dependency closure (G1),
// and the binary-level review command counts the demo symbols in the
// compiled default build and expects none.
package main

// demoDispatch reports whether a demo-only role ran. In the default
// build none ever does.
func demoDispatch(string, []string) bool { return false }
