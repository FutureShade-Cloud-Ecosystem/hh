//go:build integration

// Integration cover for migration 0416 (the alias table and the server kept conversations) against
// a real migrated Postgres. Run with:
//
//	DATABASE_URL=... go test -tags integration -count=1 -run 'Aliases|Conversations' ./internal/assist/
//
// WHAT ONLY A DATABASE CAN PROVE: the conversation log is append only in SQL (the same trigger
// pattern as the token ledger), the user and session match is enforced by the query and not only by
// a Go map, and the shipped alias rows say what the plan says they say.
//
// Conversation rows cannot be deleted, so every test uses a synthetic user and leaves its rows
// behind, attributed to an .invalid address (the 0154 fixtures do the same).

package assist

import (
	"context"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
	"strings"
	"testing"
)

func TestIntegration_Aliases_SeededRowsAndTheStubSeedConstraint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	rows, err := NewRepository(pool).Aliases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]AliasRow{}
	for _, r := range rows {
		got[r.Alias] = r
	}
	want := DefaultAliases()
	if len(got) != len(want) {
		t.Fatalf("got %d alias rows, want %d: %+v", len(got), len(want), got)
	}
	for _, w := range want {
		g := got[w.Alias]
		if g.AdapterKind != w.AdapterKind || g.Model != w.Model || g.Enabled != w.Enabled {
			t.Errorf("%s: row %+v does not match the shipped default %+v", w.Alias, g, w)
		}
	}
	// A vendor row may never carry the stub's label.
	_, err = pool.Exec(ctx, `UPDATE assist_model_aliases SET model = 'hos-stub-1' WHERE alias = 'tanyai-premium'`)
	if err == nil {
		t.Fatal("a vendor alias row accepted the stub seed as its model")
	}
}

func TestIntegration_Aliases_TheRouterReadsTheTableLive(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	router := NewRouter(repo, ModelConfig{AnthropicKey: redact.NewSecret("k"), AnthropicBaseURL: "http://127.0.0.1:1"}, NewStubProvider(), nil)
	p, ok := router.Resolve(ctx, AliasPremium)
	if !ok || p.Model() != "claude-sonnet-5-5" {
		t.Fatalf("premium resolves to %s ok=%v", p.Model(), ok)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE assist_model_aliases SET model='claude-sonnet-5-5', enabled=true WHERE alias='tanyai-premium'`)
	})
	if _, err := pool.Exec(ctx, `UPDATE assist_model_aliases SET enabled = false WHERE alias = 'tanyai-premium'`); err != nil {
		t.Fatal(err)
	}
	if p, ok := router.Resolve(ctx, AliasPremium); ok || p.Name() != "stub" {
		t.Fatalf("a disabled row still resolved to %s", p.Name())
	}
}

func TestIntegration_Aliases_TheStoredModelSettingIsNeverTheStubSeed(t *testing.T) {
	pool := testPool(t)
	var v string
	if err := pool.QueryRow(context.Background(), `SELECT value FROM assist_settings WHERE key = 'model'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v == "hos-stub-1" {
		t.Fatal("the 0153 seed is still stored as the model setting")
	}
}

func TestIntegration_Conversations_RoundTripAndTheMatchRule(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	user, other := syntheticUser(t, pool), syntheticUser(t, pool)

	id, err := repo.NewConversation(ctx, user, "jti-one")
	if err != nil || !strings.HasPrefix(id, "cv_") {
		t.Fatalf("id %q err %v", id, err)
	}
	for i, q := range []string{"first", "second", "third"} {
		ex := Exchange{Question: q, Answer: "a-" + q, Ran: []Invocation{{Command: "order.lookup", Args: Args{"order_no": "SO-" + string(rune('1'+i))}}}}
		if i == 2 {
			ex.Clarify = &Clarify{Question: "which one?", Command: "stock.lookup", Arg: "product"}
		}
		if err := repo.AppendExchange(ctx, id, user, "jti-one", ex); err != nil {
			t.Fatal(err)
		}
	}
	ex, ok, err := repo.Exchanges(ctx, id, user, "jti-one")
	if err != nil || !ok || len(ex) != 3 {
		t.Fatalf("ok %v err %v exchanges %d", ok, err, len(ex))
	}
	if ex[0].Question != "first" || ex[2].Question != "third" || ex[1].Ran[0].Args["order_no"] != "SO-2" || ex[2].Clarify == nil || ex[2].Clarify.Question != "which one?" {
		t.Fatalf("order or structured half lost: %+v", ex)
	}
	// Both must match.
	for name, args := range map[string][2]string{"other user": {other, "jti-one"}, "other session": {user, "jti-two"}, "empty session": {user, ""}, "empty user": {"", "jti-one"}} {
		if _, ok, err := repo.Exchanges(ctx, id, args[0], args[1]); ok || err != nil {
			t.Errorf("%s: ok %v err %v, want a plain miss", name, ok, err)
		}
	}
	// A mismatched append writes nothing.
	_ = repo.AppendExchange(ctx, id, other, "jti-one", Exchange{Question: "intruder", Answer: "x"})
	_ = repo.AppendExchange(ctx, id, user, "jti-two", Exchange{Question: "intruder", Answer: "x"})
	if ex, _, _ := repo.Exchanges(ctx, id, user, "jti-one"); len(ex) != 3 {
		t.Fatalf("a mismatched append landed: %d exchanges", len(ex))
	}
	// An unknown or malformed id is a plain miss, never an error.
	for _, bad := range []string{"cv_" + strings.Repeat("0", 32), "'; DROP TABLE x;--", ""} {
		if _, ok, err := repo.Exchanges(ctx, bad, user, "jti-one"); ok || err != nil {
			t.Errorf("id %q: ok %v err %v", bad, ok, err)
		}
	}
}

func TestIntegration_Conversations_AreAppendOnlyInSQL(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	user := syntheticUser(t, pool)
	id, _ := repo.NewConversation(ctx, user, "jti-append")
	_ = repo.AppendExchange(ctx, id, user, "jti-append", Exchange{Question: "q", Answer: "a"})
	for _, stmt := range []string{
		`UPDATE assist_conversation_exchanges SET answer = 'edited' WHERE conversation_id = '` + id + `'`,
		`DELETE FROM assist_conversation_exchanges WHERE conversation_id = '` + id + `'`,
		`TRUNCATE assist_conversation_exchanges`,
		`UPDATE assist_conversations SET session_key = 'stolen' WHERE id = '` + id + `'`,
		`DELETE FROM assist_conversations WHERE id = '` + id + `'`,
		`TRUNCATE assist_conversations CASCADE`,
	} {
		if _, err := pool.Exec(ctx, stmt); err == nil {
			t.Errorf("accepted: %s", stmt)
		} else if !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: refused for another reason: %v", stmt, err)
		}
	}
}

// The red team case over the real tables: two visitors on one persona user never see each other's
// turns, and a bearer with no jti leaves nothing behind.
func TestIntegration_Conversations_TwoVisitorsOnOneUserOverPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	user := syntheticUser(t, pool)
	p := &scriptedProvider{}
	svc := chatService(p, &fakeLedger{}, newFakeLimits(), nil, seededReaders()).WithConversations(repo)

	a := manager()
	a.UserID, a.SessionKey = user, "jti-visitor-a"
	b := a
	b.SessionKey = "jti-visitor-b"

	ansA, err := svc.AskConversation(ctx, a, "", "visitor A private question", nil)
	if err != nil || ansA.ConversationID == "" {
		t.Fatalf("A: id %q err %v", ansA.ConversationID, err)
	}
	ansB, err := svc.AskConversation(ctx, b, ansA.ConversationID, "visitor B here", nil)
	if err != nil || ansB.ConversationID == ansA.ConversationID {
		t.Fatalf("B was given A's conversation: %q err %v", ansB.ConversationID, err)
	}
	if strings.Contains(allText(p.seen[len(p.seen)-1].Messages), "visitor A private question") {
		t.Fatal("visitor B's request carried visitor A's turn")
	}
	if _, err := svc.AskConversation(ctx, a, ansA.ConversationID, "A again", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(allText(p.seen[len(p.seen)-1].Messages), "visitor A private question") {
		t.Fatal("visitor A lost their own history")
	}

	noJTI := a
	noJTI.SessionKey = ""
	var before int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assist_conversations`).Scan(&before)
	if ans, err := svc.AskConversation(ctx, noJTI, "", "no session", nil); err != nil || ans.ConversationID != "" {
		t.Fatalf("no jti: id %q err %v", ans.ConversationID, err)
	}
	var after int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assist_conversations`).Scan(&after)
	if after != before {
		t.Fatalf("a bearer with no jti created %d conversation rows", after-before)
	}
}

// Review P3-3: the history read is bounded. A long conversation yields only the newest window of
// exchanges, oldest first among them.
func TestIntegration_Conversations_HistoryReadIsBoundedToTheWindow(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	user := syntheticUser(t, pool)
	id, _ := repo.NewConversation(ctx, user, "jti-long")
	total := WindowTurns + 25
	for i := 0; i < total; i++ {
		if err := repo.AppendExchange(ctx, id, user, "jti-long", Exchange{Question: fmt.Sprintf("q%03d", i), Answer: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	ex, ok, err := repo.Exchanges(ctx, id, user, "jti-long")
	if err != nil || !ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
	if len(ex) != WindowTurns {
		t.Fatalf("read %d exchanges, want only the window of %d", len(ex), WindowTurns)
	}
	if ex[0].Question != fmt.Sprintf("q%03d", total-WindowTurns) || ex[len(ex)-1].Question != fmt.Sprintf("q%03d", total-1) {
		t.Fatalf("not the newest window in order: first %q last %q", ex[0].Question, ex[len(ex)-1].Question)
	}
}
