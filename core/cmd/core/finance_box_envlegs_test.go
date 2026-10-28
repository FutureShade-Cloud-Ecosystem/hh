package main

// The finance box boot guard's environment legs, unit side (no database):
// a copy that claims the finance box refuses the domain allow list on its
// own, so a box started past the entrypoint, with core serve alone, still
// refuses a non empty CORE_SIGNIN_EMAIL_DOMAINS (the S3 read's P3 2: only
// the entrypoint refused it).

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
)

func TestFinanceBoxEnvironmentLegsRefuseTheDomainAllowList(t *testing.T) {
	err := financeBoxBootGuard(financeBoxGetenv(map[string]string{
		"HH_BOX_KIND":         "finance",
		envSigninEmailDomains: "example.test",
	}), config.Config{Env: "staging"}, nil)
	if err == nil || !strings.Contains(err.Error(), envSigninEmailDomains) {
		t.Fatalf("a finance box with a non empty domain allow list must refuse naming %s, got %v", envSigninEmailDomains, err)
	}

	// A set and empty name is the absence of the list, and must pass the leg.
	if err := financeBoxBootGuard(financeBoxGetenv(map[string]string{
		"HH_BOX_KIND":         "finance",
		envSigninEmailDomains: "",
	}), config.Config{Env: "staging"}, nil); err != nil {
		t.Fatalf("a set and empty domain allow list is no list at all, got %v", err)
	}

	// A copy that does not claim the finance box is the ordinary world's
	// business: the leg is inert there.
	if err := financeBoxBootGuard(financeBoxGetenv(map[string]string{
		envSigninEmailDomains: "example.test",
	}), config.Config{Env: "staging"}, nil); err != nil {
		t.Fatalf("the domain refusal is the finance box's alone, got %v", err)
	}
}
