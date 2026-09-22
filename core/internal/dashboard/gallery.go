package dashboard

// gallery.go, "Add widget" opens a GALLERY first, and the gallery is rows.
//
// THE DOCTRINE, VERBATIM: "Add widget opens a GALLERY first, ready-made, role-relevant widgets with
// mini previews placed in one tap, with 'Build your own' opening the full form for anything the
// gallery doesn't cover." The seven-field builder does not go away; it stops being the FIRST thing a
// person meets, because most people adding a widget want one of six obvious ones and should not have
// to compose it out of a measure, a split, a shape, a window and a size to get there.
//
// "READY-MADE" AND "ROLE-RELEVANT" ARE WHY THIS IS DATA. Ready-made means somebody already chose the
// five fields; role-relevant means the choice differs per job shape. Both are a dealer's opinion,
// which in this product is rows (0140 for tiles, 0182 for the metric menu, 0201 §3 for the Needs-you
// sources) and never a switch on profile in Go. The rows live in 0208, keyed (profile, entry_key) -
// 0201's key shape, for 0201's reasons, argued at length in that file's header.
//
// -- AN ENTRY IS A WIDGET PLUS A SENTENCE, AND THE GATE IS THE SAME GATE ------------------------
//
// Everything widget.go says about a widget's measure holds here unchanged, because a gallery entry
// IS a widget somebody pre-filled:
//
//	READ  an entry whose measure is not in this caller's catalog is NOT RETURNED. Not greyed, not
//	      returned with a null measure, absent, the same bare `continue` WidgetViewsFor uses, and
//	      for a slightly sharper reason than a widget's: a widget the gate drops leaves a Home one
//	      card shorter, which is quiet. An OFFER that vanishes is worse, the person who saw it
//	      yesterday cannot tell whether they imagined it, so it must never have been offered.
//	WRITE an entry naming a measure THIS BUILD does not have is refused, 422, whole document, zero
//	      rows. The write gate is the FULL MetricByID and NoBranch, exactly as SaveProfileWidgets
//	      gates a role default: curation hides things and never grants anything, so an admin may
//	      curate a cost-bearing entry onto the manager shape and a caller who may not see it simply
//	      receives a gallery without it. Naming is not seeing.
//
// THERE IS NO branch_id ON AN ENTRY. NoBranch's argument (widget.go) applies with more force: a
// gallery entry is one row read by many people with different grants, and every answer for the
// reader who lacks the branch is bad. A person who wants "Sales at Trenton" places the ungated entry
// and narrows it on their own card, where the gate is theirs.

import "sort"

// -- the row -------------------------------------------------------------------------------------

// GalleryEntry is one curated ready-made widget, the shape of dashboard_gallery_entries.
//
// It is a distinct type from Widget rather than an embedding, and the difference is not cosmetic: an
// entry has no branch and no visibility-per-person, it has a BLURB, and its key is an entry_key that
// never lands on anybody's saved layout. Embedding Widget would put `branch_id` and `widget_key` on
// this wire as fields that must always be empty, which is four bytes of invitation to fill them in.
//
// Cols/Rows carry omitempty for Widget's reason exactly: zero is not a span, it is "the preset
// decides", and the way to say that here is for the key not to be there.
type GalleryEntry struct {
	Key      string `json:"entry_key"`
	Shape    string `json:"shape"`
	Measure  string `json:"measure"`
	Split    string `json:"split"`
	Range    string `json:"range"`
	Size     string `json:"size"`
	Cols     int    `json:"cols,omitempty"`
	Rows     int    `json:"rows,omitempty"`
	Title    string `json:"title"`
	Blurb    string `json:"blurb"`
	Position int    `json:"position"`
	Visible  bool   `json:"visible"`
}

// GalleryCard is one RESOLVED entry on the wire: the row plus what the browser needs to draw its
// mini preview, resolved from the measure's catalog entry. An entry whose measure is not in the
// caller's catalog never becomes one of these.
//
// It carries SpanCols/SpanRows for WidgetView's reason: the arithmetic that decides how big the
// placed card will be is the server's, like every other arithmetic on this wire, and the preview and
// the widget it becomes must agree about it before the person taps rather than after.
type GalleryCard struct {
	GalleryEntry
	Label    string `json:"label"`
	Kind     Kind   `json:"kind"`
	Module   string `json:"module"`
	Unit     string `json:"unit,omitempty"`
	Screen   string `json:"screen,omitempty"`
	SpanCols int    `json:"span_cols"`
	SpanRows int    `json:"span_rows"`
}

// Gallery is the GET /api/v1/dashboard/widgets/gallery payload.
type Gallery struct {
	Profile string `json:"profile"`
	// Entries is always an array, never null: an empty gallery is a real state (the driver has one
	// by design, and so does any build ahead of 0208) and it must read as "nothing here" rather than
	// as something the client has to guard.
	Entries []GalleryCard `json:"entries"`
}

// maxBlurbRune is 0208's CHECK, restated so an over-long blurb is trimmed here rather than becoming
// a constraint violation surfacing as a 500 three layers down. Same posture as maxTitleRune.
const maxBlurbRune = 120

// -- the widget an entry becomes -----------------------------------------------------------------

// AsWidget projects an entry onto the widget it would place. ONE function, so the preview, the write
// gate and the browser cannot end up with three opinions about what tapping a gallery card does.
//
// The widget_key is the caller's to choose, the browser mints one when it PUTs the layout, so this
// carries the entry key through as a starting point and nothing downstream depends on it. Position 0
// is the doctrine's "the builder places a widget by PUTting the layout it is already showing with
// the new one at the top" (widget_handler.go:33-37).
func (e GalleryEntry) AsWidget() Widget {
	return Widget{
		Key: e.Key, Shape: e.Shape, Measure: e.Measure, Split: e.Split,
		Range: e.Range, Size: e.Size, Cols: e.Cols, Rows: e.Rows,
		Title: e.Title, Position: 0, Visible: true,
	}
}

// -- resolution ----------------------------------------------------------------------------------

// GalleryCardsFor turns stored entries into what goes on the wire, DROPPING every entry whose measure
// is not in `allowed`, and dropping every entry a dealer switched off.
//
// THE MEASURE DROP IS THE BYTE-ABSENCE, the same one WidgetViewsFor performs and the same bare
// `continue`. An entry naming a measure this caller may not see is not in the slice, so the response
// bytes never carry its measure id, its label or its blurb. An entry naming a measure this BUILD does
// not have is dropped by the same line, the 0172 posture, so a retired measure leaves no dead card
// in anybody's gallery.
//
// THE RANGE IS REPAIRED, NOT DROPPED, exactly as it is on the layout read: a window is not a
// permission, and an entry whose stored window its measure cannot honour would otherwise show a
// preview captioned with something the number does not measure. Coerce to the measure's natural
// window and the card says something true.
//
// POSITIONS ARE RENUMBERED over the survivors, so a caller who is offered four of six entries gets
// 0..3 and not 0, 2, 3, 5. A gallery is a grid the browser lays out in order; a gap in the numbers is
// a question it should not have to answer.
func GalleryCardsFor(rows []GalleryEntry, allowed map[string]MetricMeta) []GalleryCard {
	out := make([]GalleryCard, 0, len(rows))
	for _, e := range rows {
		if !e.Visible {
			continue // retired by the dealer: absent, not greyed
		}
		m, ok := widgetMeasureMeta(e.Measure, allowed)
		if !ok {
			continue // BYTE-ABSENT. Not hidden, not empty, absent.
		}
		if len(m.Ranges) > 0 && !containsString(m.Ranges, e.Range) {
			e.Range = m.Ranges[0]
		}
		if e.Title == "" {
			e.Title = AutoTitle(m, e.Split)
		}
		e.Position = len(out)
		cols, rowsN := SpanFor(e.AsWidget())
		out = append(out, GalleryCard{
			GalleryEntry: e, Label: m.Label, Kind: m.Kind, Module: m.Module,
			Unit: m.Unit, Screen: m.Screen, SpanCols: cols, SpanRows: rowsN,
		})
	}
	return out
}

// -- validation ----------------------------------------------------------------------------------

// NormalizeGallery cleans a submitted gallery for storage WITHOUT deciding anything about
// permissions, the same division of labour NormalizeWidgets keeps, and for the same reason: a
// forbidden measure is a REFUSAL and dropping it quietly would swallow exactly the request somebody
// most wants to hear about.
//
// It delegates every field a gallery entry shares with a widget to NormalizeWidgets rather than
// restating the rules, so the two surfaces cannot drift into disagreeing about what a legal size or
// a legal window is. What is left here is the two fields only an entry has: the blurb, and ordering
// by the entry's own key.
func NormalizeGallery(rows []GalleryEntry) []GalleryEntry {
	// The blurb cannot ride the projection, Widget has no such field, so it is carried in a map and
	// picked up again afterwards. FIRST OCCURRENCE WINS, over a copy sorted by the SAME comparator
	// NormalizeWidgets uses, because NormalizeWidgets collapses a duplicate key to its first
	// occurrence in ITS ordering: matching the sort is what guarantees the blurb that comes back is
	// the surviving row's blurb and not the discarded twin's.
	sorted := append([]GalleryEntry(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Position != sorted[j].Position {
			return sorted[i].Position < sorted[j].Position
		}
		return sorted[i].Key < sorted[j].Key
	})
	blurbs := make(map[string]string, len(sorted))
	for _, e := range sorted {
		if _, seen := blurbs[e.Key]; !seen {
			blurbs[e.Key] = trimRunes(e.Blurb, maxBlurbRune)
		}
	}
	projected := make([]Widget, 0, len(rows))
	for _, e := range rows {
		w := e.AsWidget()
		w.Position = e.Position
		w.Visible = e.Visible
		projected = append(projected, w)
	}
	clean := NormalizeWidgets(projected)
	out := make([]GalleryEntry, 0, len(clean))
	for _, w := range clean {
		out = append(out, GalleryEntry{
			Key: w.Key, Shape: w.Shape, Measure: w.Measure, Split: w.Split,
			Range: w.Range, Size: w.Size, Cols: w.Cols, Rows: w.Rows,
			Title: w.Title, Blurb: blurbs[w.Key], Position: w.Position, Visible: w.Visible,
		})
	}
	return out
}

// ValidateGallery is the WRITE GATE, and it is ValidateWidgets, deliberately the same function and
// not a copy of it.
//
// An entry is a widget somebody pre-filled, so every question worth asking about it is a question
// already asked about a widget: is the measure in this catalog, is the shape one that measure
// declares, the split, the window, the span. A second implementation would be a second place for the
// answer to be wrong, and the one that drifted would be this one, the gallery is written by an admin
// once a quarter and the layout is written on every drag.
//
// NoBranch, always. A gallery entry has no branch column at all (0208), so the projection carries the
// empty string and NoBranch allows it; passing NoBranch rather than AnyBranch is what makes that a
// STATED rule instead of an accident of the schema, so the day somebody adds the column the gate is
// already refusing it and says so in words.
func ValidateGallery(rows []GalleryEntry, allowed map[string]MetricMeta) error {
	projected := make([]Widget, 0, len(rows))
	for _, e := range rows {
		projected = append(projected, e.AsWidget())
	}
	return ValidateWidgets(projected, allowed, NoBranch)
}

// trimRunes bounds a string by RUNES rather than bytes, because the CHECK it mirrors is
// char_length(). Cutting at a byte offset would split a multi-byte character and store something
// Postgres would reject for a reason nobody could read.
func trimRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
