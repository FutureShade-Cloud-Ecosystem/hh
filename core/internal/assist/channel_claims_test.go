package assist

import (
	"context"
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// membership answers ChannelIDs from a map of user id to channels, or an error.
type membership struct {
	byUser map[string][]string
	err    error
	asked  []string
}

func (m *membership) ChannelIDs(_ context.Context, userID string) ([]string, error) {
	m.asked = append(m.asked, userID)
	if m.err != nil {
		return nil, m.err
	}
	return m.byUser[userID], nil
}

const (
	chA = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	chB = "dddddddd-dddd-dddd-dddd-dddddddddddd"
)

// The claim's channel ids are the messaging module's own answer, read at request time for the asking user.
func TestCallerFor_ChannelIDsAreTheMembershipReadForTheAskingUserAndSigned(t *testing.T) {
	m := &membership{byUser: map[string][]string{"u1": {chB, chA}, "u2": {chA}}}
	h := NewHandler(nil, nil).WithChannelMembership(m)
	c1 := costCaller(t, h, staffCtx("u1", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA))
	if strings.Join(c1.ChannelIDs, ",") != chA+","+chB {
		t.Fatalf("channels = %v, want both, sorted", c1.ChannelIDs)
	}
	claims, err := c1.ReadClaims()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(claims.ChannelIDs, ",") != chA+","+chB {
		t.Errorf("the signed claim carries %v, want both channels", claims.ChannelIDs)
	}
	c2 := costCaller(t, h, staffCtx("u2", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA))
	if strings.Join(c2.ChannelIDs, ",") != chA {
		t.Errorf("another user's channels = %v, want only the one they are in", c2.ChannelIDs)
	}
	if strings.Join(m.asked, ",") != "u1,u2" {
		t.Errorf("the membership was asked for %v, want each asking user once", m.asked)
	}
}

// Every failure is no channel: no read wired, a read that errors, a caller with no role.
func TestCallerFor_ChannelIDsFailClosed(t *testing.T) {
	unwired := NewHandler(nil, nil)
	if c := costCaller(t, unwired, staffCtx("u1", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA)); len(c.ChannelIDs) != 0 {
		t.Errorf("with no membership read wired the caller has channels %v", c.ChannelIDs)
	}
	failing := NewHandler(nil, nil).WithChannelMembership(&membership{err: errors.New("down"), byUser: map[string][]string{"u1": {chA}}})
	if c := costCaller(t, failing, staffCtx("u1", "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, cbA)); len(c.ChannelIDs) != 0 {
		t.Errorf("a failing membership read still gave channels %v", c.ChannelIDs)
	}
	m := &membership{byUser: map[string][]string{"u1": {chA}}}
	noRole := NewHandler(nil, nil).WithChannelMembership(m)
	if c := costCaller(t, noRole, staffCtx("u1", "", 0, identity.ScopeBranch, cbA)); len(c.ChannelIDs) != 0 || len(m.asked) != 0 {
		t.Errorf("a caller with no role has channels %v (asked %v)", c.ChannelIDs, m.asked)
	}
}

// A channel id that is not a uuid never reaches the signer.
func TestReadClaims_ARefusesAChannelIDThatIsNotAUUID(t *testing.T) {
	c := Caller{UserID: "u1", RoleCode: "SALES_MANAGER", Tier: 2, BranchIDs: []string{cbA}, ChannelIDs: []string{"not-a-uuid"}}
	if _, err := c.ReadClaims(); err == nil {
		t.Error("a channel id that is not a uuid was signed")
	}
}
