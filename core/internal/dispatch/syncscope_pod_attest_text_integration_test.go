//go:build integration

// syncscope_pod_attest_text_integration_test.go: the typed name of an attestation is client text, and
// the rule for it lives in CapturePOD so both doors share it. A name the rule refuses is the op's
// rejected result (never an operational fault that fails the push again on every replay), attests
// nothing, and reaches the signature provider never.
package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// unsafeNames are the typed names CapturePOD refuses: over 200 bytes (the signature provider's own
// bound), and every control character class (C0, DEL, the bidi controls).
func unsafeNames() map[string]string {
	return map[string]string{
		"201 ascii bytes":         strings.Repeat("e", 201),
		"101 accented letters":    strings.Repeat("é", 101),
		"NUL inside":              "Site\x00Foreman",
		"newline inside":          "Site\nForeman",
		"tab inside":              "Site\tForeman",
		"bell inside":             "Site\x07Foreman",
		"DEL inside":              "Site\x7fForeman",
		"right to left override":  "Site Foreman‮",
		"left to right isolate":   "Site ⁦Foreman",
		"pop directional isolate": "Site Foreman⁩",
	}
}

func TestPodAttest_UnsafeNamesAreRejectedPerOpAndAttestNothing(t *testing.T) {
	info, ok := sync.Lookup("pod")
	if !ok {
		t.Fatal("pod missing from the registry")
	}
	for name, typed := range unsafeNames() {
		t.Run(name, func(t *testing.T) {
			a := newAttestWorld(t)
			resp, err := a.engine.Push(context.Background(), info, a.driverA, []apigen.SyncPushOperation{attestOp(syncFreshKey(), a.stopA2, typed, true)})
			if err != nil {
				t.Fatalf("a client fault must be the op's result, not a failed push: %v", err)
			}
			if len(resp.Results) != 1 || resp.Results[0].Status != "rejected" || errText(resp.Results[0]) == "" {
				t.Fatalf("want rejected with words, got %+v", resp.Results)
			}
			if len(a.h.signer.calls) != 0 {
				t.Fatalf("a refused name must never reach the signature provider: %d calls", len(a.h.signer.calls))
			}
			if got := a.stop(a.stopA2); got.status != StopEnRoute || got.receivedBy != "" {
				t.Fatalf("a refused name attests nothing: %+v", got)
			}
		})
	}
}

func TestPodAttest_TwoHundredBytesIsStillAccepted(t *testing.T) {
	a := newAttestWorld(t)
	name := strings.Repeat("é", 100)
	if res := a.push(a.driverA, attestOp(syncFreshKey(), a.stopA2, name, true)); res[0].Status != "applied" {
		t.Fatalf("exactly 200 bytes is the bound and is accepted: %+v (%s)", res[0], errText(res[0]))
	}
}

func TestCapturePOD_UnsafeNamesAreTheDirectDoorsBadRequest(t *testing.T) {
	for name, typed := range unsafeNames() {
		t.Run(name, func(t *testing.T) {
			a := newAttestWorld(t)
			directCtx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
				UserID: testkit.UserDriverID, RoleCode: identity.RoleDrivers, Tier: identity.TierFrontline,
				Scope: identity.ScopeBranch, Status: identity.StatusActive, Branches: map[string]bool{testkit.BranchNorthAID: true},
			})
			_, err := a.h.svc.CapturePOD(directCtx, a.stopA, PODInput{ReceivedBy: typed, Affirmed: true})
			var bad httpx.BadRequestError
			if !errors.As(err, &bad) {
				t.Fatalf("want the module's BadRequest, got %v", err)
			}
			if len(a.h.signer.calls) != 0 {
				t.Fatalf("a refused name must never reach the signature provider: %d calls", len(a.h.signer.calls))
			}
		})
	}
}
