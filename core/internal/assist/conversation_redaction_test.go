package assist

// conversation_redaction_test.go, the recorded command of a conversation exchange carries no personal filter
// value (FROM-PRINCIPAL item 44, PII-CLASSES-r2): the same rule slice 6b round 2 put on assist_query_log. The
// question and answer text stay as they are, by design; only the recorded data.query call is changed.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type emptyQuerier struct{}

func (emptyQuerier) Query(context.Context, Caller, string) (Result, error) {
	return Result{Command: DataQueryCode, Items: []Item{}, Empty: true}, nil
}

func queryConvService(p Provider) (*Service, *MemoryConversations) {
	store := NewMemoryConversations()
	reg := NewRegistry(Deps{Query: emptyQuerier{}}, nil)
	svc := NewService(reg, p, &fakeLedger{}, newFakeLimits(), StaticSettings{}, nil).
		WithClock(func() time.Time { return at(11) }).WithConversations(store)
	svc.withTestMeter()
	return svc, store
}

func savedRan(t *testing.T, store *MemoryConversations, c Caller, id string) []Invocation {
	t.Helper()
	ex, ok, err := store.Exchanges(context.Background(), id, c.UserID, c.SessionKey)
	if err != nil || !ok || len(ex) != 1 {
		t.Fatalf("saved exchanges: ok=%v err=%v n=%d", ok, err, len(ex))
	}
	return ex[0].Ran
}

func TestConversation_TheSavedRanCarriesNoPersonalFilterValue(t *testing.T) {
	const secret = "Mrs Zelda Quimby"
	for name, spec := range map[string]string{
		"equals":   `{"view":"ai.quotes","select":["quote_no"],"where":[{"col":"contact_name","op":"=","value":"` + secret + `"}]}`,
		"in":       `{"view":"ai.quotes","select":["quote_no"],"where":[{"col":"delivery_address","op":"in","value":["` + secret + `","7 Elm"]}]}`,
		"between":  `{"view":"ai.quotes","select":["quote_no"],"where":[{"col":"customer_notes","op":"between","value":["` + secret + `","Zz"]}]}`,
		"contains": `{"view":"ai.quotes","select":["quote_no"],"where":[{"col":"customer_name","op":"contains","value":"` + secret + `"}]}`,
		"unparsed": `{"view":"ai.quotes","where":[{"col":"contact_name","op":"=","value":"` + secret + `"}],"bogus":`,
	} {
		p := &scriptedProvider{replies: []Reply{
			{ToolCalls: []ToolCall{{Command: DataQueryCode, Args: Args{"spec": spec}}}},
			{Text: "done"},
		}}
		svc, store := queryConvService(p)
		who := persona("jti-q")
		ans, err := svc.AskConversation(context.Background(), who, "", "find them", nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ran := savedRan(t, store, who, ans.ConversationID)
		if len(ran) != 1 || ran[0].Command != DataQueryCode {
			t.Fatalf("%s: the recorded call is %+v", name, ran)
		}
		saved := ran[0].Args["spec"]
		if strings.Contains(saved, "Quimby") || strings.Contains(saved, "Elm") || strings.Contains(saved, "Zz") {
			t.Errorf("%s: the saved ran carries a personal value: %s", name, saved)
		}
		if name == "unparsed" {
			continue
		}
		var sp Spec
		if err := json.Unmarshal([]byte(saved), &sp); err != nil || len(sp.Where) != 1 || string(sp.Where[0].Value) != `"(personal)"` || sp.Where[0].Op == "" || sp.Where[0].Col == "" {
			t.Errorf("%s: column and operator should stay with the marker: %s (%v)", name, saved, err)
		}
	}
}

func TestConversation_TheSavedRanKeepsAPlainFilterValue(t *testing.T) {
	spec := `{"view":"ai.quotes","select":["quote_no"],"where":[{"col":"status","op":"in","value":["SENT","DRAFT"]},{"col":"contact_name","op":"=","value":"Ann Lee"}]}`
	p := &scriptedProvider{replies: []Reply{{ToolCalls: []ToolCall{{Command: DataQueryCode, Args: Args{"spec": spec}}}}, {Text: "done"}}}
	svc, store := queryConvService(p)
	who := persona("jti-q2")
	ans, err := svc.AskConversation(context.Background(), who, "", "find them", nil)
	if err != nil {
		t.Fatal(err)
	}
	saved := savedRan(t, store, who, ans.ConversationID)[0].Args["spec"]
	if !strings.Contains(saved, `["SENT","DRAFT"]`) || strings.Contains(saved, "Ann Lee") || !strings.Contains(saved, "(personal)") {
		t.Errorf("saved ran: %s", saved)
	}
	// The answer the caller gets back still shows what ran, unchanged.
	if len(ans.Ran) != 1 || !strings.Contains(ans.Ran[0].Args["spec"], "Ann Lee") {
		t.Errorf("the returned answer must not be altered: %+v", ans.Ran)
	}
}
