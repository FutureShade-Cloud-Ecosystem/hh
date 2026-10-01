//go:build demo

package demo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
)

// fakeMinter records the subjects it is asked to sign and answers a fixed
// token, or the error it is told to.
type fakeMinter struct {
	subjects []string
	token    string
	err      error
}

func (f *fakeMinter) Mint(subject string) (string, error) {
	f.subjects = append(f.subjects, subject)
	if f.err != nil {
		return "", f.err
	}
	return f.token, nil
}

// postSession drives the session route the way the entry page calls it.
func postSession(t *testing.T, m *Mint, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/demo/session", strings.NewReader(body))
	rsp := httptest.NewRecorder()
	m.Session(rsp, req)
	return rsp
}

// The flow listing answers the closed table, in the table's order, with
// the contract's three fields per row and nothing else: no account name
// and no address on a pre-auth listing.
func TestFlowsAnswersTheClosedTable(t *testing.T) {
	m, err := NewMint(&fakeMinter{token: "t"})
	if err != nil {
		t.Fatalf("NewMint: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/demo/flows", nil)
	rsp := httptest.NewRecorder()
	m.Flows(rsp, req)

	if rsp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rsp.Code)
	}
	var got flowList
	if err := json.Unmarshal(rsp.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not a flow list: %v", err)
	}
	all := flows.All()
	if len(got.Flows) != len(all) {
		t.Fatalf("the listing carries %d flows, want the table's %d", len(got.Flows), len(all))
	}
	for i, row := range got.Flows {
		if row.Flow != all[i].Flow || row.RoleCode != all[i].Role || row.Label != all[i].Label {
			t.Errorf("row %d is %+v, want the table's flow %q, role %q, label %q",
				i, row, all[i].Flow, all[i].Role, all[i].Label)
		}
	}
	// The labels are the flow table's, never the roles table's: the roles
	// catalog spells the pair differently, and the testers' page must read
	// what the table says.
	if got.Flows[0].Label != "Sales" || got.Flows[1].Label != "Sales manager" {
		t.Errorf("labels %q and %q are not the flow table's", got.Flows[0].Label, got.Flows[1].Label)
	}
}

// A session mint for a table flow answers the contract's response: the
// token from the issuer, the flow echoed, and the staff login user block
// field for field, with the role label taken from the flow table.
func TestSessionAnswersTheContractShape(t *testing.T) {
	minter := &fakeMinter{token: "demo-token-1"}
	m, err := NewMint(minter)
	if err != nil {
		t.Fatalf("NewMint: %v", err)
	}
	rsp := postSession(t, m, `{"flow":"sales"}`)

	if rsp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (body: %s)", rsp.Code, rsp.Body.String())
	}
	if len(minter.subjects) != 1 || minter.subjects[0] != "de110000-0000-4000-9000-000000000001" {
		t.Fatalf("the minter was asked to sign %v, want exactly the sales account's platform user id", minter.subjects)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rsp.Body.Bytes(), &raw); err != nil {
		t.Fatalf("body is not a JSON object: %v", err)
	}
	if string(raw["token"]) != `"demo-token-1"` {
		t.Errorf("token %s, want the issuer's", raw["token"])
	}
	if string(raw["flow"]) != `"sales"` {
		t.Errorf("flow %s, want the request's flow echoed", raw["flow"])
	}
	if _, ok := raw["chapter_key"]; ok {
		t.Errorf("the answer carries a chapter_key: the closed table names no walkthrough, the field stays absent")
	}
	var user struct {
		UserID      string `json:"user_id"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		RoleCode    string `json:"role_code"`
		RoleName    string `json:"role_name"`
	}
	if err := json.Unmarshal(raw["user"], &user); err != nil {
		t.Fatalf("the user block does not decode: %v", err)
	}
	account, _ := flows.Get("sales")
	if user.UserID != account.PlatformUserID || user.Email != account.Email ||
		user.DisplayName != account.DisplayName || user.RoleCode != account.Role {
		t.Errorf("user block %+v, want the flow table's account values", user)
	}
	if user.RoleName != "Sales" {
		t.Errorf("role name %q, want the flow table's label, never the roles table's", user.RoleName)
	}
}

// The manager flow mints the manager's credential: the shape W8's
// approval dialog sends on the one call that needs it.
func TestSessionMintsTheManagerFlow(t *testing.T) {
	minter := &fakeMinter{token: "demo-token-2"}
	m, err := NewMint(minter)
	if err != nil {
		t.Fatalf("NewMint: %v", err)
	}
	rsp := postSession(t, m, `{"flow":"sales-manager"}`)
	if rsp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (body: %s)", rsp.Code, rsp.Body.String())
	}
	if len(minter.subjects) != 1 || minter.subjects[0] != "de110000-0000-4000-9000-000000000002" {
		t.Fatalf("the minter was asked to sign %v, want exactly the manager account's platform user id", minter.subjects)
	}
	var got sessionResponse
	if err := json.Unmarshal(rsp.Body.Bytes(), &got); err != nil {
		t.Fatalf("body does not decode: %v", err)
	}
	if got.User.RoleCode != "sales_manager" || got.User.RoleName != "Sales manager" {
		t.Errorf("manager block carries role %q named %q, want the flow table's pair", got.User.RoleCode, got.User.RoleName)
	}
}

// The request carries a flow name and nothing else: a malformed body, a
// missing flow and a name outside the closed table are all 400, and none
// of them reaches the issuer.
func TestSessionRefusesBadRequests(t *testing.T) {
	minter := &fakeMinter{token: "t"}
	m, err := NewMint(minter)
	if err != nil {
		t.Fatalf("NewMint: %v", err)
	}
	cases := []struct {
		name string
		body string
	}{
		{"not JSON", `flow=sales`},
		{"empty object", `{}`},
		{"empty flow", `{"flow":""}`},
		{"unknown flow", `{"flow":"receivables"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rsp := postSession(t, m, c.body)
			if rsp.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400 (body: %s)", rsp.Code, rsp.Body.String())
			}
			var got map[string]string
			if err := json.Unmarshal(rsp.Body.Bytes(), &got); err != nil {
				t.Fatalf("the refusal is not the error envelope: %v", err)
			}
			if got["error"] == "" {
				t.Fatal("the refusal carries no message")
			}
		})
	}
	if len(minter.subjects) != 0 {
		t.Fatalf("the minter was called for %v, a refused request must reach no issuer", minter.subjects)
	}
}

// NewMint refuses to build without its issuer: the contract's own
// condition that the module refuse to register unless the session issuer
// is wired.
func TestNewMintRequiresAnIssuer(t *testing.T) {
	if _, err := NewMint(nil); err == nil {
		t.Fatal("NewMint accepted a nil issuer")
	}
}
