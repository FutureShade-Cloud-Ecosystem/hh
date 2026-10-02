//go:build demo

package main

import (
	"strings"
	"testing"
)

// HH-200: the demo build refuses a bad PORT at config load, before the database is touched, like the
// default build (it used to be refused only after the whole graph was built).
func TestLoadDemoServeConfigRefusesABadPortEarly(t *testing.T) {
	get := func(k string) string {
		switch k {
		case "CORE_DATABASE_URL":
			return "postgres://x:x@127.0.0.1:1/demo"
		case "CORE_STAFF_AUDIENCE":
			return "hh-staff"
		case "CORE_TENANT_AUDIENCE":
			return "hh-tenant"
		case "PORT":
			return "abc"
		}
		return ""
	}
	if _, err := loadDemoServeConfig(get); err == nil || !strings.Contains(err.Error(), "PORT") {
		t.Fatalf("loadDemoServeConfig with PORT=abc = %v, want a refusal naming PORT", err)
	}
}
