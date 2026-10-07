package pos

// refund_limit_off_test.go, the dealer with no refund approval threshold at
// all (the Dibbits ruling): refund_approval_limit_enabled false in the
// dealer's configuration means no refund, of any size, ever asks for an
// approver, the clerk's own return committing through the rail with no
// refusal row. The mirror leg holds the other side of the ruling: a dealer
// who sets a number keeps the gate exactly as it stood, the same return
// asking for one of the configured approvers. Both legs rewrite the decided
// default document through Validate, the refund rule file's own round.

import (
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// The Dibbits ruling's switch, off: a whole return in the thousands, far
// above any amount a threshold dealer configures, commits as the clerk with
// no approver, no refusal row, the rail touched once.
func TestRefundRule_LimitOff_AnySizeCommitsWithNoApprover(t *testing.T) {
	h := newHarness()
	seedSaleWorth(h, 900000, 0) // the 9000.00 whole return, the ruling's top end
	h.svc = h.svc.WithRoleConfig(modifiedDefault(t, roleconfig.SettingRefundApprovalLimitEnabled, false))
	h.caller = salesCaller()

	out, err := h.svc.Return(ctxWithActor("u-cashier"), wholeReturnInput())
	if err != nil {
		t.Fatalf("under no limit a return of any size must commit with no approver: %v", err)
	}
	if out.TotalCents != 900000 || !h.tender.refundCalled {
		t.Fatalf("the return must commit through the rail: %+v", out)
	}
	for _, row := range h.rec.rows {
		if row.Action == auditActionRefundApprovalRefused {
			t.Fatal("no refusal row may exist under no limit, no approver was ever asked for")
		}
	}
}

// The mirror, a dealer who sets a number: the same sale, the same clerk, the
// decided default's amount, and the whole return asks for one of the
// configured approvers exactly as it did before the switch existed.
func TestRefundRule_LimitOn_TheSameReturnStillAsks(t *testing.T) {
	h := newHarness()
	seedSaleWorth(h, 900000, 0)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	h.caller = salesCaller()

	_, err := h.svc.Return(ctxWithActor("u-cashier"), wholeReturnInput())
	var refusal ErrRefundApprovalRequired
	if !errors.As(err, &refusal) {
		t.Fatalf("under a configured amount the same return must still ask, got %v", err)
	}
	if refusal.AmountCents != 25000 {
		t.Fatalf("the refusal must keep naming the configured 250.00: %d", refusal.AmountCents)
	}
}
