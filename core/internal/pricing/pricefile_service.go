package pricing

// pricefile_service.go, parse a manufacturer price file, diff it against the retail anchor, and
// (separately, later, on purpose) stage the result.
//
// THE ORDER OF THE TWO ACTS IS THE WHOLE POINT. PreviewPriceFile touches no table. It reads the
// catalogue and the anchor, it computes, and it returns; run it fifty times and the database is
// unchanged. ApplyPriceFile is the only function here that writes, it re-derives the entire diff
// from the file it is handed rather than accepting one the client computed, and it refuses unless
// the caller can name, by digest, the preview it is committing.
//
// WHY APPLY STAGES A BATCH INSTEAD OF MOVING THE PRICES ITSELF.
//
// A-08 already has an apply, and it is guarded: a batch goes STAGED → APPROVED → APPLIED, and
// ApproveBatch refuses when the approver is the person who made it (batch_service.go, errSeparation).
// That is a maker-checker control over every price this dealer charges, and it exists because a
// price list is the one file where a single wrong column moves every number in the catalogue at once.
//
// The alternative I rejected was to write pricing_list_prices directly from this apply. It would
// make the screen's fourth step feel more final, and it would hand one person, the same person who
// chose the mapping, uploaded the file and read the preview, the ability to reprice the entire yard
// with no second signature. A new endpoint that quietly routes around a shipped money control is a
// worse defect than a fourth step that hands off. So apply COMMITS THE IMPORT: it fixes the mapping
// as the vendor's profile, writes the change lines into a STAGED batch, and writes the audit row.
// The second signature, and the write to the anchor, stay where they already are.
//
// This is stated in the report as a boundary, not hidden as an implementation detail.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// Price-file errors. Each maps to a status through ClientStatus/writeErr like the rest of the module.
var (
	errPriceFileStoreUnset = errors.New("pricing: the price-file import is not configured on this server")
	errNoVendor            = errors.New("pricing: pick a manufacturer before importing a price file")
	errStalePreview        = errors.New("pricing: this file or its column mapping changed since the preview, preview it again before applying")
	errCatalogUnread       = errors.New("pricing: the product catalogue could not be read, so no import can be checked against it")
	errNothingToStage      = errors.New("pricing: this file moves no prices, check the column mapping before applying")
)

const (
	// previewLineCap is how many rows a preview LISTS. The counts are always over the whole file;
	// this bounds the payload, not the arithmetic. 200 fills a worklist several screens deep, and a
	// price list long enough to be truncated is one nobody was going to read line by line anyway.
	previewLineCap = 200
	// previewLineMax is the ceiling a caller may raise the cap to.
	previewLineMax = 1000
	// priceFileMaxBytes bounds the upload. An xlsx of a few thousand rows is under a megabyte; 12 MiB
	// is generous for a workbook carrying images somebody forgot to delete.
	priceFileMaxBytes = 12 << 20
)

// PriceFileInput is one request against this surface. The same struct serves preview and apply so
// the two cannot drift about what a file, a mapping or a vendor is.
type PriceFileInput struct {
	VendorID string
	UserID   string // the caller the auth guard resolved, never a body field
	FileName string
	Data     []byte
	// Mapping is the caller's column choice. Nil means "use what is stored, or guess", which is what
	// the first request of a session sends, before the operator has touched the mapping step.
	Mapping map[string]string
	// Digest is required by apply and ignored by preview. See ApplyPriceFile.
	Digest string
	// EffectiveDate schedules the staged batch. Nil means the batch carries no date and applies as
	// soon as it is approved, which is A-08's own behaviour for a dateless batch.
	EffectiveDate *time.Time
	Limit         int
}

// - parsing -

// parsedFile is a price file reduced to the only shape the diff needs.
type parsedFile struct {
	format  string
	headers []string   // verbatim, for the mapping step to offer
	keys    []string   // headers lower-cased and de-duplicated, for lookup
	rows    [][]string // blank-padded to len(keys)
}

// value reads one column of one row by mapped header name, case-insensitively. A mapping naming a
// header the file does not have returns "" rather than an error: the honest report of a wrong
// mapping is a preview full of unpriced rows with the header list sitting right beside it, not a
// 400 that says "bad column" and shows the operator nothing to fix it with.
func (p parsedFile) value(row []string, header string) string {
	h := strings.ToLower(strings.TrimSpace(header))
	if h == "" {
		return ""
	}
	for i, k := range p.keys {
		if k == h && i < len(row) {
			return strings.TrimSpace(row[i])
		}
	}
	return ""
}

// parsePriceFile reads CSV or XLSX and normalises both into the same grid.
//
// THE FORMAT IS DECIDED BY THE BYTES, NOT THE FILENAME. A manufacturer's "pricelist.xlsx" that is
// actually a CSV is a real thing that happens, and so is the reverse; sniffing the zip magic and
// falling through to CSV means both import instead of one of them producing a parse error about a
// format nobody chose.
func parsePriceFile(data []byte) (parsedFile, error) {
	if len(data) == 0 {
		return parsedFile{}, badReq("the uploaded file is empty")
	}
	if len(data) > priceFileMaxBytes {
		return parsedFile{}, badReq("the uploaded file is larger than this import accepts")
	}
	var (
		grid   [][]string
		format string
		err    error
	)
	if looksLikeXLSX(data) {
		format = FormatXLSX
		grid, err = readXLSX(data)
		if err != nil {
			return parsedFile{}, err
		}
	} else {
		format = FormatCSV
		grid, err = readCSVGrid(data)
		if err != nil {
			return parsedFile{}, err
		}
	}
	// The header row is the first row carrying any text. Manufacturer exports routinely open with a
	// title row or two of blanks; skipping them here is why the mapping step is offered real column
	// names instead of ["", "", ""].
	start := -1
	for i, r := range grid {
		if rowHasText(r) {
			start = i
			break
		}
	}
	if start < 0 {
		return parsedFile{}, badReq("the file has no header row")
	}
	pf := parsedFile{format: format}
	seen := map[string]int{}
	for i, h := range grid[start] {
		name := strings.TrimSpace(h)
		if name == "" {
			name = fmt.Sprintf("column %d", i+1)
		}
		key := strings.ToLower(name)
		// A duplicated header is legal in a spreadsheet and fatal to a lookup by name, so the second
		// one is disambiguated rather than shadowing the first. The operator sees both in the picker.
		if n, dup := seen[key]; dup {
			seen[key] = n + 1
			name = fmt.Sprintf("%s (%d)", name, n+1)
			key = strings.ToLower(name)
		} else {
			seen[key] = 1
		}
		pf.headers = append(pf.headers, name)
		pf.keys = append(pf.keys, key)
	}
	for _, r := range grid[start+1:] {
		if !rowHasText(r) {
			continue // blank separator rows, and the trailing blanks a spreadsheet always carries
		}
		pf.rows = append(pf.rows, r)
	}
	return pf, nil
}

func rowHasText(r []string) bool {
	for _, c := range r {
		if strings.TrimSpace(c) != "" {
			return true
		}
	}
	return false
}

// readCSVGrid reads a CSV into the same positional grid the xlsx reader produces.
//
// It does not reuse batch_service.go's parseCSV, which returns a map per row keyed by header: a map
// cannot represent a duplicated header (the second column silently wins) and cannot represent a
// header row that is not the first line. Both happen in manufacturer exports, and the grid handles
// them where the map hid them.
//
// A UTF-8 BOM is stripped. Excel writes one on every CSV it exports, and a BOM left on the front of
// the first header prefixes "SKU" with an invisible byte-order mark, which matches no mapping and produces a file where
// every row is unpriced for a reason nobody can see on screen.
func readCSVGrid(data []byte) ([][]string, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	cr := csv.NewReader(bytes.NewReader(data))
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1 // manufacturer exports are ragged; a short row is a short row, not a fault
	cr.LazyQuotes = true    // a stray quote inside a product description must not lose the whole file
	grid, err := cr.ReadAll()
	if err != nil {
		return nil, badReq("the file could not be read as CSV: %v", err)
	}
	return grid, nil
}

// - the mapping -

// guessMappingOffers is the subset of the four columns a guess is allowed to fill. It is the whole
// list MINUS the trade column, see guessMapping for why that omission is the point of this variable
// existing rather than the loop simply reading PriceFileMapKeys.
var guessMappingOffers = []string{MapSKU, MapCost, MapRetail}

// guessMapping proposes a column mapping from the file's own headers.
//
// IT IS OFFERED AS A GUESS AND LABELLED AS ONE. MappingSource on the preview says GUESSED, the
// screen shows it as a starting point, and nothing is saved until the operator applies, which is
// the difference between this and pre-seeding a profile, the thing migration 0198 refuses to do.
// The heuristics are deliberately narrow: an exact match on a known synonym, then a contains match,
// and NOTHING for a header nobody recognises. Guessing wide is how "Cost Centre" becomes the cost
// column.
//
// AND IT NEVER GUESSES THE TRADE COLUMN. That is not an oversight and it is the most important line
// in this function. trade_col is the HIGHEST-PRECEDENCE derivation anchor, deriveRetail takes the
// trade path ahead of cost×markup and ahead of manufacturer retail, so proposing it flips the
// pricing basis for every row in the file. A guess may fill in a column; it may not choose the
// arithmetic. A manufacturer file with a "Trade" column is common, and if this function mapped it,
// the difference between a file priced at cost×1.85 and one priced at trade+estimated-FOB would be a
// heuristic nobody was asked about. So the trade path is only ever entered by a person ticking that
// column themselves, and the preview then shows them every line's anchor change before anything is
// committed. guessMappingOffers below pins this.
func guessMapping(headers []string) map[string]string {
	synonyms := map[string][]string{
		MapSKU:    {"sku", "item", "item no", "item number", "product code", "code", "part", "part no"},
		MapCost:   {"cost", "dealer", "dealer net", "net", "dealer cost", "your cost", "buy"},
		MapRetail: {"retail", "list", "msrp", "list price", "retail price", "suggested retail"},
	}
	out := map[string]string{}
	taken := map[string]bool{}
	// Exact matches first, across every key, before any contains match is considered, otherwise a
	// header literally called "Cost" can lose to "Dealer Cost Centre" on a contains pass.
	for _, key := range guessMappingOffers {
		for _, h := range headers {
			lh := strings.ToLower(strings.TrimSpace(h))
			if taken[lh] {
				continue
			}
			for _, s := range synonyms[key] {
				if lh == s {
					out[key], taken[lh] = h, true
					break
				}
			}
			if out[key] != "" {
				break
			}
		}
	}
	for _, key := range guessMappingOffers {
		if out[key] != "" {
			continue
		}
		for _, h := range headers {
			lh := strings.ToLower(strings.TrimSpace(h))
			if taken[lh] {
				continue
			}
			for _, s := range synonyms[key] {
				if strings.Contains(lh, s) {
					out[key], taken[lh] = h, true
					break
				}
			}
			if out[key] != "" {
				break
			}
		}
	}
	return out
}

// resolveMapping decides which of the three mappings this preview runs under, and says which.
func (s *Service) resolveMapping(ctx context.Context, vendorID string, requested map[string]string, headers []string) (map[string]string, string, ImportProfile, string) {
	if len(requested) > 0 {
		clean := map[string]string{}
		for _, k := range PriceFileMapKeys {
			if v := strings.TrimSpace(requested[k]); v != "" {
				clean[k] = v
			}
		}
		if len(clean) > 0 {
			prof, origin, _ := s.activeProfile(ctx, vendorID)
			return clean, "REQUESTED", prof, origin
		}
	}
	prof, origin, err := s.activeProfile(ctx, vendorID)
	if err == nil && len(prof.Mapping) > 0 {
		stored := map[string]string{}
		for _, k := range PriceFileMapKeys {
			if v := strings.TrimSpace(prof.Mapping[k]); v != "" {
				stored[k] = v
			}
		}
		if len(stored) > 0 {
			return stored, "STORED", prof, origin
		}
	}
	return guessMapping(headers), "GUESSED", prof, origin
}

// vendorNameFor resolves the manufacturer's name for the screen header and for the name a lazily
// created profile is given.
//
// IT ASKS THE DIRECTORY, NOT THE PROFILE, and that is the whole point of it existing. The profile
// join carries the vendor name, and a vendor with no profile has no join, which is precisely the
// twenty-three this surface was built for. Reading the name off the profile would leave the header
// blank and the lazily created profile named after the uploaded FILE ("browns.csv"), which is a
// picker entry that tells the next operator nothing about which manufacturer it maps.
//
// Degrades to "" and never errors: an unnamed vendor is a header showing the picker's own selection
// instead, not a preview that refuses to run.
func (s *Service) vendorNameFor(ctx context.Context, vendorID string) string {
	if s.vendors == nil {
		return ""
	}
	list, err := s.vendors.ListVendors(ctx)
	if err != nil {
		return ""
	}
	for _, v := range list {
		if v.VendorID == vendorID {
			return v.Name
		}
	}
	return ""
}

func (s *Service) activeProfile(ctx context.Context, vendorID string) (ImportProfile, string, error) {
	if s.pricefile == nil {
		return ImportProfile{}, "", errPriceFileStoreUnset
	}
	return s.pricefile.ActiveProfileForVendor(ctx, vendorID)
}

// mergedMapping is what apply SAVES: the four keys this screen owns, written over whatever the
// profile already held. A merge and not a replace, because 0039's seeded profiles carry an
// `effective_col` this screen does not offer, and a screen that silently deletes a key it declined
// to show is a screen that edits things it does not display.
func mergedMapping(stored, chosen map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range stored {
		out[k] = v
	}
	for _, k := range PriceFileMapKeys {
		if v := strings.TrimSpace(chosen[k]); v != "" {
			out[k] = v
		} else {
			// An explicitly cleared column is cleared, not left at its old value. Un-mapping the
			// trade column is how a manager takes a file OFF the trade+FOB anchor, and a merge that
			// could only add would make that impossible from this screen.
			delete(out, k)
		}
	}
	return out
}

// - the cost gate -

// costVisible is the ONE policy question this surface asks, asked once per request.
//
// FAIL CLOSED AT EVERY STEP, and each step has a different reason:
//   - no store: the policy row is unreadable, so the dealer's answer is unknown. Unknown is not yes.
//   - HIDDEN: nobody, manager or not. It is the dealer's switch and it means what it says.
//   - ALL_STAFF: anybody the read guard already admitted.
//   - MANAGER_UP (and the empty string a partial read yields): the seam decides, and a nil seam or a
//     seam that errors decides no. An unwired gate that defaults open turns a wiring mistake into a
//     data leak; one that defaults closed turns it into a support call.
func (s *Service) costVisible(ctx context.Context, userID string) bool {
	if s.pricefile == nil {
		return false
	}
	vis, err := s.pricefile.ImportCostVisibility(ctx, "")
	if err != nil {
		if s.log != nil {
			s.log.Warn("pricing: import cost visibility unreadable, cost withheld", "err", err)
		}
		return false
	}
	switch vis {
	case settings.VisibilityHidden:
		return false
	case settings.VisibilityAllStaff:
		return true
	default:
		if s.costvis == nil || strings.TrimSpace(userID) == "" {
			return false
		}
		ok, err := s.costvis.IsManager(ctx, userID)
		if err != nil {
			if s.log != nil {
				s.log.Error("pricing: caller role lookup failed, cost withheld", "user_id", userID, "err", err)
			}
			return false
		}
		return ok
	}
}

// - the diff -

// diffResult is the whole computed answer, before it is capped for the wire.
type diffResult struct {
	lines   []PriceFileLine
	counts  PriceFileCounts
	checked bool
}

// buildDiff is the pure heart of this surface: file + mapping + catalogue + anchor → outcomes.
// It writes nothing, and both preview and apply run it, which is what makes the preview a promise
// rather than a demonstration.
func (s *Service) buildDiff(ctx context.Context, pf parsedFile, mapping map[string]string, policy AnchorPolicy, allowCost bool) (diffResult, error) {
	skuCol, costCol := mapping[MapSKU], mapping[MapCost]
	retailCol, tradeCol := mapping[MapRetail], mapping[MapTrade]

	// One pass to collect the SKUs, so the catalogue is asked once. Order is preserved and duplicates
	// are kept: a price list that names the same SKU twice is a fact about the file the operator
	// should see on two rows, not a de-duplication we performed on their behalf.
	skus := make([]string, 0, len(pf.rows))
	seen := map[string]bool{}
	for _, r := range pf.rows {
		sku := strings.TrimSpace(pf.value(r, skuCol))
		if sku == "" || seen[strings.ToLower(sku)] {
			continue
		}
		seen[strings.ToLower(sku)] = true
		skus = append(skus, sku)
	}

	catalog := map[string]CatalogSKU{}
	checked := false
	if s.catskus != nil && len(skus) > 0 {
		found, err := s.catskus.LookupSKUs(ctx, skus)
		if err != nil {
			// DEGRADE, DO NOT REFUSE. The preview still renders; CatalogChecked=false is what stops it
			// from being read as "we stock none of this". Apply refuses separately.
			if s.log != nil {
				s.log.Error("pricing: catalogue lookup failed during a price-file preview", "err", err)
			}
		} else {
			checked = true
			for k, v := range found {
				catalog[strings.ToLower(k)] = v
			}
		}
	} else if s.catskus != nil {
		checked = true // nothing to look up; the seam is present and the answer is "no SKUs"
	}

	productIDs := make([]string, 0, len(catalog))
	for _, e := range catalog {
		productIDs = append(productIDs, e.ProductID)
	}
	anchors := map[string]int64{}
	if s.pricefile != nil && len(productIDs) > 0 {
		got, err := s.pricefile.ListPricesForProducts(ctx, productIDs)
		if err != nil {
			// The anchor is pricing's own table and an unreadable anchor cannot be papered over: every
			// matched row would read NEW, which is the most dangerous wrong answer available here
			// (it says "nothing is being overwritten"). Refuse the whole diff instead.
			return diffResult{}, err
		}
		anchors = got
	}

	out := diffResult{checked: checked}
	for i, r := range pf.rows {
		sku := strings.TrimSpace(pf.value(r, skuCol))
		if sku == "" {
			continue // a row with no SKU is not a price; blank rows are already gone
		}
		line := PriceFileLine{Row: i + 1, SKU: sku}

		cost, err := parseMoneyCents(pf.value(r, costCol))
		if err != nil {
			return diffResult{}, badReq("row %d (%s): %v", line.Row, sku, err)
		}
		trade, err := parseMoneyCents(pf.value(r, tradeCol))
		if err != nil {
			return diffResult{}, badReq("row %d (%s): %v", line.Row, sku, err)
		}
		mfr, err := parseMoneyCents(pf.value(r, retailCol))
		if err != nil {
			return diffResult{}, badReq("row %d (%s): %v", line.Row, sku, err)
		}

		dr := deriveRetail(policy, derivationInput{costCents: cost, tradeCents: trade, mfrRetailCents: mfr})
		line.DerivedFrom = dr.source
		line.FreightEstimated = dr.freightEstimated
		line.Costs = costsFor(allowCost, costInputs{
			cost: cost, trade: trade,
			markupBps: markupUsed(dr.source, policy.MarkupBps),
			freight:   dr.freightCents, freighted: dr.freightEstimated,
		})

		entry, matched := catalog[strings.ToLower(sku)]
		if !checked || !matched {
			line.Outcome = OutcomeUnknown
			out.counts.Unknown++
			out.lines = append(out.lines, line)
			out.counts.Rows++
			continue
		}
		line.ProductID, line.ProductName = entry.ProductID, entry.Name
		if before, ok := anchors[entry.ProductID]; ok {
			b := before
			line.CurrentRetailCents = &b
		}
		switch {
		case !dr.ok:
			// Matched, and nothing to price it from. Truthfully unchanged, and counted separately so
			// the truth does not read as agreement. See PriceFileCounts.Unpriced.
			line.Outcome = OutcomeUnchanged
			out.counts.Unchanged++
			out.counts.Unpriced++
		default:
			after := dr.retailCents
			line.NewRetailCents = &after
			line.VarianceBps = varianceBps(line.CurrentRetailCents, after)
			if line.VarianceBps != nil && absInt64(*line.VarianceBps) > policy.VarianceAlertBps {
				line.IsOutlier = true
				out.counts.Outliers++
			}
			switch {
			case line.CurrentRetailCents == nil:
				line.Outcome = OutcomeNew
				out.counts.New++
			case *line.CurrentRetailCents == after:
				line.Outcome = OutcomeUnchanged
				out.counts.Unchanged++
			default:
				line.Outcome = OutcomeChanged
				out.counts.Changed++
			}
		}
		out.lines = append(out.lines, line)
		out.counts.Rows++
	}
	return out, nil
}

// markupUsed reports the multiplier that ACTUALLY priced the row, which is not always the policy's.
// A trade-anchored or manufacturer-retail-anchored row did not use the markup at all, and reporting
// one would invite a reader to divide by it and get a cost that never existed.
func markupUsed(source string, policyMarkup int64) int64 {
	if source == DeriveCostMarkup {
		return policyMarkup
	}
	return 0
}

// - preview -

// PreviewPriceFile answers "what would this file do" and writes NOTHING.
func (s *Service) PreviewPriceFile(ctx context.Context, in PriceFileInput) (PriceFilePreview, error) {
	if s.pricefile == nil || s.batch == nil {
		return PriceFilePreview{}, errPriceFileStoreUnset
	}
	if strings.TrimSpace(in.VendorID) == "" {
		return PriceFilePreview{}, errNoVendor
	}
	pf, err := parsePriceFile(in.Data)
	if err != nil {
		return PriceFilePreview{}, err
	}
	mapping, source, prof, _ := s.resolveMapping(ctx, in.VendorID, in.Mapping, pf.headers)
	policy, err := s.batch.AnchorPolicy(ctx, "")
	if err != nil {
		return PriceFilePreview{}, err
	}
	if prof.MarkupBps > 0 {
		policy.MarkupBps = prof.MarkupBps
	}
	allowCost := s.costVisible(ctx, in.UserID)
	diff, err := s.buildDiff(ctx, pf, mapping, policy, allowCost)
	if err != nil {
		return PriceFilePreview{}, err
	}

	limit := in.Limit
	if limit <= 0 {
		limit = previewLineCap
	}
	if limit > previewLineMax {
		limit = previewLineMax
	}
	shown := orderForReview(diff.lines)
	truncated := false
	if len(shown) > limit {
		shown, truncated = shown[:limit], true
	}

	out := PriceFilePreview{
		VendorID:       in.VendorID,
		VendorName:     firstNonEmpty(prof.VendorName, s.vendorNameFor(ctx, in.VendorID)),
		ProfileID:      prof.ID,
		Format:         pf.format,
		Headers:        pf.headers,
		Mapping:        mapping,
		MappingSource:  source,
		Counts:         diff.counts,
		Lines:          shown,
		Truncated:      truncated,
		CatalogChecked: diff.checked,
		Digest:         priceFileDigest(pf, mapping),
		CostsVisible:   allowCost,
	}
	if out.Lines == nil {
		out.Lines = []PriceFileLine{}
	}
	if out.Headers == nil {
		out.Headers = []string{}
	}
	return out, nil
}

// orderForReview puts the rows that need a decision first: the biggest movers, then the new prices,
// then the SKUs we do not stock, then the rows that agree with us. A cap that took the file's own
// order would truncate away exactly the rows worth reading, which is how a preview becomes theatre.
func orderForReview(lines []PriceFileLine) []PriceFileLine {
	rank := map[string]int{OutcomeChanged: 0, OutcomeNew: 1, OutcomeUnknown: 2, OutcomeUnchanged: 3}
	out := make([]PriceFileLine, len(lines))
	copy(out, lines)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank[out[i].Outcome], rank[out[j].Outcome]
		if ri != rj {
			return ri < rj
		}
		vi, vj := int64(0), int64(0)
		if out[i].VarianceBps != nil {
			vi = absInt64(*out[i].VarianceBps)
		}
		if out[j].VarianceBps != nil {
			vj = absInt64(*out[j].VarianceBps)
		}
		return vi > vj
	})
	return out
}

// priceFileDigest fingerprints WHAT THE OPERATOR CONTROLS: the parsed rows and the mapping.
//
// It deliberately excludes the catalogue and the anchor, which are the two inputs that can move
// underneath a preview without anybody touching the file. Including them would make the digest fail
// whenever another lane repriced one product mid-review, a refusal that teaches the operator to
// re-preview reflexively rather than to look. What the digest is FOR is the other thing: guaranteeing
// that the bytes being applied are the bytes that were read, under the columns that were chosen.
// Drift in the anchor is handled honestly instead, apply re-derives the diff and records the counts
// it actually produced on the audit row, so the run says what happened rather than what was expected.
func priceFileDigest(pf parsedFile, mapping map[string]string) string {
	h := sha256.New()
	keys := append([]string(nil), PriceFileMapKeys...)
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\x00", k, strings.ToLower(strings.TrimSpace(mapping[k])))
	}
	fmt.Fprintf(h, "\x01%s\x01", pf.format)
	for _, k := range pf.keys {
		fmt.Fprintf(h, "%s\x00", k)
	}
	for _, r := range pf.rows {
		for _, c := range r {
			fmt.Fprintf(h, "%s\x00", strings.TrimSpace(c))
		}
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// - apply -

// ApplyPriceFile commits the import: the mapping becomes the vendor's profile, the movers become a
// STAGED price batch, and the whole thing gets an audit row. It does NOT write the retail anchor -
// see the file header for why, and say so on the screen rather than in a comment alone.
func (s *Service) ApplyPriceFile(ctx context.Context, in PriceFileInput) (PriceFileApplyResult, error) {
	if s.pricefile == nil || s.batch == nil {
		return PriceFileApplyResult{}, errPriceFileStoreUnset
	}
	if strings.TrimSpace(in.VendorID) == "" {
		return PriceFileApplyResult{}, errNoVendor
	}
	pf, err := parsePriceFile(in.Data)
	if err != nil {
		return PriceFileApplyResult{}, err
	}
	mapping, _, prof, _ := s.resolveMapping(ctx, in.VendorID, in.Mapping, pf.headers)

	// THE BINDING. A caller who cannot name the preview they read is applying something else.
	if strings.TrimSpace(in.Digest) == "" || in.Digest != priceFileDigest(pf, mapping) {
		return PriceFileApplyResult{}, errStalePreview
	}

	policy, err := s.batch.AnchorPolicy(ctx, "")
	if err != nil {
		return PriceFileApplyResult{}, err
	}
	if prof.MarkupBps > 0 {
		policy.MarkupBps = prof.MarkupBps
	}
	// The apply's OWN diff, re-derived. The client's numbers are never trusted, and the cost gate is
	// irrelevant here because nothing this computes is returned as cost, but it is asked anyway, so
	// the ONE constructor of a costs struct stays the only path a cost can travel.
	diff, err := s.buildDiff(ctx, pf, mapping, policy, s.costVisible(ctx, in.UserID))
	if err != nil {
		return PriceFileApplyResult{}, err
	}
	if !diff.checked {
		return PriceFileApplyResult{}, errCatalogUnread
	}
	if diff.counts.Movers() == 0 {
		return PriceFileApplyResult{}, errNothingToStage
	}

	lines := stageLines(diff.lines)
	now := s.now().UTC()
	run := PriceImportRun{
		VendorID:      in.VendorID,
		SourceName:    sanitizeFileName(in.FileName),
		SourceFormat:  pf.format,
		SourceBytes:   int64(len(in.Data)),
		ContentDigest: in.Digest,
		MappingUsed:   mergedMapping(prof.Mapping, mapping),
		Counts:        diff.counts,
		ImportedBy:    in.UserID,
	}
	staged, err := s.pricefile.StagePriceFile(ctx, priceFileStage{
		VendorID:      in.VendorID,
		ProfileID:     prof.ID,
		ProfileName:   profileNameFor(firstNonEmpty(prof.VendorName, s.vendorNameFor(ctx, in.VendorID)), run.SourceName),
		Mapping:       run.MappingUsed,
		BatchNo:       batchNoFor(now, in.Digest),
		EffectiveDate: in.EffectiveDate,
		SourceRef:     run.SourceName,
		Lines:         lines,
		OutlierCount:  diff.counts.Outliers,
		Run:           run,
		UserID:        in.UserID,
		Now:           now,
	})
	if err != nil {
		return PriceFileApplyResult{}, err
	}
	batch, err := s.batch.GetBatch(ctx, staged.BatchID)
	if err != nil {
		// The write succeeded; only the read-back failed. Returning the ids we already hold beats
		// failing an operation that has already happened, a caller told "error" after a successful
		// stage will import the file again.
		batch = PriceBatch{ID: staged.BatchID, BatchNo: staged.BatchNo, Status: BatchStaged}
	}
	return PriceFileApplyResult{
		RunID:          staged.RunID,
		Batch:          batch,
		Counts:         diff.counts,
		Staged:         staged.Staged,
		ProfileCreated: staged.ProfileCreated,
	}, nil
}

// stageLines is what actually gets written into price_change_lines.
//
// MOVERS AND UNKNOWNS, NOT EVERYTHING. An UNCHANGED row would be a change line recording no change:
// it inflates the batch's line count, it puts a no-op through the maker-checker review, and at
// apply-to-anchor time it rewrites a price to the value it already holds and appends a change-log
// row saying so. UNKNOWN rows ARE written, with a NULL product_id, because the unmatched-SKU report
// is the only artefact that survives the session and "which SKUs did the file name that we do not
// stock" is the question a buyer asks the week after.
func stageLines(lines []PriceFileLine) []PriceChangeLine {
	out := make([]PriceChangeLine, 0, len(lines))
	for _, l := range lines {
		if l.Outcome == OutcomeUnchanged {
			continue
		}
		cl := PriceChangeLine{
			ProductID:         l.ProductID,
			SKU:               l.SKU,
			BeforeRetailCents: l.CurrentRetailCents,
			AfterRetailCents:  l.NewRetailCents,
			VarianceBps:       l.VarianceBps,
			IsOutlier:         l.IsOutlier,
			Unmatched:         l.Outcome == OutcomeUnknown,
		}
		if l.Costs != nil {
			cl.AfterCostCents = l.Costs.FileCostCents
		}
		out = append(out, cl)
	}
	return out
}

// batchNoFor builds a human-readable, unique batch number. The date is what an operator scans for;
// the digest prefix is what stops two imports in the same second from colliding on the CITEXT UNIQUE.
func batchNoFor(now time.Time, digest string) string {
	short := digest
	if len(short) > 6 {
		short = short[:6]
	}
	return fmt.Sprintf("PF-%s-%s", now.Format("20060102-150405"), strings.ToUpper(short))
}

// profileNameFor names a lazily created profile. The vendor's own name where we have it, because the
// picker groups by vendor and a profile called "price file" thirty times over is a list of nothing -
// and the filename, which is the other thing to hand, names the FILE rather than the mapping and goes
// stale the moment the manufacturer sends its next quarter.
func profileNameFor(vendorName, sourceName string) string {
	if vendorName != "" {
		return vendorName + " price file"
	}
	if sourceName != "" {
		return sourceName
	}
	return "Price file"
}

// firstNonEmpty returns the first of its arguments that carries text.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// sanitizeFileName keeps a filename readable and harmless. It is rendered on the audit screen and
// stored, so control characters and path segments come off; the extension and the words stay,
// because "TECHO_2026_Q3.xlsx" is the whole reason anybody looks at this column.
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 160 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// - reads -

// PriceFileVendors is the manufacturer picker's list: every vendor, each carrying its profile when
// it has one. A vendor with no profile is NOT hidden, it is the normal state of twenty-three of
// them, and hiding it would make the lazy creation unreachable.
func (s *Service) PriceFileVendors(ctx context.Context) ([]PriceFileVendor, error) {
	if s.vendors == nil {
		// Degrade: an empty picker renders as an empty state. A screen with nothing to choose is not
		// a screen that failed.
		return []PriceFileVendor{}, nil
	}
	list, err := s.vendors.ListVendors(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Error("pricing: vendor directory unreadable on the price-file screen", "err", err)
		}
		return []PriceFileVendor{}, nil
	}
	profiles := map[string]PriceFileVendor{}
	if s.pricefile != nil {
		if got, perr := s.pricefile.ProfileVendorRows(ctx); perr == nil {
			profiles = got
		}
	}
	for i := range list {
		if p, ok := profiles[list[i].VendorID]; ok {
			list[i].ProfileID = p.ProfileID
			list[i].Origin = p.Origin
			list[i].Mapping = p.Mapping
			list[i].LastImportAt = p.LastImportAt
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	return list, nil
}

// PriceImportRuns is the append-only import audit, newest first.
func (s *Service) PriceImportRuns(ctx context.Context, vendorID string, limit int) ([]PriceImportRun, error) {
	if s.pricefile == nil {
		return []PriceImportRun{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	runs, err := s.pricefile.ListImportRuns(ctx, vendorID, limit)
	if err != nil {
		// A read path never errors. An unreadable audit list renders as an empty list beside a screen
		// that still works, and the failure is in the log where somebody can act on it.
		if s.log != nil {
			s.log.Error("pricing: price-file import runs unreadable", "err", err)
		}
		return []PriceImportRun{}, nil
	}
	if runs == nil {
		runs = []PriceImportRun{}
	}
	return runs, nil
}

// decodeMappingJSON reads the mapping field of a multipart form. A malformed value is a bad request
// in words rather than a silent fall-back to the stored mapping: an operator who typed a mapping and
// got the profile's is being lied to about what they previewed.
func decodeMappingJSON(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, badReq("the column mapping is not readable")
	}
	return m, nil
}
