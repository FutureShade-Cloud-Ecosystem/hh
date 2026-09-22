// DIB-513, a refusal is a sentence and a row of chips, not a paragraph and a bulleted manual.
//
// WHAT WAS MEASURED, on a live stack, by asking TanyaAI something it cannot answer ("what is the
// weather in Trenton tomorrow?"):
//
//	manager tier  → 279 words, 0 chips
//	counter tier  → 155 words, 0 chips
//
// The words were the word "Right now I can:" followed by every offered tool's full Summary as prose
// , the same list the command palette four inches up the screen was already showing as buttons. A
// list you can click beats a list you have to read, and it is the only version that ends in an
// answer rather than in more typing.
//
// These tests are ADDITIVE and they pin the NUMBER, because a budget nobody measures walks back.
package assist

import (
	"context"
	"strings"
	"testing"
)

// the seventeen-command manager palette, as the server serves it.
func d513Tools() []ToolSpec {
	return []ToolSpec{
		{Name: "payment.lookup", Description: "Look up a payment by its reference, its exact amount, or the customer it came from."},
		{Name: "order.lookup", Description: "Look up an order by its number and see its status, customer and total."},
		{Name: "stock.lookup", Description: "On-hand, allocated and available for a product, branch by branch."},
		{Name: "customer.balance", Description: "What a customer owes right now, and how much of it is overdue."},
		{Name: "customer.activity", Description: "The last few orders and payments on a customer's account."},
		{Name: "payments.today", Description: "What has come in today across the branches you can see."},
		{Name: "ar.aging", Description: "Who owes what, oldest money first, across the branches you can see."},
		{Name: "product.find", Description: "Look a product up in the catalogue by name, SKU or the words your customers use."},
	}
}

func words(s string) int { return len(strings.Fields(s)) }

func TestDIB513_RefusalIsShortAndOffersChips(t *testing.T) {
	p := NewStubProvider()
	tools := d513Tools()
	reply, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Text: "what is the weather in Trenton tomorrow?"}},
		Tools:    tools,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if n := words(reply.Text); n > 25 {
		t.Fatalf("the refusal is %d words, want <= 25:\n%s", n, reply.Text)
	}
	// The menu is not recited. Not one tool's prose Summary may appear in the sentence.
	for _, tool := range tools {
		if strings.Contains(reply.Text, tool.Description) {
			t.Fatalf("the refusal recites %q as prose, it belongs in Suggest", tool.Name)
		}
	}
	if strings.Contains(reply.Text, "Right now I can") {
		t.Fatalf("the prose menu is back:\n%s", reply.Text)
	}
	// NOTHING WAS LOST. Every offered tool is still offered, as a code the surface renders as a chip.
	if len(reply.Suggest) != len(tools) {
		t.Fatalf("Suggest has %d of %d tools, the menu must survive as chips", len(reply.Suggest), len(tools))
	}
	for i, tool := range tools {
		if reply.Suggest[i] != tool.Name {
			t.Fatalf("Suggest[%d] = %q, want %q (the caller's own order)", i, reply.Suggest[i], tool.Name)
		}
	}
	// A refusal is an ordinary answer: it runs nothing.
	if len(reply.ToolCalls) != 0 {
		t.Fatalf("a refusal must not invoke anything, got %d calls", len(reply.ToolCalls))
	}
}

func TestDIB513_ACallerWithNoToolsGetsWordsInsteadOfAnEmptyChipRow(t *testing.T) {
	// The one case where prose is still the whole answer. Somebody offered nothing has no chips to
	// be given, so the sentence has to carry what would change that, and an empty chip row under a
	// refusal would be a frame around nothing.
	p := NewStubProvider()
	reply, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Text: "what is the weather in Trenton tomorrow?"}},
		Tools:    nil,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(reply.Suggest) != 0 {
		t.Fatalf("no tools must mean no chips, got %v", reply.Suggest)
	}
	if !strings.Contains(reply.Text, "administrator") {
		t.Fatalf("with nothing to offer, the sentence must name what would change it:\n%s", reply.Text)
	}
	if n := words(reply.Text); n > 30 {
		t.Fatalf("even the no-tools refusal is %d words, want <= 30", n)
	}
}

func TestDIB513_HelpAndUnreachableCommandAlsoOfferChips(t *testing.T) {
	// The other two menu-shaped replies. All three used to end in the same 279-word paragraph.
	p := NewStubProvider()
	tools := d513Tools()
	for _, q := range []string{"help", "what can you do?"} {
		reply, err := p.Complete(context.Background(), CompletionRequest{
			Messages: []Message{{Role: RoleUser, Text: q}},
			Tools:    tools,
		})
		if err != nil {
			t.Fatalf("Complete(%q): %v", q, err)
		}
		if len(reply.ToolCalls) > 0 {
			continue // a question that resolved into a real lookup is not a menu reply
		}
		if n := words(reply.Text); n > 25 {
			t.Fatalf("%q answered in %d words, want <= 25:\n%s", q, n, reply.Text)
		}
		if len(reply.Suggest) == 0 {
			t.Fatalf("%q must offer the palette as chips", q)
		}
	}
}
