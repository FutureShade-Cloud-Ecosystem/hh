//go:build demo

// The demo build's subcommand registration, W3's slice of it. W1 owns this
// file's full shape (the demo module and the guards register their pieces
// here); on W3's branch the file did not exist yet, so this hook carries
// ONLY the `demo-populate` registration and nothing else. The lead stitches
// the two branches: fold this init into W1's demo_on.go, keeping the guard
// (an exact match on os.Args[1]) and the exit (the one-shot handles its own
// errors and exits before main's role switch ever sees the argument).
//
// There is deliberately no demo_off.go counterpart on this branch: the
// default build imports nothing under internal/demo and references no demo
// symbol, so it compiles unchanged, and `core demo-populate` on a
// production binary is just an unknown role (main's own refusal).
package main

import (
	"fmt"
	"os"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
)

func init() {
	if len(os.Args) > 1 && os.Args[1] == "demo-populate" {
		if err := demo.PopulateMain(os.Args[2:], os.Stdout, os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}
