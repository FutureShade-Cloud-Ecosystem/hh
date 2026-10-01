//go:build demo

package boot

import (
	"strings"
	"testing"
)

func lookupOf(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

// G2: the box must say demo twice, by name. Every incomplete spelling is
// refused with the offending variable named, including the trap the
// configuration package's dev default sets: APP_ENV unset looks like dev
// to the configuration package and must never look like demo here.
func TestCheckDemoBoxRefusals(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"mode set, environment unset", map[string]string{"DEMO_MODE": "true"}, "APP_ENV"},
		{"mode set, environment dev", map[string]string{"DEMO_MODE": "true", "APP_ENV": "dev"}, "APP_ENV"},
		{"mode set, environment staging", map[string]string{"DEMO_MODE": "true", "APP_ENV": "staging"}, "APP_ENV"},
		{"mode unset entirely", map[string]string{"APP_ENV": "demo"}, "DEMO_MODE"},
		{"mode empty", map[string]string{"DEMO_MODE": "", "APP_ENV": "demo"}, "DEMO_MODE"},
		{"mode near miss", map[string]string{"DEMO_MODE": "True", "APP_ENV": "demo"}, "DEMO_MODE"},
		{"mode yes", map[string]string{"DEMO_MODE": "yes", "APP_ENV": "demo"}, "DEMO_MODE"},
		{"neither set", map[string]string{}, "DEMO_MODE"},
	}
	for _, c := range cases {
		err := CheckDemoBox(lookupOf(c.env))
		if err == nil {
			t.Fatalf("%s: accepted", c.name)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: refusal %q does not name %s", c.name, err, c.want)
		}
	}
}

// The one spelling that opens the door.
func TestCheckDemoBoxAcceptsTheDemoBox(t *testing.T) {
	env := map[string]string{"DEMO_MODE": "true", "APP_ENV": "demo"}
	if err := CheckDemoBox(lookupOf(env)); err != nil {
		t.Fatalf("the demo box itself refused: %v", err)
	}
}
