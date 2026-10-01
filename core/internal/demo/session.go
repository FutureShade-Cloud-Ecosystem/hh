//go:build demo

// The demo module (W2, the plan's 3.2 piece 1): the tap-a-role sign-in,
// carried from the older app's demo. GET /api/v1/demo/flows lists the
// closed flow table; POST /api/v1/demo/session mints one demo session for
// one named flow. Both routes are pre-auth by design (the contract's
// x-role public, security none), a recorded demo-only exception to D38
// (D49), and they register only in the demo build, at the marked block in
// core/cmd/core/demo_on.go, on the same public leg the tenant config
// uses.
//
// The request carries a flow name and nothing else, so there is nowhere
// to put an email or a role: the closed flow table names the account, the
// token subject and the label, and the account the table names is the
// only thing a mint can answer. Everything after the token is the real
// path: the token is an ordinary staff bearer the unchanged verifier
// classifies, and the org, the role grants and the dealer's role
// configuration hold it per request exactly as they hold a
// platform-issued one.
//
// The response's role label comes from the flow table, never from the
// roles table (the older app's lesson): the table is the one copy of the
// labels the testers see.
package demo

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Minter is the narrow seam the module needs of the demo issuer
// (issuer.Issuer satisfies it): sign one session token for one platform
// user id. The subject is never taken from the request.
type Minter interface {
	Mint(subject string) (string, error)
}

// Mint is the demo module: the two routes the entry page calls.
type Mint struct {
	minter Minter
}

// NewMint validates the collaborators and returns the module.
func NewMint(minter Minter) (*Mint, error) {
	if minter == nil {
		return nil, errors.New("demo: the mint requires a session issuer")
	}
	return &Mint{minter: minter}, nil
}

// flowRow is one DemoFlow of the contract: the flow name, the staff role
// code the flow's account holds, and the label the entry page renders.
// The row carries no account name and no address: the listing is pre-auth
// and minimal on purpose.
type flowRow struct {
	Flow     string `json:"flow"`
	RoleCode string `json:"role_code"`
	Label    string `json:"label"`
}

// flowList is the DemoFlowList envelope.
type flowList struct {
	Flows []flowRow `json:"flows"`
}

// Flows answers GET /api/v1/demo/flows: the closed table, in the table's
// own stable order.
func (m *Mint) Flows(w http.ResponseWriter, _ *http.Request) {
	all := flows.All()
	rows := make([]flowRow, 0, len(all))
	for _, a := range all {
		rows = append(rows, flowRow{Flow: a.Flow, RoleCode: a.Role, Label: a.Label})
	}
	httpx.WriteJSON(w, http.StatusOK, flowList{Flows: rows})
}

// sessionRequest is the DemoSessionRequest: one enum field and nothing
// else, by design.
type sessionRequest struct {
	Flow string `json:"flow"`
}

// sessionUser is the staff login user block (the contract's
// IdentityUser), field for field, so the client's session plumbing needs
// no second shape. The role name is the flow table's label, never the
// roles table's.
type sessionUser struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	RoleCode    string `json:"role_code"`
	RoleName    string `json:"role_name"`
}

// sessionResponse is the DemoSessionResponse. chapter_key stays absent:
// the closed table names no walkthrough, and an absent optional field is
// the honest answer.
type sessionResponse struct {
	Token string      `json:"token"`
	User  sessionUser `json:"user"`
	Flow  string      `json:"flow"`
}

// maxSessionBodyBytes bounds the request body: the contract's request is
// one short string field, so anything larger is not a client of this
// route.
const maxSessionBodyBytes = 4096

// Session answers POST /api/v1/demo/session: mint one session for the
// account one named flow stands for.
func (m *Mint) Session(w http.ResponseWriter, r *http.Request) {
	var in sessionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionBodyBytes))
	if err := dec.Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "demo: the body must be one JSON object carrying the flow name")
		return
	}
	if in.Flow == "" {
		httpx.Error(w, http.StatusBadRequest, "demo: flow is required")
		return
	}
	account, ok := flows.Get(in.Flow)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "demo: unknown flow: the entry page offers only the flows GET /api/v1/demo/flows lists")
		return
	}
	token, err := m.minter.Mint(account.PlatformUserID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "demo: the session could not be minted")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sessionResponse{
		Token: token,
		User: sessionUser{
			UserID:      account.PlatformUserID,
			Email:       account.Email,
			DisplayName: account.DisplayName,
			RoleCode:    account.Role,
			RoleName:    account.Label,
		},
		Flow: account.Flow,
	})
}
