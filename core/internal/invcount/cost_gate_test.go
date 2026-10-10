package invcount

// cost_gate_test.go: the X3 cost gate over the counts' negative-stock
// read, the read the yard team reaches through Buying and Stock. The
// seam behind the read is the kit's double (the gate is the marshal
// layer's, proved here without a database); the inventory module's own
// test walks its stock reads against the real repository.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// costGateStock is the read-only double behind the negative-stock read.
type costGateStock struct{ snaps []Snapshot }

func (s *costGateStock) SnapshotOnHand(context.Context, string) ([]Snapshot, error) {
	return s.snaps, nil
}
func (s *costGateStock) SnapshotOnHandTx(context.Context, pgx.Tx, string) ([]Snapshot, error) {
	return s.snaps, nil
}
func (s *costGateStock) WeightedAvgCostCents(context.Context, string, string) (int64, error) {
	return 0, nil
}
func (s *costGateStock) WeightedAvgCostCentsTx(context.Context, pgx.Tx, string, string) (int64, error) {
	return 0, nil
}
func (s *costGateStock) PostCountAdjustmentTx(context.Context, pgx.Tx, string, string, string, float64, int64, string) (string, error) {
	return "", nil
}

// costGateGet drives the negative-stock handler under one stamped role
// and answers whether the cost key is present in the first row.
func costGateGet(t *testing.T, h *Handler, role string) (bool, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/counts/negative-stock", nil)
	req = req.WithContext(identity.ContextWithStaff(req.Context(), identity.StaffContext{
		RoleCode: role, Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Status: identity.StatusActive,
	}))
	w := httptest.NewRecorder()
	h.negativeStock(w, req, guards.StaffCaller{})
	if w.Code != http.StatusOK {
		t.Fatalf("negative-stock answered %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	raw, ok := body["items"].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("no items in %v", body)
	}
	row, ok := raw[0].(map[string]any)
	if !ok {
		t.Fatalf("item is not an object: %v", raw[0])
	}
	_, present := row["avg_cost_cents"]
	return present, row
}

// TestInvCountCostGate_NegativeStockHoldsCostOutByRole: the negative
// stock worklist's cost leg is byte-absent for a yard caller and
// present for a counter caller on the cost and margin list.
func TestInvCountCostGate_NegativeStockHoldsCostOutByRole(t *testing.T) {
	roles, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	svc := NewService(nil).WithStockCounter(&costGateStock{snaps: []Snapshot{
		{ProductID: "00000000-0000-4000-8000-0000000000a1", LocationID: "00000000-0000-4000-8000-000000000002",
			OnHandBase: -2, AvgCostCents: 4321},
	}})
	h := NewHandler(svc, nil).WithCostRoles(roles)

	if present, row := costGateGet(t, h, "YARD_TEAM"); present {
		t.Errorf("the yard caller's negative-stock row carries avg_cost_cents: %v", row)
	}
	if present, row := costGateGet(t, h, "INSIDE_SALES"); !present {
		t.Errorf("the counter caller's negative-stock row lost avg_cost_cents: %v", row)
	}
}
