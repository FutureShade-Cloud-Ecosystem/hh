package assist

import (
	"errors"
	"regexp"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
)

// The assistant reads data only through the assist logins (package readtx), as the
// database role that holds nothing but the ai and ai_pro objects. ReadClaims is
// the one place a resolved Caller becomes the claims the database verifies: the
// scope the Caller already holds, copied, and nothing from the request.

var uuidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ReadClaims builds the staff claims of c: the tier, the branch reach, and the cost and margin fields the
// handler resolved from the dealer's cost rule. PII class and channel fields stay empty until the slices
// that own them fill them from the policy the Caller already passed through.
func (c Caller) ReadClaims() (readtx.Claims, error) {
	if !c.HasRole() {
		return readtx.Claims{}, errors.New("assist: no role, no read claims")
	}
	if c.UserID == "" {
		return readtx.Claims{}, errors.New("assist: no user, no read claims")
	}
	branches := make([]string, 0, len(c.BranchIDs))
	for _, id := range c.BranchIDs {
		if !uuidShape.MatchString(id) {
			return readtx.Claims{}, errors.New("assist: a branch id is not a uuid")
		}
		branches = append(branches, id)
	}
	costBranches := make([]string, 0, len(c.CostBranchIDs))
	for _, id := range c.CostBranchIDs {
		if !uuidShape.MatchString(id) {
			return readtx.Claims{}, errors.New("assist: a cost branch id is not a uuid")
		}
		costBranches = append(costBranches, id)
	}
	poBranches := make([]string, 0, len(c.POCostBranchIDs))
	for _, id := range c.POCostBranchIDs {
		if !uuidShape.MatchString(id) {
			return readtx.Claims{}, errors.New("assist: a purchase cost branch id is not a uuid")
		}
		poBranches = append(poBranches, id)
	}
	channels := make([]string, 0, len(c.ChannelIDs))
	for _, id := range c.ChannelIDs {
		if !uuidShape.MatchString(id) {
			return readtx.Claims{}, errors.New("assist: a channel id is not a uuid")
		}
		channels = append(channels, id)
	}
	return readtx.Claims{
		Audience:        readtx.AudienceStaff,
		AskerID:         c.UserID,
		Tier:            c.Tier,
		Global:          c.Global,
		BranchIDs:       branches,
		CostBranchIDs:   costBranches,
		POCostBranchIDs: poBranches,
		Margin:          c.Margin,
		PaymentTrail:    c.PaymentTrail,
		ChannelIDs:      channels,
	}, nil
}
