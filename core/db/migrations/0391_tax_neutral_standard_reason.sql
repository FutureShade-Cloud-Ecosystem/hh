-- 0391_tax_neutral_standard_reason.sql
-- Owner module: internal/tax (item 3b slice 1, the tax engine binds by the dealer's own jurisdiction).
-- Forward only; nothing is rewritten.
--
-- 1. The snapshot's reason universe gains one code: STD_SALES_TAX, the neutral standard rate of a
--    jurisdiction other than Ontario. The Ontario code STD_ON_HST_13 stays lawful and an Ontario
--    dealer keeps it. The 0029 CHECK is the authority for the universe, so it is widened here (never
--    edited there) and the engine's LegalReasonCodes mirrors it.
-- 2. The snapshot stores the STANDARD rate its full_hst_cents column was computed at. The recompute
--    reconstructs the taxable base as full_hst / standard rate; that division must use the rate the
--    row was written under, never the dealer's current one, or a rate change would re-price history.
--    Every row written before this migration is an Ontario row at 13 percent, which is the default.
ALTER TABLE tax_decision_snapshot
  DROP CONSTRAINT tax_decision_snapshot_tax_reason_code_check;
ALTER TABLE tax_decision_snapshot
  ADD CONSTRAINT tax_decision_snapshot_tax_reason_code_check CHECK (tax_reason_code IN (
    'STD_ON_HST_13','STD_SALES_TAX','ON_FN_POS_8','FED_S87_GOODS_ON_RES','FED_S87_VENDOR_DELIVERY',
    'FED_S87_SERVICE_RESERVE','FED_BAND_MANAGEMENT','FNGST_5','FNT_PRODUCT',
    'TREATY_OR_SELF_GOV_REVIEW','MANUAL_TAX_REVIEW','RELIEF_DECLINED_EVIDENCE'));
ALTER TABLE tax_decision_snapshot
  ADD COLUMN standard_rate_bps INTEGER NOT NULL DEFAULT 1300
    CHECK (standard_rate_bps BETWEEN 0 AND 10000);
