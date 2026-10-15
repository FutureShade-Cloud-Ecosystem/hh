package dashboard

import (
	"context"
	"sync"
)

// widget_data.go, MEASURING A WIDGET BY ITS KEY.
//
// -- WHAT THIS REPLACES --------------------------------------------------------------------------
//
// Home's numbers came from the composed-chart route. The browser built a chart id out of each
// widget's measure and split (`vizIdsFor`), asked /viz for it, and matched the answers back by that
// id. Three things were wrong with it, in increasing order of seriousness:
//
//   1. The client was synthesising server ids. A composed id is the chart composer's vocabulary and
//      a widget is not a composed chart, 0202 exists precisely because they are not the same
//      object, so every release had to keep two id grammars agreeing across the wire.
//   2. TWO WIDGETS COULD SHARE ONE NUMBER. The id carries measure, dimension and shape and nothing
//      else, and the client de-duplicated on it. Two widgets differing only by BRANCH or by WINDOW
//      collapsed to one query, and both drew whichever answer came back.
//   3. A widget's window and branch could not reach the SQL at all, because they were not in the id
//      and the id was the whole request.
//
// Measuring by widget_key fixes all three by construction: one row, one measurement, every field the
// row carries available to the query.
//
// -- THE KEY IS A SELECTOR, NEVER A CARRIER ------------------------------------------------------
//
// The request names KEYS and nothing else. Measure, split, branch and window are read from the
// caller's OWN stored row, resolved exactly as their Home is, through the same layering and the
// same catalog gate, so there is no query parameter that can widen what gets measured. A key that
// is not in the caller's resolved layout is not in the result set to begin with: there is nothing to
// refuse, because there was never anything to index into. That is the same shape as the branch
// scoping, which comes from the caller's grants and never from the URL.

// seriesDimension is the dimension a widget's series is measured over.
//
// A WIDGET'S SPLIT AND A SERIES' DIMENSION ARE NOT THE SAME VOCABULARY, and this is where they meet.
// A split may be "none", the doctrine's first option and the honest default for a stat, while a
// series is always measured over SOME dimension; the measure's first is the one its catalog entry
// lists first, which is the composer's default too. A split the measure does not offer as a
// dimension falls back the same way rather than being passed through to a query that would return
// nothing.
//
// This arithmetic used to live in the BROWSER (home-bento-logic.ts's vizIdFor), which is why it is
// worth naming: the client was deciding what the server would measure, from its own copy of the
// catalog, and the two had to agree across every release.
func seriesDimension(m MetricMeta, split string) string {
	if split != "" && split != SplitNone && containsString(m.Dimensions, split) {
		return split
	}
	if len(m.Dimensions) > 0 {
		return m.Dimensions[0]
	}
	return ""
}

// narrowToBranch applies a widget's branch narrowing to the caller's scope.
//
// -- IT CAN ONLY EVER NARROW, AND THAT IS THE WHOLE CONTRACT ------------------------------------
//
// 0201's column comment set this rule before anything implemented it: "A NARROWING filter, never a
// grant: NULL means 'the branches this caller may see', and the read intersects any value here with
// the caller's own grants."
//
// The four cases, and the reasoning for each:
//
//	branch "" (any caller)   -> UNCHANGED. "" is not "no branch", it is "the branches I may see",
//	                            which is exactly the scope the auth guard already resolved.
//	branch X, caller holds X -> [X], AllBranches FALSE. Note the second half: a Tier-1 global caller
//	                            narrowing to one branch must lose AllBranches, or the branch filter
//	                            is dropped by w6aBranchScope and the widget silently measures
//	                            everything under a caption naming one yard.
//	branch X, AllBranches    -> [X], AllBranches false. Same line; a global caller holds every
//	                            branch, so this is still a narrowing.
//	branch X, caller lacks X -> NOT MEASURED AT ALL. The caller reports this by returning ok=false,
//	                            and WidgetDataFor omits the datum. It must NOT fall back to the
//	                            caller's own branches, that answers a different question than the
//	                            one stored, and it must NOT measure with an empty grant list either,
//	                            because that returns zero points, TotalOf sums them to 0, and the card
//	                            draws a confident "$0.00" for a question it was never allowed to ask.
//	                            A FALSE ZERO IS THE ONE ANSWER WORSE THAN NO ANSWER.
//
// This case is reachable: a grant revoked after the widget was saved. The layout route deliberately
// does not rewrite the stored branch (see WidgetViewsFor), because the client re-PUTs what it is
// served and a "repair" there would be a silent permanent write.
func narrowToBranch(scope Scope, branchID string) (Scope, bool) {
	if branchID == "" {
		return scope, true
	}
	if !BranchGateFor(scope).Allows(branchID) {
		return scope, false
	}
	// AllBranches MUST be cleared, and this is the line that matters most. A Tier-1 global caller
	// keeps AllBranches through every other path; if it survived here, w6aBranchScope would send
	// SQL NULL, the branch predicate would drop out, and the widget would measure the whole company
	// under a caption naming one yard.
	scope.BranchIDs, scope.AllBranches = []string{branchID}, false
	return scope, true
}

// WidgetDatum is one widget's measurement, keyed by the widget it belongs to.
//
// It deliberately mirrors VizTile's state vocabulary rather than inventing one: `ok`, `unwired` and
// `error` already mean something precise on this wire and a second set of words for the same three
// facts would be two vocabularies for one question.
type WidgetDatum struct {
	Key    string  `json:"widget_key"`
	State  string  `json:"state"`
	Total  *int64  `json:"total,omitempty"`
	Points []Point `json:"points,omitempty"`
	Note   string  `json:"note,omitempty"`
	// Range is the window this number was ACTUALLY measured over, echoed back rather than assumed.
	// The stored row may name a window its measure cannot answer, nine of the shipped role defaults
	// did, and the read repairs it, so a client that captioned the card from its own copy of the
	// row would print the window that was asked for instead of the one that was used.
	Range string `json:"range"`
}

// WidgetDataFor measures the named widgets from the caller's own resolved layout.
//
// BYTE-ABSENCE, twice over and for two different reasons. A key that is not the caller's produces no
// entry; so does a widget whose measure is not in their catalog, the latter being the same `continue`
// the layout read and the composed-chart read both use, so a measure a person may not see never
// reaches the wire by any of the three doors.
//
// A MEASUREMENT NEVER FAILS THE PAGE. A provider that is missing gives `unwired`, one that errors
// gives `error` with a fixed public note and the detail in the log, the posture ResolveViz already
// takes, because a widget that cannot be measured is a gap on a Home and a 500 is a Home that is not
// there at all.
func (s *Service) WidgetDataFor(ctx context.Context, scope Scope, keys []string) []WidgetDatum {
	s.stampScope(ctx, &scope)
	layout := s.WidgetsFor(ctx, scope)
	byKey := make(map[string]WidgetView, len(layout.Widgets))
	for _, w := range layout.Widgets {
		byKey[w.Key] = w
	}

	allowed := s.allowedMeasures(ctx, scope)
	out := make([]WidgetDatum, 0, len(keys))
	views := make([]WidgetView, 0, len(keys))
	meta := make([]MetricMeta, 0, len(keys))
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		w, ok := byKey[k]
		if !ok {
			continue // BYTE-ABSENT: not the caller's widget, or not one they may see.
		}
		// The reserved queue carries no catalog measure and no series, RegisterSeries would refuse
		// it, and its body is drawn from the Needs read instead. Omitting it is not a gap: reporting
		// it `unwired` would tell a client to draw an em-dash over a queue that is working.
		if w.Measure == MeasureNeedsYou {
			continue
		}
		m, ok := allowed[w.Measure]
		if !ok {
			continue // BYTE-ABSENT again, and by the same line the layout read uses.
		}
		// AND BYTE-ABSENT A THIRD TIME: a widget narrowed to a branch this caller no longer holds is
		// not measured. Not zero, not an error, not their own branches instead, absent, so the card
		// draws the same empty state it draws for a measure that was never wired.
		if _, ok := narrowToBranch(scope, w.BranchID); !ok {
			continue
		}
		out = append(out, WidgetDatum{Key: w.Key, State: StateUnwired,
			Note: unwiredNote(w.Module), Range: w.Range})
		views = append(views, w)
		meta = append(meta, m)
	}

	var wg sync.WaitGroup
	for i := range out {
		p, ok := s.seriesProvider(views[i].Measure)
		if !ok {
			continue // present, unwired, a different answer from absent, and deliberately so.
		}
		wg.Add(1)
		go func(idx int, p SeriesProvider) {
			defer wg.Done()
			w := views[idx]
			// THE WINDOW IS RESOLVED FROM THE STORED ROW, per widget, and only here. Two widgets on
			// one measure asking for different windows now get different queries, which is the
			// defect the composed id could not express.
			sc, _ := narrowToBranch(scope, w.BranchID)
			sc.Window = ResolveWindowIn(scope.Zone, w.Range, scope.Now)
			pts, err := s.measureSeries(ctx, p, sc, seriesDimension(meta[idx], w.Split))
			d := &out[idx]
			if err != nil {
				d.State, d.Note = StateError, "Temporarily unavailable"
				s.warn("dashboard: widget measurement failed",
					"widget", w.Key, "measure", w.Measure, "split", w.Split, "err", err)
				return
			}
			total := TotalOf(pts)
			d.State, d.Points, d.Note = StateOK, pts, ""
			d.Total = &total
		}(i, p)
	}
	wg.Wait()
	return out
}
