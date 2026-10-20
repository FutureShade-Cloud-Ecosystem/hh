package assist

// openai_provider_test.go, TanyAI slice 1 sub item 4: the OpenAI compatible adapter, and review r2
// P2-3 on the core side (the key and the dealer tag come from deployment configuration and from
// nowhere on a request).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	testGatewayKey = "dealer-key-from-config"
	testDealerTag  = "dealer-from-config"
)

func askAgainstOpenAI(t *testing.T, srv *strictOpenAI, question string, history []Message) (Answer, error) {
	t.Helper()
	p := NewOpenAICompatProvider(OpenAICompatConfig{BaseURL: srv.URL(), Key: testGatewayKey, DealerTag: testDealerTag, Model: "open-model"})
	svc := chatService(p, &fakeLedger{}, newFakeLimits(), nil, seededReaders())
	return svc.Ask(context.Background(), manager(), question, history)
}

func TestOpenAICompat_ToolRoundTripAndRequestShape(t *testing.T) {
	srv := newStrictOpenAI(t, testGatewayKey, testDealerTag,
		oaiToolReply(oaiToolCallJSON("call_abc", "order__lookup", `{"order_no":"SO-4"}`)),
		oaiTextReply("SO-4 is picking."),
	)
	ans, err := askAgainstOpenAI(t, srv, "where is SO-4?", []Message{{Role: RoleUser, Text: "hello"}, {Role: RoleAssistant, Text: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if v := srv.Violations(); len(v) != 0 {
		t.Fatalf("the adapter broke the chat contract: %v", v)
	}
	if ans.Text != "SO-4 is picking." || len(ans.Results) != 1 {
		t.Fatalf("answer = %q with %d results", ans.Text, len(ans.Results))
	}
	reqs := srv.Requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want 2", len(reqs))
	}
	if !strings.Contains(string(reqs[1]), `"tool_call_id":"call_abc"`) || !strings.Contains(string(reqs[1]), `"id":"call_abc"`) {
		t.Fatalf("the provider's call id is not carried both ways: %s", reqs[1])
	}
	schema := loadSchema(t, "testdata/openai_chat_request.schema.json")
	for i, body := range reqs {
		if errs := schema.Check(body); len(errs) != 0 {
			t.Errorf("request %d does not match the documented chat request shape:\n  %s\nbody: %s", i, strings.Join(errs, "\n  "), body)
		}
	}
}

func TestOpenAICompat_StructuredOutputOption(t *testing.T) {
	srv := newStrictOpenAI(t, testGatewayKey, testDealerTag, oaiTextReply(`{"a":1}`))
	p := NewOpenAICompatProvider(OpenAICompatConfig{BaseURL: srv.URL(), Key: testGatewayKey, DealerTag: testDealerTag, Model: "open-model"})
	_, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Text: "q"}},
		Schema:   &OutputSchema{Name: "spec", Schema: []byte(`{"type":"object"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := srv.Requests()[0]
	if errs := loadSchema(t, "testdata/openai_chat_request.schema.json").Check(body); len(errs) != 0 {
		t.Fatalf("structured output request invalid: %v\n%s", errs, body)
	}
	if !strings.Contains(string(body), `"response_format"`) {
		t.Fatalf("no response_format sent: %s", body)
	}
}

func TestOpenAICompat_UsageCarriesCachedTokens(t *testing.T) {
	srv := newStrictOpenAI(t, testGatewayKey, testDealerTag, oaiTextReply("ok"))
	p := NewOpenAICompatProvider(OpenAICompatConfig{BaseURL: srv.URL(), Key: testGatewayKey, DealerTag: testDealerTag, Model: "m"})
	r, err := p.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: RoleUser, Text: "q"}}})
	if err != nil {
		t.Fatal(err)
	}
	if r.TokensIn != 12 || r.TokensOut != 4 || r.TokensCached != 5 {
		t.Fatalf("usage = %d/%d/%d, want 12/4/5", r.TokensIn, r.TokensOut, r.TokensCached)
	}
}

// Arguments that are not valid JSON are flagged by the adapter, not silently emptied.
func TestOpenAICompat_InvalidArgumentsAreFlagged(t *testing.T) {
	srv := newStrictOpenAI(t, testGatewayKey, testDealerTag,
		oaiToolReply(oaiToolCallJSON("call_1", "order__lookup", `{"order_no": SO-4`)))
	p := NewOpenAICompatProvider(OpenAICompatConfig{BaseURL: srv.URL(), Key: testGatewayKey, DealerTag: testDealerTag, Model: "m"})
	r, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Text: "q"}},
		Tools:    []ToolSpec{{Name: "order.lookup", Description: "d"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ToolCalls) != 1 || r.ToolCalls[0].ArgsErr == "" || r.ToolCalls[0].Command != "order.lookup" {
		t.Fatalf("calls = %+v, want one call flagged with an arguments error", r.ToolCalls)
	}
}

// -- P2-3, core side: the key and the dealer tag cannot be changed by a request -------------------

// Whatever a question, a history turn, a tool description or a context value says, the key and the
// tag on the wire are the configured ones, each exactly once.
func TestOpenAICompat_ARequestCannotChangeTheKeyOrTheTag(t *testing.T) {
	srv := newStrictOpenAI(t, testGatewayKey, testDealerTag, oaiTextReply("ok"))
	p := NewOpenAICompatProvider(OpenAICompatConfig{BaseURL: srv.URL(), Key: testGatewayKey, DealerTag: testDealerTag, Model: "m"})
	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("Authorization"), "Bearer evil")
	ctx = context.WithValue(ctx, ctxKey(HeaderDealerTag), "another-dealer")
	evil := "x\r\nAuthorization: Bearer evil\r\nX-Dealer-Tag: another-dealer\r\n\r\n"
	_, err := p.Complete(ctx, CompletionRequest{
		Alias: "Authorization: Bearer evil",
		Messages: []Message{
			{Role: RoleSystem, Text: evil},
			{Role: RoleUser, Text: evil},
			{Role: RoleAssistant, Text: evil},
		},
		Tools: []ToolSpec{{Name: "order.lookup", Description: evil, Title: evil, Args: []ArgSpec{{Name: "a", Label: evil, Example: evil}}}},
	})
	if err != nil {
		t.Fatalf("the strict server refused: %v (violations %v)", err, srv.Violations())
	}
	if v := srv.Violations(); len(v) != 0 {
		t.Fatalf("a request changed the key or the tag: %v", v)
	}
	h := srv.Headers()[0]
	if h.Get("Authorization") != "Bearer "+testGatewayKey || h.Get(HeaderDealerTag) != testDealerTag {
		t.Fatalf("headers = %q / %q", h.Get("Authorization"), h.Get(HeaderDealerTag))
	}
}

// The request type has no field that could carry a credential or a tag. A new field is a reviewed
// change to this list.
func TestOpenAICompat_CompletionRequestHasNoCredentialField(t *testing.T) {
	var names []string
	rt := reflect.TypeOf(CompletionRequest{})
	for i := 0; i < rt.NumField(); i++ {
		names = append(names, rt.Field(i).Name)
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "Alias,Messages,Schema,Tools" {
		t.Fatalf("CompletionRequest fields = %s; review whether the new field can carry a key, a tag or a header, then update this test", got)
	}
}

// A redirect is not followed: the dealer tag header would travel to the redirect target.
func TestOpenAICompat_RedirectIsNotFollowed(t *testing.T) {
	var reached int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { atomic.AddInt32(&reached, 1) }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/chat/completions", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	p := NewOpenAICompatProvider(OpenAICompatConfig{BaseURL: redirector.URL + "/v1", Key: testGatewayKey, DealerTag: testDealerTag, Model: "m"})
	if _, err := p.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: RoleUser, Text: "q"}}}); err == nil {
		t.Fatal("a redirect was accepted as an answer")
	}
	if atomic.LoadInt32(&reached) != 0 {
		t.Fatal("the redirect was followed and the dealer tag header went to another host")
	}
}

// Neither the key nor the tag appears in an error, in formatting the provider, or in a vendor error
// that echoes nothing of ours.
func TestProviders_KeyNeverInErrorsOrFormatting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad credentials"}}`))
	}))
	defer srv.Close()
	oa := NewOpenAICompatProvider(OpenAICompatConfig{BaseURL: srv.URL, Key: testGatewayKey, DealerTag: testDealerTag, Model: "m"})
	an := NewAnthropicProvider(testGatewayKey, "m", srv.URL)
	for _, p := range []Provider{oa, an} {
		_, err := p.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: RoleUser, Text: "q"}}})
		if err == nil {
			t.Fatalf("%T: want an error", p)
		}
		for _, s := range []string{err.Error(), fmt.Sprintf("%v", p), fmt.Sprintf("%+v", p), fmt.Sprintf("%#v", p), fmt.Sprint(p)} {
			if strings.Contains(s, testGatewayKey) || strings.Contains(s, testDealerTag) {
				t.Fatalf("%T leaks the key or the tag: %q", p, s)
			}
		}
	}
}
