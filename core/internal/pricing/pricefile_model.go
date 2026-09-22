package pricing

// pricefile_model.go, the price-file import: types, the four diff outcomes, and the ONE place a
// cost number is allowed to enter a payload.
//
// - WHAT THIS IS, AND WHAT IT IS NOT -
//
// A-08 (batch_service.go) already imports a manufacturer CSV. Its ImportBatch parses the file,
// derives a retail anchor per row, WRITES the derived lines into price_change_lines and moves the
// batch to STAGED, and only then hands the caller a five-row sample it calls a "preview". That is a
// write with a summary printed after it. The acceptance clause here is the opposite claim: an import
// PREVIEWS BEFORE IT WRITES, which can only be true if the preview is a separate act that touches no
// table at all.
//
// So PreviewPriceFile below is a pure function of (file, mapping, catalog, policy). It creates no
// profile, no batch, no line and no run row; a caller may preview the same file forty times and the
// database will not know. ApplyPriceFile is the act that writes, and it re-derives the whole diff
// server-side from the file it is given rather than trusting a client to hand back what it was
// shown, a preview whose numbers the client could edit before committing them is not a control.
//
// WHY NOT JUST FIX ImportBatch. Because A-08's shape is load-bearing for its own screens (the batch
// console lists STAGED batches with line counts; the cost-compare picker reads those lines), and a
// preview that stopped writing them would break the two surfaces that depend on the write. This is
// additive: the same profiles, the same derivation engine, the same batch tables at the end.
//
// - THE FOUR OUTCOMES ARE A DIFF AGAINST THE RETAIL ANCHOR -
//
// Each row of the file lands in exactly one bucket, and each bucket implies a different action:
//
//	UNKNOWN    the SKU matches no catalog product. Nothing can be priced. Reported, never applied -
//	           0039's posture for an unmatched SKU, kept, because a price list naming a product we do
//	           not stock is a fact about the catalogue, not an error in the file.
//	NEW        the SKU matches a product carrying NO list price. The import would create one.
//	CHANGED    it matches a product whose list price differs from the derived one. This is the money.
//	UNCHANGED  it matches, and the derived price equals the one already on file. Counted, shown last,
//	           and NOT staged as a change line, see ApplyPriceFile.
//
// The bucket is decided against pricing_list_prices, which is ONE row per product org-wide (pricing
// is unified cross-yard, batch_model.go states it and this file does not relitigate it).

import (
	"context"
	"time"
)

// The four diff outcomes. They are wire values and reach a screen only through a word map
// (app/src/price-file-logic.ts), never interpolated raw.
const (
	OutcomeNew       = "NEW"
	OutcomeChanged   = "CHANGED"
	OutcomeUnchanged = "UNCHANGED"
	OutcomeUnknown   = "UNKNOWN"
)

// The two file formats this reader accepts. XLSX is read by a hand-written OOXML reader
// (xlsx_reader.go) because a third-party spreadsheet library is refused by client ruling (D-076 /
// DIB-441, the same ruling internal/export writes under).
const (
	FormatCSV  = "CSV"
	FormatXLSX = "XLSX"
)

// The mapping keys this surface owns. The profile's `mapping` JSONB may hold others (0039 seeded
// `effective_col`); a mapping written from here MERGES over the stored map rather than replacing it,
// so a key this screen does not offer is preserved rather than quietly deleted.
//
// effective_col is deliberately NOT offered. A-08's derivation never reads it, the effective date
// that governs an import is the BATCH's scheduled date, one date for the file, and a field that
// looks like it does something and does nothing is worse than an absent field.
const (
	MapSKU    = "sku_col"
	MapCost   = "cost_col"
	MapRetail = "retail_col"
	MapTrade  = "trade_col"
)

// PriceFileMapKeys is the offered set, in the order the screen shows them.
var PriceFileMapKeys = []string{MapSKU, MapCost, MapRetail, MapTrade}

// - the gated money -

// PriceFileCosts is EVERY cost number this surface can produce, in one struct, so that "may this
// caller see cost" is one question asked in one place instead of a field-by-field audit.
//
// ABSENT, NOT ZEROED AND NOT NULL. Each field is a pointer with omitempty and the struct itself is
// carried as a *PriceFileCosts with omitempty, so a refused caller's payload contains neither the
// numbers nor the key that would announce their existence. `"costs": null` is NOT absence, it is a
// byte sequence that says "there is a cost here and you may not have it", which is a different and
// more interesting message than silence, and the omitempty on the pointer is the only thing
// standing between this struct and that. pricefile_money_test.go scans the marshalled bytes for the
// literal `null` for exactly that reason, and marshals an ALLOWED caller first as its positive
// control so a scan that finds nothing cannot be a scan that was looking at nothing.
//
// WHY A POINTER WHERE quote.QuoteMargin USES A VALUE. The house precedent (quote/builder.go) carries
// its gated struct by value, so a refused caller receives `{}`. That is byte-absent and it is right
// for ONE glance object on ONE quote. This struct rides on every line of a file that can be five
// thousand rows long, and five thousand `"costs":{}` are byte-absent but not silent: they advertise
// a locked drawer on every row, which is an invitation rather than a refusal. A nil pointer with
// omitempty is absent AND quiet, and it costs the same policy call.
type PriceFileCosts struct {
	// FileCostCents is the manufacturer's dealer-net column for this row.
	FileCostCents *int64 `json:"file_cost_cents,omitempty"`
	// FileTradeCents is the GTA trade column, when the mapping names one. It is a cost: it is what
	// the yard pays, and the derivation adds an estimated FOB freight on top of it to reach retail.
	FileTradeCents *int64 `json:"file_trade_cents,omitempty"`
	// MarkupBps is the multiplier the derivation applied. It is gated with the cost columns and not
	// beside them by accident: retail ÷ markup IS cost, so a caller holding the derived retail and
	// this number holds the dealer net whether or not the cost column was sent.
	MarkupBps *int64 `json:"markup_bps,omitempty"`
	// FOBFreightCents is the estimated freight the trade path added. Same reasoning as MarkupBps -
	// retail minus freight is the trade price.
	FOBFreightCents *int64 `json:"fob_freight_cents,omitempty"`
}

// costInputs is what a caller offers the gate. It is a plain struct rather than four arguments so a
// fifth cost number cannot be added to the payload without passing through here.
type costInputs struct {
	cost      *int64
	trade     *int64
	markupBps int64
	freight   int64
	freighted bool
}

// costsFor is THE ONE METHOD IN THIS PACKAGE THAT PRODUCES A *PriceFileCosts.
//
// Every other function in the price-file surface receives one it did not build, which is what makes
// the gate auditable: there is a single `&PriceFileCosts{` in the package and a test pins that count
// at one. A second constructor would be a second policy decision, and the second one is always the
// one somebody forgets to gate.
//
// A refused caller gets nil, not an empty struct, not a struct of nils. See the type's header.
func costsFor(allowed bool, in costInputs) *PriceFileCosts {
	if !allowed {
		return nil
	}
	c := &PriceFileCosts{FileCostCents: in.cost, FileTradeCents: in.trade}
	if in.markupBps > 0 {
		m := in.markupBps
		c.MarkupBps = &m
	}
	if in.freighted {
		f := in.freight
		c.FOBFreightCents = &f
	}
	return c
}

// - the payloads -

// PriceFileVendor is one manufacturer the screen can import for. Vendor names are INTERNAL-ONLY
// (rule 1, 0039:3-4) and this type never leaves a staff route.
type PriceFileVendor struct {
	VendorID  string `json:"vendor_id"`
	VendorNo  string `json:"vendor_no"`
	Name      string `json:"name"`
	ProfileID string `json:"profile_id,omitempty"`
	// Origin is SEEDED for a mapping 0039 guessed and FIRST_IMPORT for one a person authored against
	// a real file. Empty when the vendor has no profile at all, which is the honest third state and
	// the one lazy creation exists to preserve.
	Origin       string            `json:"origin,omitempty"`
	Mapping      map[string]string `json:"mapping,omitempty"`
	LastImportAt *time.Time        `json:"last_import_at,omitempty"`
}

// PriceFileLine is one row of the file, diffed. Money that is not cost, the retail anchor before
// and after, is NOT gated: a counter clerk quotes list prices all day, and hiding the number they
// are allowed to say out loud would be theatre.
type PriceFileLine struct {
	Row     int    `json:"row"` // 1-based row in the source file, so a fault names a place to look
	SKU     string `json:"sku"`
	Outcome string `json:"outcome"`
	// ProductID / ProductName are empty on an UNKNOWN row. The screen falls back to the SKU as the
	// row's name rather than printing an empty cell.
	ProductID   string `json:"product_id,omitempty"`
	ProductName string `json:"product_name,omitempty"`

	CurrentRetailCents *int64 `json:"current_retail_cents,omitempty"`
	NewRetailCents     *int64 `json:"new_retail_cents,omitempty"`
	VarianceBps        *int64 `json:"variance_bps,omitempty"`
	IsOutlier          bool   `json:"is_outlier,omitempty"`

	// DerivedFrom is the anchor the retail came from (batch_model.go's four constants). It is the
	// one column that makes the trade_col trap visible: mapping a trade column flips every line from
	// COST_MARKUP to TRADE_PLUS_FOB, and this is where a manager sees that happen, in a preview,
	// before anything is written, which is the whole reason the preview writes nothing.
	DerivedFrom      string `json:"derived_from,omitempty"`
	FreightEstimated bool   `json:"freight_estimated,omitempty"`

	// Costs is nil, key and all, for a caller the dealer's cost visibility does not admit.
	Costs *PriceFileCosts `json:"costs,omitempty"`
}

// PriceFileCounts is the diff over EVERY row, not over the page that was displayed.
type PriceFileCounts struct {
	Rows      int `json:"rows"`
	New       int `json:"new"`
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
	Unknown   int `json:"unknown"`
	Outliers  int `json:"outliers"`
	// Unpriced is NOT a fifth outcome, it is the diagnostic that stops the four from lying.
	//
	// A row whose SKU matches a product but whose mapped columns yield no cost, no trade and no
	// manufacturer retail cannot be derived, so applying it would change nothing, so it is honestly
	// UNCHANGED. That is exactly the reading a mis-mapped cost column produces: point cost_col at a
	// header the file does not have and every row becomes underivable, and a preview reporting "412
	// unchanged" says the file agrees with us when it means we could not read the file. This count
	// is how the screen tells those two apart, and it is why apply refuses a file with nothing to
	// stage rather than writing an empty batch.
	Unpriced int `json:"unpriced"`
}

// Movers is the count of rows an apply would actually stage.
func (c PriceFileCounts) Movers() int { return c.New + c.Changed }

// PriceFilePreview is the whole answer to "what would this file do". It writes nothing.
type PriceFilePreview struct {
	VendorID   string `json:"vendor_id"`
	VendorName string `json:"vendor_name,omitempty"` // INTERNAL-only (rule 1)
	// ProfileID is empty when this vendor has no profile yet. The screen shows that as "map this
	// one", and the apply creates it.
	ProfileID string `json:"profile_id,omitempty"`
	Format    string `json:"format"`
	// Headers is the file's header row verbatim, which is what the mapping step maps FROM.
	Headers []string `json:"headers"`
	// Mapping is what this preview was computed under: the caller's choice where they made one, the
	// stored profile otherwise, and a header-guess where there is neither.
	Mapping map[string]string `json:"mapping"`
	// MappingSource says which of those three it was, so a screen never presents a guess as a saved
	// answer. STORED | REQUESTED | GUESSED.
	MappingSource string `json:"mapping_source"`

	Counts PriceFileCounts `json:"counts"`
	Lines  []PriceFileLine `json:"lines"`
	// Truncated is true when Counts.Rows exceeds the returned Lines. The counts are still the whole
	// file; only the listing is capped.
	Truncated bool `json:"truncated"`

	// CatalogChecked is false when the catalogue seam could not answer. Every row then reads UNKNOWN,
	// which without this flag would be a screen confidently reporting that we stock none of it. The
	// dispatch posture (service_loadboard.go: a gate that cannot answer must allow, with an honesty
	// flag so the surface can say the check did not run), applied here to a check that cannot allow,
	// so the flag is what stops the refusal from reading as a finding.
	CatalogChecked bool `json:"catalog_checked"`

	// Digest is a SHA-256 over the parsed rows and the mapping. Apply requires the caller to echo it
	// and refuses on a mismatch, which is what binds "the thing you approved" to "the thing that ran".
	Digest string `json:"digest"`

	// CostsVisible reports whether THIS caller was admitted to cost. It is a boolean about the
	// caller, not a cost, and the screen needs it to lay out its columns without inferring policy
	// from the absence of data, an inference that would render "no cost in the file" and "not
	// allowed to see cost" identically.
	CostsVisible bool `json:"costs_visible"`
}

// PriceFileApplyResult is what the commit produced.
type PriceFileApplyResult struct {
	RunID  string          `json:"run_id"`
	Batch  PriceBatch      `json:"batch"`
	Counts PriceFileCounts `json:"counts"`
	// Staged is the number of change lines written: the movers plus the unknown rows, which are
	// recorded so the unmatched-SKU report survives the session.
	Staged int `json:"staged"`
	// ProfileCreated is true when this import was the first for its vendor and the mapping became a
	// profile. The screen says so once, in the confirmation, and never again.
	ProfileCreated bool `json:"profile_created"`
}

// PriceImportRun is one append-only price_import_runs row.
type PriceImportRun struct {
	ID             string            `json:"id"`
	VendorID       string            `json:"vendor_id,omitempty"`
	VendorName     string            `json:"vendor_name,omitempty"` // INTERNAL-only (rule 1)
	BatchID        string            `json:"batch_id,omitempty"`
	BatchNo        string            `json:"batch_no,omitempty"`
	SourceName     string            `json:"source_name"`
	SourceFormat   string            `json:"source_format"`
	SourceBytes    int64             `json:"source_bytes"`
	ContentDigest  string            `json:"content_digest"`
	MappingUsed    map[string]string `json:"mapping_used"`
	Counts         PriceFileCounts   `json:"counts"`
	ImportedBy     string            `json:"imported_by,omitempty"`
	ImportedByName string            `json:"imported_by_name,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

// - the seams -

// CostVisibility answers "may THIS caller receive cost bytes", and nothing else.
//
// WHY A SEAM AND NOT AN IMPORT. The question splits cleanly in two and the halves have different
// owners. WHAT a manager may see is pricing's policy and lives in pricing's own row
// (pricing_anchor_policy.import_cost_visibility, migration 0198). WHO is a manager is identity's,
// and pricing asking identity directly would make every unit test on this path need a JWT to
// describe a clerk. Same shape and same argument as OverrideAuthority (override_authority.go).
//
// SEAM CONTRACT, and it is the same fail-closed contract as the override authority above it:
//   - userID is the caller the auth guard resolved. An implementation MUST NOT take it from a body,
//     a header or a query, and MUST NOT treat an empty userID as a manager.
//   - An error means "could not determine". The caller refuses. It never means yes.
type CostVisibility interface {
	IsManager(ctx context.Context, userID string) (bool, error)
}

// WithCostVisibility wires the caller-role seam. NIL MEANS NOBODY SEES COST, the same posture as an
// unwired override authority, and for the same reason: an unwired gate that defaults open turns a
// deployment mistake into a data leak, while one that defaults closed turns it into a support call.
func (s *Service) WithCostVisibility(v CostVisibility) *Service { s.costvis = v; return s }

// VendorDirectory is the manufacturer list this surface imports for.
//
// `vendors` is not pricing's table. 0039's own repository reaches into it with a JOIN and that is
// pre-existing; new code does not get to copy it. The consumer declares what it needs, an id, a
// number and a name, for a list of at most a few dozen, and cmd/server adapts whoever owns the
// table to it.
type VendorDirectory interface {
	ListVendors(ctx context.Context) ([]PriceFileVendor, error)
}

// WithVendorDirectory wires the manufacturer list. Nil degrades to an empty list, which the screen
// renders as an empty state rather than a fault: a vendor list that cannot be read is a screen with
// nothing to pick, not a screen that is broken.
func (s *Service) WithVendorDirectory(d VendorDirectory) *Service { s.vendors = d; return s }

// CatalogSKU is what the diff needs to know about a product: enough to name it on a row and to find
// its retail anchor. Nothing else, a widened brief here would be a catalogue read pretending to be
// a pricing one.
type CatalogSKU struct {
	ProductID string
	SKU       string
	Name      string
}

// CatalogSKUs resolves a batch of SKUs in ONE call.
//
// BATCHED ON PURPOSE. A-08 looks a product up per row and reads its list price per row, so a
// four-thousand-line file is eight thousand round trips inside one request. The seam is declared
// plural so that shape cannot be inherited: the contract is one call per import, and a key absent
// from the returned map is a SKU the catalogue does not have.
type CatalogSKUs interface {
	LookupSKUs(ctx context.Context, skus []string) (map[string]CatalogSKU, error)
}

// WithCatalogSKUs wires the SKU lookup. Nil, or an error from it, sets CatalogChecked=false on the
// preview and every row reads UNKNOWN, reported as a check that did not run, never as a catalogue
// that came back empty. Apply refuses in that state: staging four thousand unmatched rows because
// the catalogue was briefly unreachable is a write nobody asked for.
func (s *Service) WithCatalogSKUs(c CatalogSKUs) *Service { s.catskus = c; return s }

// priceFileStage is everything ONE apply writes, handed to the store as a single unit.
//
// IT IS ONE METHOD BECAUSE IT IS ONE TRANSACTION. The obvious decomposition, create the profile,
// then the batch, then the lines, then the audit row, is four store calls and therefore four places
// to stop half-done, and the half-done states are all bad in the same way: a batch with no audit row
// is a price change nobody imported, and an audit row with no batch is an import that claims to have
// staged something that is not there. The A-08 apply-to-anchor made the same call for the same reason
// (batchStore.ApplyBatch: "atomically upserts … and appends … then flips the batch").
type priceFileStage struct {
	VendorID string
	// ProfileID empty means this vendor has never been mapped and the store creates the profile
	// inside the same transaction, stamped FIRST_IMPORT. That is the lazy creation migration 0198
	// argues for, and it lives here rather than in a separate call so a failed batch write cannot
	// leave behind a profile for an import that never happened.
	ProfileID   string
	ProfileName string
	Mapping     map[string]string

	BatchNo       string
	EffectiveDate *time.Time
	SourceRef     string
	Lines         []PriceChangeLine
	OutlierCount  int

	Run    PriceImportRun
	UserID string
	Now    time.Time
}

// priceFileStaged is what the transaction produced.
type priceFileStaged struct {
	BatchID        string
	BatchNo        string
	ProfileID      string
	ProfileCreated bool
	RunID          string
	Staged         int
}

// priceFileStore is the persistence seam for THIS surface, over pricing's OWN tables. It is separate
// from batchStore so the A-08 fakes need no new methods; the pgx *Repository implements both and
// auto-wires in NewService by type assertion, exactly as batchStore does.
type priceFileStore interface {
	// ImportCostVisibility reads pricing_anchor_policy.import_cost_visibility (branch-over-NULL).
	// A missing row returns MANAGER_UP, the strictest value that still leaves the screen usable by
	// the person whose screen it is.
	ImportCostVisibility(ctx context.Context, branchID string) (string, error)
	// ActiveProfileForVendor is the lazy resolve: the oldest active profile for this vendor with its
	// origin, or errNoProfile when the vendor has never been mapped.
	ActiveProfileForVendor(ctx context.Context, vendorID string) (ImportProfile, string, error)
	// ListPricesForProducts reads pricing's own retail anchor for a batch of products, in ONE call
	// for the same reason CatalogSKUs is plural. A product with no anchor is absent from the map,
	// which is what makes it NEW rather than CHANGED-from-zero.
	ListPricesForProducts(ctx context.Context, productIDs []string) (map[string]int64, error)
	// StagePriceFile is the whole write. See priceFileStage.
	StagePriceFile(ctx context.Context, in priceFileStage) (priceFileStaged, error)
	ListImportRuns(ctx context.Context, vendorID string, limit int) ([]PriceImportRun, error)
	// ProfileVendorRows carries the per-vendor profile facts the picker needs, keyed by vendor id, so
	// the vendor directory adapter stays a name lookup and never learns about mappings.
	ProfileVendorRows(ctx context.Context) (map[string]PriceFileVendor, error)
}
