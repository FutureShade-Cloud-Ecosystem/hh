// Command core is the hh modular monolith binary. The App Shape gives it three
// roles: serve, worker and migrate. All three are no-ops here; this file exists
// so the skeleton builds and the role contract is fixed from the first commit.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: core <serve|worker|migrate>")
		os.Exit(2)
	}
	switch role := os.Args[1]; role {
	case "serve", "worker", "migrate":
		fmt.Printf("core: role %q is a skeleton no-op\n", role)
	default:
		fmt.Fprintf(os.Stderr, "core: unknown role %q (want serve, worker or migrate)\n", role)
		os.Exit(2)
	}
}
