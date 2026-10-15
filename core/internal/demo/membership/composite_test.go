//go:build demo

package membership

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
)

// recordingConsole stands for FB Console behind its cache: it counts every
// question it is asked and answers membership for one real looking id.
type recordingConsole struct {
	orgCalls, tenantCalls int
	seen                  []string
	err                   error
}

const consoleMemberID = "66a1b2c3d4e5f60718293a4b"

func (c *recordingConsole) OrgMember(_ context.Context, id, org string) (bool, error) {
	c.orgCalls++
	c.seen = append(c.seen, id)
	return id == consoleMemberID && org == flows.OrgSlug, c.err
}

func (c *recordingConsole) TenantOrgs(_ context.Context, id string) ([]string, error) {
	c.tenantCalls++
	c.seen = append(c.seen, id)
	return []string{"hh-demo"}, c.err
}

func TestCompositeAnswersEnterablePersonasFromTheStaticSource(t *testing.T) {
	console := &recordingConsole{}
	c, err := NewComposite(console)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range flows.Personas() {
		got, err := c.OrgMember(context.Background(), p.Subject(), flows.OrgSlug)
		if err != nil {
			t.Fatal(err)
		}
		if got == p.Contractor {
			t.Fatalf("persona %s member=%v contractor=%v", p.ID, got, p.Contractor)
		}
		if other, _ := c.OrgMember(context.Background(), p.Subject(), "some-other-dealer"); other {
			t.Fatalf("persona %s is a member of another org", p.ID)
		}
	}
	if console.orgCalls != 0 {
		t.Fatalf("a persona subject reached the console source %d times", console.orgCalls)
	}
}

// A persona: subject can never reach the console source, listed or not,
// whatever the org, and a colon in any position keeps a subject off it.
func TestAColonSubjectNeverReachesTheConsoleSource(t *testing.T) {
	console := &recordingConsole{}
	c, _ := NewComposite(console)
	for _, sub := range []string{
		"persona:sales", "persona:nobody", "persona:", "PERSONA:sales", "persona:sales:x",
		consoleMemberID + ":x", ":" + consoleMemberID, "a:b", ":", "tenant:hh-demo:x", " persona:sales",
	} {
		for _, org := range []string{flows.OrgSlug, "other"} {
			got, err := c.OrgMember(context.Background(), sub, org)
			if err != nil {
				t.Fatal(err)
			}
			if sub != "persona:sales" && got {
				t.Fatalf("%q is a member of %s", sub, org)
			}
		}
		orgs, err := c.TenantOrgs(context.Background(), sub)
		if err != nil || len(orgs) != 0 {
			t.Fatalf("%q tenant orgs %v %v", sub, orgs, err)
		}
	}
	if console.orgCalls != 0 || console.tenantCalls != 0 {
		t.Fatalf("console reached %d+%d times, seen %v", console.orgCalls, console.tenantCalls, console.seen)
	}
}

// Every other subject goes to the console source, and a console id can
// never match the static map: a console id holds no colon.
func TestEveryColonlessSubjectGoesToTheConsoleSource(t *testing.T) {
	console := &recordingConsole{}
	c, _ := NewComposite(console)
	got, err := c.OrgMember(context.Background(), consoleMemberID, flows.OrgSlug)
	if err != nil || !got {
		t.Fatalf("console member: %v %v", got, err)
	}
	if got, _ := c.OrgMember(context.Background(), "someone-else", flows.OrgSlug); got {
		t.Fatal("an unknown console id is a member")
	}
	if _, err := c.TenantOrgs(context.Background(), consoleMemberID); err != nil {
		t.Fatal(err)
	}
	if console.orgCalls != 2 || console.tenantCalls != 1 {
		t.Fatalf("calls %d %d", console.orgCalls, console.tenantCalls)
	}
	for _, id := range console.seen {
		if strings.Contains(id, ":") {
			t.Fatalf("console was asked about %q", id)
		}
	}
	// No flow id and no persona id equals a colonless console id by accident:
	// the static map keys all hold a colon.
	for _, p := range flows.Personas() {
		if !strings.Contains(p.Subject(), ":") {
			t.Fatalf("persona subject %q holds no colon", p.Subject())
		}
	}
}

func TestConsoleErrorsPassThroughAndFailClosed(t *testing.T) {
	boom := errors.New("boom")
	c, _ := NewComposite(&recordingConsole{err: boom})
	if _, err := c.OrgMember(context.Background(), consoleMemberID, flows.OrgSlug); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	if _, err := c.TenantOrgs(context.Background(), consoleMemberID); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
}

func TestCompositeNeedsAConsoleSource(t *testing.T) {
	if _, err := NewComposite(nil); err == nil {
		t.Fatal("a nil console source was accepted")
	}
}
