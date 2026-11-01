package main

import (
	"os"
	"strings"
	"testing"
)

// TestRT3_TheSyncScopeIsRegisteredWithTheWiredDecider pins plan row 0.47: wireOperations registers the
// approvals sync scope with syncScopeJEDecider (the one constructor that binds the holdback seam), and
// builds no jeDeciderAdapter or bare invoice writer of its own. The mutation this catches is the one the
// PR 762 reader ran: the registration put back to a hand built adapter over billing.NewInvoiceWriter(pool).
func TestRT3_TheSyncScopeIsRegisteredWithTheWiredDecider(t *testing.T) {
	src, err := os.ReadFile("wire_operations.go")
	if err != nil {
		t.Fatalf("read wire_operations.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, `RegisterScope("approvals", identity.NewApprovalsSyncStore(pool, link).`) ||
		!strings.Contains(text, "WithJEDecider(syncScopeJEDecider(pool, spine.glSvc))") {
		t.Error("the approvals sync scope must be registered with syncScopeJEDecider(pool, spine.glSvc)")
	}
	for _, banned := range []string{"jeDeciderAdapter{", "billing.NewInvoiceWriter(pool)"} {
		if strings.Contains(text, banned) {
			t.Errorf("wire_operations.go builds %q by hand; the sync scope's decider comes from syncScopeJEDecider", banned)
		}
	}
}
