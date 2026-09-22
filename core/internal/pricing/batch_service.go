package pricing

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// A-08 service errors (mapped to HTTP in batch_handler.go / writeErr).
var (
	errBatchStoreUnset = errors.New("pricing: batch store not configured")
	errNoProfile       = errors.New("pricing: import profile not found")
	errBadBatchState   = errors.New("pricing: invalid batch state for this action")
	errSeparation      = errors.New("pricing: separation of duties, the approver must differ from the batch creator")
	errNotEffective    = errors.New("pricing: batch effective date is in the future")
	errNoLines         = errors.New("pricing: batch has no importable lines")
)

// previewLimit is the fixed 5-row import preview the mapping editor shows (plan §A-08).
const previewLimit = 5

// - retail derivation (pure) -
//
// deriveRetail is the anchor-precedence engine. It is a PURE function of the policy + the
// row's source columns, so the "derivation anchor rules" AC is unit-tested with no DB. The
// precedence is exactly the plan's: GTA trade + estimated FOB > cost × markup > mfr retail.
func deriveRetail(policy AnchorPolicy, in derivationInput) derivationResult {
	switch {
	case in.tradeCents != nil:
		freight := policy.fobMidCents()
		return derivationResult{
			retailCents:      *in.tradeCents + freight,
			source:           DeriveTradePlusFOB,
			freightCents:     freight,
			freightEstimated: true,
			ok:               true,
		}
	case in.costCents != nil:
		markup := policy.MarkupBps
		if markup <= 0 {
			markup = 18500 // ×1.85 fallback (policy row is authoritative; this guards a 0 row)
		}
		return derivationResult{
			retailCents: roundDivInt64(*in.costCents*markup, 10000),
			source:      DeriveCostMarkup,
			ok:          true,
		}
	case in.mfrRetailCents != nil:
		return derivationResult{retailCents: *in.mfrRetailCents, source: DeriveMfrRetail, ok: true}
	default:
		return derivationResult{source: DeriveNone, ok: false}
	}
}

// roundDivInt64 divides with round-half-up on the magnitude (money never truncates down silently).
func roundDivInt64(n, d int64) int64 {
	if d == 0 {
		return 0
	}
	if n < 0 {
		return -roundDivInt64(-n, d)
	}
	return (n + d/2) / d
}

// varianceBps is the signed move of after vs before, in basis points. nil when there is no
// prior anchor (a brand-new list price cannot be an "outlier" against nothing).
func varianceBps(before *int64, after int64) *int64 {
	if before == nil || *before <= 0 {
		return nil
	}
	v := roundDivInt64((after-*before)*10000, *before)
	return &v
}

// - CSV parsing + money -

// parseMoneyCents reads a manufacturer price-list money cell (dollars, possibly with $, commas
// and a decimal) into integer cents. Empty → (nil, nil) so an absent column is "unset", not 0.
func parseMoneyCents(raw string) (*int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	s = strings.NewReplacer("$", "", ",", "", " ", "").Replace(s)
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") { // accounting negative
		neg, s = true, s[1:len(s)-1]
	}
	if strings.HasPrefix(s, "-") {
		neg, s = true, s[1:]
	}
	dot := strings.IndexByte(s, '.')
	var dollars, cents string
	if dot < 0 {
		dollars, cents = s, "00"
	} else {
		dollars, cents = s[:dot], s[dot+1:]
	}
	if dollars == "" {
		dollars = "0"
	}
	if len(cents) < 2 {
		cents = (cents + "00")[:2]
	} else if len(cents) > 2 {
		cents = cents[:2] // truncate sub-cent precision
	}
	var d, c int64
	if _, err := fmt.Sscanf(dollars, "%d", &d); err != nil {
		return nil, fmt.Errorf("bad money value %q", raw)
	}
	if _, err := fmt.Sscanf(cents, "%d", &c); err != nil {
		return nil, fmt.Errorf("bad money value %q", raw)
	}
	v := d*100 + c
	if neg {
		v = -v
	}
	return &v, nil
}

// csvRow is one parsed source row keyed by (lower-cased) header name.
type csvRow map[string]string

func parseCSV(r io.Reader) (headers []string, rows []csvRow, err error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1 // tolerate ragged manufacturer exports
	raw, err := cr.ReadAll()
	if err != nil {
		return nil, nil, badReq("could not parse CSV: %v", err)
	}
	if len(raw) == 0 {
		return nil, nil, badReq("CSV is empty")
	}
	for _, h := range raw[0] {
		headers = append(headers, strings.ToLower(strings.TrimSpace(h)))
	}
	for _, rec := range raw[1:] {
		blank := true
		row := csvRow{}
		for i, h := range headers {
			if i < len(rec) {
				val := strings.TrimSpace(rec[i])
				row[h] = val
				if val != "" {
					blank = false
				}
			}
		}
		if !blank {
			rows = append(rows, row)
		}
	}
	return headers, rows, nil
}

// - lifecycle -

// CreateBatch opens a DRAFT batch for a vendor/profile with a scheduled effective date.
func (s *Service) CreateBatch(ctx context.Context, b PriceBatch, userID string) (PriceBatch, error) {
	if s.batch == nil {
		return PriceBatch{}, errBatchStoreUnset
	}
	if strings.TrimSpace(b.BatchNo) == "" {
		return PriceBatch{}, badReq("batch_no is required")
	}
	b.Status = BatchDraft
	b.CreatedBy = userID
	b.LineCount, b.OutlierCount = 0, 0
	return s.batch.CreateBatch(ctx, b)
}

// ImportBatch parses a manufacturer CSV over the batch's import profile, derives the retail
// anchor per row (precedence engine), stamps the outlier flag against the anchor policy, and
// REPLACES the batch's staged lines (so a re-import of the same file is idempotent, same
// lines, no duplication). The batch moves DRAFT/STAGED → STAGED. Unmatched SKUs are recorded
// (product_id NULL) and reported, never dropped.
func (s *Service) ImportBatch(ctx context.Context, batchID string, csvReader io.Reader, sourceRef string) (ImportResult, error) {
	if s.batch == nil {
		return ImportResult{}, errBatchStoreUnset
	}
	b, err := s.batch.GetBatch(ctx, batchID)
	if err != nil {
		return ImportResult{}, err
	}
	if b.Status != BatchDraft && b.Status != BatchStaged {
		return ImportResult{}, errBadBatchState
	}
	if b.ProfileID == "" {
		return ImportResult{}, errNoProfile
	}
	profile, err := s.batch.GetImportProfile(ctx, b.ProfileID)
	if err != nil {
		return ImportResult{}, err
	}
	policy, err := s.batch.AnchorPolicy(ctx, "") // unified cross-yard → org-default policy
	if err != nil {
		return ImportResult{}, err
	}
	if profile.MarkupBps > 0 { // a profile override wins over the org markup for its vendor
		policy.MarkupBps = profile.MarkupBps
	}

	_, rows, err := parseCSV(csvReader)
	if err != nil {
		return ImportResult{}, err
	}
	if len(rows) == 0 {
		return ImportResult{}, errNoLines
	}

	skuCol := strings.ToLower(profile.col("sku_col", "sku"))
	costCol := strings.ToLower(profile.col("cost_col", "cost"))
	retailCol := strings.ToLower(profile.col("retail_col", "retail"))
	tradeCol := strings.ToLower(profile.col("trade_col", "trade"))

	lines := make([]PriceChangeLine, 0, len(rows))
	for _, row := range rows {
		sku := strings.TrimSpace(row[skuCol])
		if sku == "" {
			continue // a row with no SKU is unusable; skip (blank rows already dropped)
		}
		cost, err := parseMoneyCents(row[costCol])
		if err != nil {
			return ImportResult{}, badReq("row %q: %v", sku, err)
		}
		trade, err := parseMoneyCents(row[tradeCol])
		if err != nil {
			return ImportResult{}, badReq("row %q: %v", sku, err)
		}
		mfr, err := parseMoneyCents(row[retailCol])
		if err != nil {
			return ImportResult{}, badReq("row %q: %v", sku, err)
		}

		dr := deriveRetail(policy, derivationInput{costCents: cost, tradeCents: trade, mfrRetailCents: mfr})
		line := PriceChangeLine{
			BatchID:          batchID,
			SKU:              sku,
			AfterCostCents:   cost,
			DerivedFrom:      dr.source,
			FreightEstimated: dr.freightEstimated,
		}
		if dr.ok {
			retail := dr.retailCents
			line.AfterRetailCents = &retail
		}

		ref, lookupErr := s.batch.ProductBySKU(ctx, sku)
		if lookupErr != nil {
			if errors.Is(lookupErr, errNotFound) {
				line.Unmatched = true
				lines = append(lines, line)
				continue
			}
			return ImportResult{}, lookupErr
		}
		line.ProductID = ref.ID

		before, err := s.batch.CurrentListPrice(ctx, ref.ID)
		if err != nil {
			return ImportResult{}, err
		}
		line.BeforeRetailCents = before
		if line.AfterRetailCents != nil {
			line.VarianceBps = varianceBps(before, *line.AfterRetailCents)
			if line.VarianceBps != nil && absInt64(*line.VarianceBps) > policy.VarianceAlertBps {
				line.IsOutlier = true
			}
		}
		lines = append(lines, line)
	}

	if err := s.batch.ReplaceBatchLines(ctx, batchID, lines); err != nil {
		return ImportResult{}, err
	}

	// Recompute counts + advance to STAGED.
	b.Status = BatchStaged
	if sourceRef != "" {
		b.SourceRef = sourceRef
	}
	matched, outliers := 0, 0
	for _, l := range lines {
		if !l.Unmatched {
			matched++
		}
		if l.IsOutlier {
			outliers++
		}
	}
	b.LineCount = len(lines)
	b.OutlierCount = outliers
	updated, err := s.batch.UpdateBatch(ctx, b)
	if err != nil {
		return ImportResult{}, err
	}

	return buildImportResult(updated, lines, matched), nil
}

func buildImportResult(b PriceBatch, lines []PriceChangeLine, matched int) ImportResult {
	res := ImportResult{Batch: b, TotalLines: len(lines), MatchedLines: matched}
	for _, l := range lines {
		switch {
		case l.Unmatched:
			res.Unmatched = append(res.Unmatched, l)
		default:
			if len(res.Preview) < previewLimit {
				res.Preview = append(res.Preview, l)
			}
		}
		if l.FreightEstimated {
			res.Estimated = append(res.Estimated, l)
		}
		if l.IsOutlier {
			res.Outliers = append(res.Outliers, l)
		}
	}
	return res
}

// ApproveBatch is the maker-checker gate: the approver MUST differ from the batch creator
// (separation of duties). A STAGED batch → APPROVED. Idempotent-safe: an already-APPROVED
// batch is left as-is (no error) provided the same separation invariant already held.
func (s *Service) ApproveBatch(ctx context.Context, batchID, approverID string) (PriceBatch, error) {
	if s.batch == nil {
		return PriceBatch{}, errBatchStoreUnset
	}
	if strings.TrimSpace(approverID) == "" {
		return PriceBatch{}, badReq("approver id is required")
	}
	b, err := s.batch.GetBatch(ctx, batchID)
	if err != nil {
		return PriceBatch{}, err
	}
	if b.Status == BatchApproved {
		return b, nil // idempotent
	}
	if b.Status != BatchStaged {
		return PriceBatch{}, errBadBatchState
	}
	if b.CreatedBy != "" && b.CreatedBy == approverID {
		return PriceBatch{}, errSeparation
	}
	now := s.now().UTC()
	b.Status = BatchApproved
	b.ApprovedBy = approverID
	b.ApprovedAt = &now
	return s.batch.UpdateBatch(ctx, b)
}

// ApplyBatch writes an APPROVED, effective batch to the retail anchor (unified cross-yard -
// one list price per product) and appends the append-only change-log rows, atomically via
// the store. Guards: status must be APPROVED (an already-APPLIED batch is an idempotent
// no-op); the effective_date must be on/before asOf. Unmatched lines are skipped in the store.
func (s *Service) ApplyBatch(ctx context.Context, batchID, userID string, asOf time.Time) (PriceBatch, int, error) {
	if s.batch == nil {
		return PriceBatch{}, 0, errBatchStoreUnset
	}
	b, err := s.batch.GetBatch(ctx, batchID)
	if err != nil {
		return PriceBatch{}, 0, err
	}
	if b.Status == BatchApplied {
		return b, 0, nil // idempotent, already applied, no duplicate writes
	}
	if b.Status != BatchApproved {
		return PriceBatch{}, 0, errBadBatchState
	}
	if b.EffectiveDate != nil && b.EffectiveDate.After(dayStart(asOf)) {
		return PriceBatch{}, 0, errNotEffective
	}
	applied, err := s.batch.ApplyBatch(ctx, batchID, userID)
	if err != nil {
		return PriceBatch{}, 0, err
	}
	out, err := s.batch.GetBatch(ctx, batchID)
	if err != nil {
		return PriceBatch{}, applied, err
	}
	return out, applied, nil
}

// ApplyDueBatches applies every APPROVED batch whose effective_date has arrived (≤ asOf). No
// scheduler infra (plan): this is the endpoint/lazy sweep the caller triggers. Returns the
// count of batches applied. Errors on individual batches are collected, not fatal to the rest.
func (s *Service) ApplyDueBatches(ctx context.Context, userID string, asOf time.Time) (int, error) {
	if s.batch == nil {
		return 0, errBatchStoreUnset
	}
	due, err := s.batch.DueBatches(ctx, dayStart(asOf))
	if err != nil {
		return 0, err
	}
	var firstErr error
	applied := 0
	for _, b := range due {
		if _, _, err := s.ApplyBatch(ctx, b.ID, userID, asOf); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		applied++
	}
	return applied, firstErr
}

// ListImportProfiles returns the seeded V-* manufacturer mapping profiles (internal-only).
func (s *Service) ListImportProfiles(ctx context.Context) ([]ImportProfile, error) {
	if s.batch == nil {
		return nil, errBatchStoreUnset
	}
	return s.batch.ListImportProfiles(ctx)
}

// UpdateImportProfileMapping saves the CSV column mapping edited in the mapping editor.
func (s *Service) UpdateImportProfileMapping(ctx context.Context, id string, mapping map[string]string) (ImportProfile, error) {
	if s.batch == nil {
		return ImportProfile{}, errBatchStoreUnset
	}
	if mapping == nil {
		return ImportProfile{}, badReq("mapping is required")
	}
	return s.batch.UpdateImportProfileMapping(ctx, id, mapping)
}

// GetBatch / ListBatches / ListBatchLines / ChangeLog are the read surface.
func (s *Service) GetBatch(ctx context.Context, id string) (PriceBatch, error) {
	if s.batch == nil {
		return PriceBatch{}, errBatchStoreUnset
	}
	return s.batch.GetBatch(ctx, id)
}

func (s *Service) ListBatches(ctx context.Context, f BatchFilter) ([]PriceBatch, error) {
	if s.batch == nil {
		return nil, errBatchStoreUnset
	}
	return s.batch.ListBatches(ctx, f)
}

func (s *Service) ListBatchLines(ctx context.Context, batchID string) ([]PriceChangeLine, error) {
	if s.batch == nil {
		return nil, errBatchStoreUnset
	}
	return s.batch.ListBatchLines(ctx, batchID)
}

func (s *Service) ChangeLog(ctx context.Context, f ChangeLogFilter) ([]ChangeLogRow, error) {
	if s.batch == nil {
		return nil, errBatchStoreUnset
	}
	rows, err := s.batch.ListChangeLog(ctx, f)
	if err != nil {
		return nil, err
	}
	// newest-first for the log viewer
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// dayStart truncates to the date (00:00 UTC) so an effective_date DATE compares cleanly.
func dayStart(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}
