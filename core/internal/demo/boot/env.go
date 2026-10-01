//go:build demo

// Package boot holds the demo build's boot guards: the refusals that run
// before the demo serve opens its listener, and the demo-check one-shot
// the entrypoint runs before anything writes. Each guard is a plain
// function over injected inputs (an environment lookup, an environment
// picture, a pool) so every refusal is testable without a box, and the
// guard's test fails when the guard is removed from the wiring.
package boot

import (
	"errors"
	"fmt"
)

// CheckDemoBox refuses any box that has not said demo twice, by name,
// explicitly: DEMO_MODE set, and set to exactly true, and APP_ENV present
// and equal to demo (G2). The lookup is the os.LookupEnv shape, so an
// unset APP_ENV is seen as unset and the configuration package's dev
// default never counts: one flag on a box that set no environment is
// precisely the state that must not open this door. The variables are not
// secrets and are echoed, so an operator who typed True or yes learns at
// boot which spelling missed.
func CheckDemoBox(lookupEnv func(string) (string, bool)) error {
	mode, ok := lookupEnv("DEMO_MODE")
	if !ok || mode != "true" {
		return fmt.Errorf("DEMO_MODE must be set, and to exactly true (it is %s): the demo serve mints staff sessions with no credential, so it boots only where the box says demo twice", stated(ok, mode))
	}
	env, ok := lookupEnv("APP_ENV")
	if !ok {
		return errors.New("APP_ENV must be set to demo explicitly: it is unset, and an unset APP_ENV never counts as demo no matter what DEMO_MODE says")
	}
	if env != "demo" {
		return fmt.Errorf("APP_ENV is %q, want exactly demo: the demo serve mints staff sessions with no credential and runs on no other kind of box", env)
	}
	return nil
}

// stated renders an absent variable for the refusal message.
func stated(ok bool, v string) string {
	if !ok {
		return "unset"
	}
	return fmt.Sprintf("%q", v)
}
