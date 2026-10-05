package gl

// post_external_tx_test.go, DB-free: the transaction variants validate exactly what the pool variants do, and
// ResolveRuleTx keeps ResolveRule's loud refusal for a role with no rule.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPostExternalTx_ValidatesLikePostExternal(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(newFakeStore())

	cases := map[string]ExternalJournal{
		"no lines":   {Source: SourceAdjustment, IdempotencyKey: "k1"},
		"unbalanced": {Source: SourceAdjustment, IdempotencyKey: "k2", Lines: []ExternalLine{{AccountCode: "1000", DebitCents: 100}, {AccountCode: "1020", CreditCents: 90}}},
		"negative":   {Source: SourceAdjustment, IdempotencyKey: "k3", Lines: []ExternalLine{{AccountCode: "1000", DebitCents: -9}, {AccountCode: "1020", CreditCents: -9}}},
	}
	for name, in := range cases {
		_, poolErr := svc.PostExternal(ctx, in)
		_, txErr := svc.PostExternalTx(ctx, nil, in)
		if poolErr == nil || txErr == nil {
			t.Fatalf("%s: pool %v, tx %v, both must refuse", name, poolErr, txErr)
		}
		if poolErr.Error() != txErr.Error() || errors.Is(poolErr, errUnbalanced) != errors.Is(txErr, errUnbalanced) {
			t.Fatalf("%s: pool refuses with %q, tx with %q", name, poolErr, txErr)
		}
	}
}

func TestResolveRuleTx_MissingRoleIsRefusedLoudly(t *testing.T) {
	svc := newTestService(newFakeStore())
	_, err := svc.ResolveRuleTx(context.Background(), nil, "vendor.bill.posted", "no_such_role", "")
	if err == nil || !strings.Contains(err.Error(), "no posting rule for vendor.bill.posted/no_such_role") {
		t.Fatalf("missing role = %v, want the loud no-rule refusal", err)
	}
}
