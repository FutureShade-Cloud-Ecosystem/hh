package adapters

// quoteextract.go, DIB-66's optional AI path, and THE ONLY DOOR IT HAS.
//
// -----------------------------------------------------------------------------------------------
// WHY THIS FILE IS IN THIS PACKAGE AND NOT IN internal/quoteassist
// -----------------------------------------------------------------------------------------------
// D-075 ratifies one set of rails for every AI provider call in this application: budget resolved
// per caller, spend checked BEFORE anything is sent, an append-only ledger row per exchange,
// provider pluggable, graceful degradation when any of that says no. Those rails are the assist
// package's (assist.BudgetFor, assist.OverBudget, assist.MonthWindow, assist.LedgerStore) and the
// point of putting the second consumer HERE is that it reaches them by CALLING them, not by
// growing a second copy inside a quote module that nobody would think to audit against the first.
//
// Every rule below is assist's own function. This file contributes the SEQUENCING and nothing else,
// which is why the tests for it assert the sequence: over budget → no provider call, no ledger row;
// in budget → one provider call, exactly one ledger row, with the provider's OWN reported tokens.
//
// -----------------------------------------------------------------------------------------------
// WHAT THE MODEL IS ALLOWED TO RETURN
// -----------------------------------------------------------------------------------------------
// Phrases. Text, a quantity, a unit word. That is the entire contract, and the return type is what
// enforces it: quoteassist.Segmentation cannot carry a product id, a SKU, a price or a line, so a
// provider that hallucinates one has nowhere to put it. Product resolution happens afterwards, in
// quoteassist.resolve(), against the real catalogue through the real ranked search.
//
// This is the difference between "AI extracts line items" as a demo and as something a yard can
// run: the model is doing English, and the business data is doing business data.
//
// -----------------------------------------------------------------------------------------------
// THE LEDGER ROW IS WRITTEN FOR AN EXCHANGE THAT HAPPENED, AND NOT FOR ONE THAT DIDN'T
// -----------------------------------------------------------------------------------------------
// assist/ledger.go's ruling, restated because this is a second consumer of the same table and the
// two must agree:
//
//	provider answered            → one row, with its reported tokens (the stub's zeros included)
//	provider returned an error   → one row. Something was sent; something was spent.
//	REFUSED OVER BUDGET          → NO ROW. Nothing was sent, nothing was spent, and a row here
//	                               would inflate the exchange count with the app's own refusals.
//
// The ledger write failure does not swallow the answer, for the reason assist gives: a missing row
// is recoverable from the provider's own billing, and a broken extractor is not.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quoteassist"
)

// KindQuoteExtract is this consumer's ledger kind.
//
// assist/ledger.go says of the column: "'chat' today; named rather than assumed so a future
// non-chat consumer lands in the same ledger instead of a second one nobody sums." This is that
// consumer, and it lands in that ledger, so a month's spend across chat and extraction is one
// number from one table, and the budget a rep is refused against is the budget they actually spent.
const KindQuoteExtract = "quote_extract"

// THERE IS NO TOKEN ESTIMATOR IN THIS FILE, AND THAT IS DELIBERATE. Budgets are enforced against
// the spend ALREADY RECORDED in the ledger, assist/provider.go's rule, that "a budget enforced
// against a guess is a budget that cannot be explained to the person it refuses", so what a request
// is about to cost is never predicted. The only forward-looking bound is on the bytes we send, and
// that is the handler's cap. "Add a cheap estimate here" is the obvious-looking change; it is the
// one that makes the refusals unexplainable.

// QuoteExtractRails is the AI segmentation seam with the assist rails around it.
//
// Every field is one of assist's own: the provider it will call, the ledger it will write to, the
// limit store the budget is resolved from. There is no state and no cache, the single most likely
// way to break the caller-scoping invariant would be a per-user cache that forgot the user, so there
// is nothing here that could hold one.
type QuoteExtractRails struct {
	provider assist.Provider
	ledger   assist.LedgerStore
	limits   assist.LimitStore
	log      *slog.Logger
	now      func() time.Time
}

var _ quoteassist.AIExtractor = (*QuoteExtractRails)(nil)

// NewQuoteExtractRails builds the seam, or returns NIL when there is no provider.
//
// A nil AIExtractor is the shipped default and quoteassist treats it as "AI is off", not as a
// fault. Returning nil rather than a rails object with a nil provider is what makes the absence
// legible at the wiring site: `svc.WithAI(adapters.NewQuoteExtractRails(...))` either attaches
// something that works or attaches nothing.
func NewQuoteExtractRails(p assist.Provider, ledger assist.LedgerStore, limits assist.LimitStore, log *slog.Logger) *QuoteExtractRails {
	if p == nil {
		return nil
	}
	return &QuoteExtractRails{provider: p, ledger: ledger, limits: limits, log: log, now: time.Now}
}

// WithClock replaces the clock (tests only), so the month-window behaviour can be exercised without
// waiting for a month.
func (q *QuoteExtractRails) WithClock(f func() time.Time) *QuoteExtractRails { q.now = f; return q }

// SegmentText asks the provider to split a job description into candidate lines.
//
// The five steps, in this order, and the order is the security argument:
//
//  1. is there a caller at all           → refused, no spend
//  2. resolve the caller's budget        → assist.BudgetFor, the whole-or-absent rule
//  3. what have they spent this month    → the ledger, this calendar month, half-open
//  4. over it?                           → assist.ErrOverBudget. NO PROVIDER CALL. NO LEDGER ROW.
//  5. call the provider, then ALWAYS     → one row, with the provider's own numbers
//     write the row
func (q *QuoteExtractRails) SegmentText(ctx context.Context, c assist.Caller, text string) (quoteassist.Segmentation, error) {
	if !c.HasRole() {
		// Tier 0, an account with no role assigned. Refused before any spend, exactly as
		// assist.Service.Ask refuses it: unassigned authority is unknown authority.
		return quoteassist.Segmentation{}, assist.ErrRefused
	}

	budget, err := assist.BudgetFor(ctx, q.limits, c)
	if err != nil {
		return quoteassist.Segmentation{}, err
	}
	spent, err := q.monthSpend(ctx, c.UserID)
	if err != nil {
		return quoteassist.Segmentation{}, err
	}
	if assist.OverBudget(spent, budget) {
		// THE GATE. assist.OverBudget is the one implementation of `spent >= budget` in the app and
		// this is a call to it, not a copy of it, so the boundary a rep is refused at here is
		// byte-identical to the one the chat surface refuses them at, and flipping the operator
		// breaks both surfaces' tests at once.
		return quoteassist.Segmentation{}, assist.ErrOverBudget
	}

	reply, callErr := q.provider.Complete(ctx, assist.CompletionRequest{
		Messages: []assist.Message{
			{Role: assist.RoleUser, Text: segmentPrompt(text)},
		},
		// NO TOOLS. This exchange is not allowed to reach a command: it is a text transformation,
		// and offering the registry's tools here would give a provider a reason to go looking at
		// data it has no business in for a job that is pure English.
		Tools: nil,
	})

	// The row lands whether the provider answered or failed, see the file header.
	q.record(ctx, c, reply.TokensIn, reply.TokensOut)

	if callErr != nil {
		return quoteassist.Segmentation{}, fmt.Errorf("quoteassist ai: provider failed: %w", callErr)
	}
	phrases := parsePhrases(reply.Text)
	return quoteassist.Segmentation{
		Phrases:   phrases,
		Provider:  q.provider.Name(),
		Model:     q.provider.Model(),
		TokensIn:  reply.TokensIn,
		TokensOut: reply.TokensOut,
	}, nil
}

// monthSpend is this caller's usage in the current calendar month, through assist's own window.
func (q *QuoteExtractRails) monthSpend(ctx context.Context, userID string) (assist.Spend, error) {
	if q.ledger == nil {
		// No ledger wired: no recorded spend, so the budget cannot be enforced against anything.
		// Zero is the honest answer to "what have they spent" when nothing is being recorded, and
		// the operator-visible consequence (an unmetered provider) is a wiring decision made in the
		// composition root, not something this file may paper over.
		return assist.Spend{}, nil
	}
	from, to := assist.MonthWindow(q.now())
	return q.ledger.SpendFor(ctx, userID, from, to)
}

// record appends the ledger row. A failure is logged loudly and swallowed, per assist/ledger.go.
func (q *QuoteExtractRails) record(ctx context.Context, c assist.Caller, in, out int) {
	if q.ledger == nil {
		return
	}
	err := q.ledger.Append(ctx, assist.Entry{
		UserID:   c.UserID,
		RoleCode: c.RoleCode,
		Provider: q.provider.Name(),
		Model:    q.provider.Model(),
		// EXACTLY what the provider reported. Never estimated, never rounded up "to be safe".
		TokensIn:  in,
		TokensOut: out,
		Kind:      KindQuoteExtract,
		At:        q.now(),
	})
	if err != nil && q.log != nil {
		q.log.Error("quoteassist: token ledger write failed, this extraction is not counted",
			"err", err, "user_id", c.UserID, "kind", KindQuoteExtract)
	}
}

// segmentPrompt is the instruction sent to a provider.
//
// IT ASKS FOR ENGLISH, NOT FOR BUSINESS DATA. No SKUs, no product ids, no prices, no catalogue, the
// model is not shown the catalogue and is not asked to guess at it, because whatever it guessed would
// be thrown away by the resolver anyway. What it is good at, and what is asked for, is reading a
// paragraph a human dictated and saying where one item stops and the next begins.
//
// The requested format is a JSON array so the parse is unambiguous, with a line-per-item fallback
// (parsePhrases) because a provider that answers in prose must degrade to something usable rather
// than to an error.
func segmentPrompt(text string) string {
	var b strings.Builder
	b.WriteString("Split this landscape-supply job description into one entry per material.\n")
	b.WriteString("Answer with a JSON array only. Each entry: ")
	b.WriteString(`{"text": the words for that item exactly as written, "qty": the number if one is stated else "", "unit": the unit word if one is stated else ""}.`)
	b.WriteString("\nDo not name products, SKUs, prices or catalogue items. Do not invent quantities.")
	b.WriteString(" Keep the caller's own wording in \"text\".\n\n")
	b.WriteString(text)
	return b.String()
}

// parsePhrases reads a provider's answer into phrases.
//
// THREE LAYERS OF FALLBACK, because a provider's output format is not something this app controls:
//
//	a JSON array of objects   → the requested shape
//	a JSON array of strings   → close enough; the deterministic parser reads the quantities
//	anything else             → one phrase per non-empty line, quantities read deterministically
//
// In every case the QUANTITY IS RE-READ DETERMINISTICALLY when the provider did not state one, by
// quoteassist.Segment on the phrase itself. That is deliberate belt-and-braces: a model that drops a
// quantity costs the rep a keystroke, and a model that invents one would cost a customer money, so
// the parser gets the last word on any number the model left blank.
func parsePhrases(raw string) []quoteassist.Phrase {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	// Providers commonly fence JSON in a code block. Strip it rather than failing to parse, the
	// fence is a rendering habit, not a different answer.
	s = strings.TrimSpace(strings.TrimPrefix(s, "```json"))
	s = strings.TrimSpace(strings.TrimPrefix(s, "```"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "```"))

	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.LastIndex(s, "]"); j > i {
			body := s[i : j+1]
			var objs []struct {
				Text string `json:"text"`
				Qty  string `json:"qty"`
				Unit string `json:"unit"`
			}
			if err := json.Unmarshal([]byte(body), &objs); err == nil && len(objs) > 0 {
				return fromObjects(objs)
			}
			var strs []string
			if err := json.Unmarshal([]byte(body), &strs); err == nil && len(strs) > 0 {
				return fromLines(strs)
			}
		}
	}
	return fromLines(strings.Split(s, "\n"))
}

func fromObjects(objs []struct {
	Text string `json:"text"`
	Qty  string `json:"qty"`
	Unit string `json:"unit"`
}) []quoteassist.Phrase {
	out := make([]quoteassist.Phrase, 0, len(objs))
	for _, o := range objs {
		text := strings.TrimSpace(o.Text)
		if text == "" {
			continue
		}
		p := quoteassist.Phrase{Text: text, Qty: strings.TrimSpace(o.Qty), Unit: strings.TrimSpace(o.Unit)}
		if p.Qty == "" || p.Unit == "" {
			// The deterministic parser fills what the provider left blank, and only what it left
			// blank. It never overrides a stated value, the model read the sentence and this
			// function did not.
			if det := quoteassist.Segment(text); len(det) == 1 {
				if p.Qty == "" {
					p.Qty = det[0].Qty
				}
				if p.Unit == "" {
					p.Unit = det[0].Unit
				}
			}
		}
		out = append(out, p)
	}
	return out
}

func fromLines(lines []string) []quoteassist.Phrase {
	out := make([]quoteassist.Phrase, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(strings.Trim(strings.TrimSpace(l), `",`))
		if l == "" {
			continue
		}
		// One phrase per line, read by the deterministic parser, which is the same code path the
		// no-AI default uses, so a provider whose only contribution was better line breaks still
		// gets the same quantity handling as everything else.
		det := quoteassist.Segment(l)
		if len(det) == 0 {
			continue
		}
		// Segment may split one provider line further (a line with " and " in it). That is correct:
		// the deterministic splitter is not overridden by a worse one.
		out = append(out, det...)
	}
	return out
}
