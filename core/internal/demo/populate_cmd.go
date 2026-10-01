//go:build demo

// The `core demo-populate` subcommand's own flag parsing, kept beside the
// populate it drives. The subcommand is registered for the demo build only,
// by the hook in core/cmd/core/demo_on.go, which calls PopulateMain; the
// default build has no demo-populate subcommand at all.
package demo

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"
)

// PopulateMain is the subcommand's entrypoint: parse the flags, run the
// populate, report. The only flag is -now, the demoseed flag of the same
// name and shape, so an operator (or a test) can pin the clock the world is
// dated from; without it the wall clock is read exactly once at the start.
func PopulateMain(args []string, out io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("demo-populate", flag.ContinueOnError)
	nowFlag := fs.String("now", "", "RFC3339 instant to date the world from (default: the wall clock, once, at start)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("demo-populate: unexpected argument %q", fs.Arg(0))
	}

	opt := Options{Getenv: getenv, Out: out}
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			return fmt.Errorf("demo-populate: -now must be an RFC3339 instant: %w", err)
		}
		opt.Now = &t
	}

	_, err := RunPopulate(context.Background(), opt)
	return err
}
