package assist

// capability_test.go, DIB-465. The assistant's answer to "what can you do?".
//
// MUTATION TARGET recorded in NOTES-FIX-POLISH.md (M3): compose capabilityAnswer from
// BuiltinCommands() instead of from the caller's offered tools, and
// `the capability answer never names a command the caller cannot run` dies.

import (
	"context"
	"strings"
	"testing"
)

// counterTools is what a frontline (tier 3) caller is offered: no money commands.
func counterTools(t *testing.T) []ToolSpec {
	t.Helper()
	reg := NewRegistry(Deps{}, nil)
	return toolSpecsFor(reg.List(context.Background(), Caller{UserID: "u", RoleCode: "counter", Tier: 3}))
}

func managerTools(t *testing.T) []ToolSpec {
	t.Helper()
	reg := NewRegistry(Deps{}, nil)
	return toolSpecsFor(reg.List(context.Background(), Caller{UserID: "u", RoleCode: "mgr", Tier: 2}))
}

func TestCapabilityAnswerNeverNamesACommandTheCallerCannotRun(t *testing.T) {
	tools := counterTools(t)
	for _, spec := range tools {
		if spec.Name == "payment.lookup" || spec.Name == "customer.balance" {
			t.Fatalf("precondition: a tier-3 caller must not be offered %s", spec.Name)
		}
	}

	// The bare question, the write refusal, and a "no" all OFFER the menu. Every one of them.
	//
	// DIB-513 relocated WHERE the menu is, not whether it is fenced. It used to be prose in the
	// reply text, so this test read the prose; it is now Reply.Suggest, the codes the surface renders
	// as chips, so this test reads the codes. The invariant is untouched and the check is now exact
	// rather than substring-matched: a caller can be offered a command if and only if its code is in
	// their own filtered tool set.
	replies := []Reply{
		capabilityReply(CapabilityQuestion{}, tools),
		capabilityReply(CapabilityQuestion{Subject: "delete an order"}, tools),
		capabilityReply(CapabilityQuestion{Subject: "juggle"}, tools),
		capabilityReply(CapabilityQuestion{Subject: "tell me what a customer owes"}, tools),
	}
	allowed := map[string]bool{}
	for _, spec := range tools {
		allowed[spec.Name] = true
	}
	for i, r := range replies {
		for _, forbidden := range []string{"Customer balance", "Find a payment", "payment.lookup", "customer.balance"} {
			if strings.Contains(r.Text, forbidden) {
				t.Fatalf("answer %d names %q in its prose, which this caller may not run:\n%s", i, forbidden, r.Text)
			}
		}
		for _, code := range r.Suggest {
			if !allowed[code] {
				t.Fatalf("answer %d offers %q as a chip, which this caller may not run", i, code)
			}
		}
	}
	// …and it does offer the ones they CAN run, or it is not an answer.
	offered := map[string]bool{}
	for _, code := range replies[0].Suggest {
		offered[code] = true
	}
	if !offered["order.lookup"] || !offered["stock.lookup"] {
		t.Fatalf("the bare capability answer must offer the caller's own commands, got %v", replies[0].Suggest)
	}
}

func TestCapabilityAnswerTracksTheCallersTier(t *testing.T) {
	q := CapabilityQuestion{Subject: "tell me what a customer owes"}
	counter := capabilityReply(q, counterTools(t)).Text
	manager := capabilityReply(q, managerTools(t)).Text

	if !strings.HasPrefix(counter, "No") {
		t.Fatalf("a frontline caller must be told no, customer.balance is manager-and-above:\n%s", counter)
	}
	if !strings.HasPrefix(manager, "Yes") {
		t.Fatalf("a manager must be told yes, from the same code and the same sentence:\n%s", manager)
	}
	if !strings.Contains(manager, "Customer balance") {
		t.Fatalf("the yes should name the command:\n%s", manager)
	}
}

func TestAWriteRequestIsAnsweredNoWhateverItNames(t *testing.T) {
	tools := managerTools(t)
	for _, subject := range []string{
		"cancel an order",
		"create a new order",
		"change a customer's balance",
		"email a payment receipt",
		"delete stock",
	} {
		ans := capabilityReply(CapabilityQuestion{Subject: subject}, tools).Text
		if !strings.HasPrefix(ans, ReadOnlyAnswer) {
			t.Fatalf("%q must be refused as a write, not matched to a lookup:\n%s", subject, ans)
		}
	}
}

func TestACapabilityQuestionWithARealTargetIsARequestNotAQuestion(t *testing.T) {
	p := NewStubProvider()
	tools := managerTools(t)

	// "can you check stock for TB-BLU60?" is polite phrasing for DO IT.
	reply, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Text: "can you check stock for TB-BLU60?"}},
		Tools:    tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.ToolCalls) != 1 || reply.ToolCalls[0].Command != "stock.lookup" {
		t.Fatalf("a capability opener with a SKU in it must run the command, not describe it: %+v", reply)
	}

	// …and the same sentence with no SKU is a genuine question.
	reply2, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Text: "can you check stock?"}},
		Tools:    tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply2.ToolCalls) != 0 {
		t.Fatalf("a bare capability question must not run a command: %+v", reply2)
	}
	if !strings.HasPrefix(reply2.Text, "Yes") || !strings.Contains(reply2.Text, "Check stock") {
		t.Fatalf("expected a yes naming the stock command, got: %s", reply2.Text)
	}
}

func TestTheStubAnswersWhatCanYouDoInsteadOfRefusing(t *testing.T) {
	p := NewStubProvider()
	tools := managerTools(t)
	for _, q := range []string{
		"what can you do?",
		"What can you do for me",
		"what can I ask you?",
		"what are your capabilities",
		"how can you help?",
	} {
		reply, err := p.Complete(context.Background(), CompletionRequest{
			Messages: []Message{{Role: RoleUser, Text: q}},
			Tools:    tools,
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(reply.Text, "I can't answer that one yet") {
			t.Fatalf("%q must not fall through to the refusal template: %s", q, reply.Text)
		}
		// DIB-513: the menu moved from the prose into Suggest, so "answered from the registry" is
		// now read off the codes. Same invariant, exact instead of substring-matched.
		var offersOrder bool
		for _, code := range reply.Suggest {
			if code == "order.lookup" {
				offersOrder = true
			}
		}
		if !offersOrder {
			t.Fatalf("%q must be answered from the registry, got %v: %s", q, reply.Suggest, reply.Text)
		}
		// "usable" used to mean the example argument was printed beside each menu line. A chip is
		// usable by being clicked: it selects that lookup and lands the caret in its first argument,
		// where the SAME example is the placeholder (assist-panel.ts, pickSuggested). So what has to
		// hold here is that the registry still carries the example the form will show.
		var sawExample bool
		for _, spec := range tools {
			if spec.Name != "order.lookup" {
				continue
			}
			for _, a := range spec.Args {
				if a.Example == "SO-4" {
					sawExample = true
				}
			}
		}
		if !sawExample {
			t.Fatalf("%q: order.lookup must still carry its example argument for the chip's form", q)
		}
	}
}

func TestACapabilityAnswerWithNoToolsBlamesTheConfigurationNotTheTool(t *testing.T) {
	ans := capabilityReply(CapabilityQuestion{}, nil).Text
	if !strings.Contains(ans, "administrator") {
		t.Fatalf("an empty tool set is a narrowing, and the answer must say so: %s", ans)
	}
}

func TestParseCapabilityQuestionLeavesOrdinaryQuestionsAlone(t *testing.T) {
	for _, q := range []string{
		"where's SO-4",
		"how many Blu 60 do we have",
		"what does Northgate owe us",
		"did we get a payment from Bay Stone",
	} {
		if _, ok := parseCapabilityQuestion(q); ok {
			t.Fatalf("%q is a lookup, not a question about the assistant", q)
		}
	}
}

// TestARealDibbitsSKUResolves pins the shape of the SKUs this deployment actually has.
//
// MUTATION TARGET: drop the repetition from reSKU (back to a single hyphenated pair) and this test
// dies, as every real stock question did before the DIB-465 walkthrough asked one.
func TestARealDibbitsSKUResolves(t *testing.T) {
	p := NewStubProvider()
	tools := managerTools(t)
	for _, tc := range []struct{ question, want string }{
		{"how many DBS-TB-BLU60 do we have?", "DBS-TB-BLU60"},
		{"stock on DBS-AGG-34CLEAR", "DBS-AGG-34CLEAR"},
		{"do we have any TB-BLU60?", "TB-BLU60"},
	} {
		reply, err := p.Complete(context.Background(), CompletionRequest{
			Messages: []Message{{Role: RoleUser, Text: tc.question}},
			Tools:    tools,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(reply.ToolCalls) != 1 || reply.ToolCalls[0].Command != "stock.lookup" {
			t.Fatalf("%q: expected a stock lookup, got %+v", tc.question, reply)
		}
		if got := reply.ToolCalls[0].Args["product"]; got != tc.want {
			t.Fatalf("%q: product = %q, want %q, a truncated SKU is a lookup that can never hit",
				tc.question, got, tc.want)
		}
	}
}
