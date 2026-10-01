// Ported from hardscapeos_dibbits master 87d7941b app/src/tax-api.ts; adapted for HH.

// W6 M5 Tax Engine client helpers. Thin typed wrappers over the staff API (Bearer JWT via api()). All money is
// integer cents; fmtCents is the single money renderer. Card identifiers are MASKED-only: the raw number never
// exists on this surface (the backend never serializes it).
import { api, BRANCHES, branchName } from './ports.js';

export { BRANCHES, branchName };

export function fmtCents(cents: number): string {
  const sign = cents < 0 ? '-' : '';
  const abs = Math.abs(cents);
  return `${sign}$${(abs / 100).toLocaleString('en-CA', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

export function fmtBps(bps: number): string {
  return `${(bps / 100).toLocaleString('en-CA', { maximumFractionDigits: 2 })}%`;
}

export interface Rate {
  id: string;
  jurisdiction: string;
  reason_code: string;
  rate_bps: number;
  effective_from: string;
  effective_to?: string;
  notes?: string;
}

/**
 * TAX_REASON_LABEL is WHY a rate applies: the reason column of the effective-dated ON-HST table.
 *
 * `tax_rates.reason_code` carries no CHECK of its own; migration 0042:12 says it mirrors
 * `tax_decision_snapshot.tax_reason_code`, whose CHECK (migration 0029:70-73) is the actual vocabulary and
 * is transcribed here in full: all eleven, not just the five 0042 seeds. These are the audit trail's
 * reasons for charging what was charged, and stored as codes they are unreadable to the person who has to
 * decide whether a row is right: FED_S87_VENDOR_DELIVERY is s.87 relief on goods the vendor delivers to a
 * reserve, and a screen that prints the token is asking an administrator to parse a key.
 *
 * The words stay faithful to the codes and drop nothing that the ROW does not already say. "Ontario" comes
 * off ON_FN_POS_8 because the jurisdiction column sits immediately to its left; the "13" comes off
 * STD_ON_HST_13 because the rate column sits immediately to its right. "Point-of-sale relief" is the
 * product's own phrase for that path in client-facing copy already (pricing-doc.ts:218) and is reused
 * rather than reinvented.
 *
 * The default arm matters MORE here than anywhere else in this module: the admin's Add-rate form takes the
 * reason as FREE TEXT (there is no CHECK to stop it), so an operator can and will store a code this map has
 * never heard of. It must come back looking like the typed code: visibly odd, obviously theirs: and never
 * be laundered into 'Unknown', which would hide a typo that silently mis-rates a sale.
 */
export const TAX_REASON_LABEL: Record<string, string> = {
  STD_ON_HST_13: 'Standard HST',
  ON_FN_POS_8: 'Point-of-sale relief',
  FED_S87_GOODS_ON_RES: 'Goods on reserve',
  FED_S87_VENDOR_DELIVERY: 'Delivery to reserve',
  FED_S87_SERVICE_RESERVE: 'Service on reserve',
  FED_BAND_MANAGEMENT: 'Band management',
  FNGST_5: 'First Nations GST',
  FNT_PRODUCT: 'First Nations tax',
  TREATY_OR_SELF_GOV_REVIEW: 'Treaty review',
  MANUAL_TAX_REVIEW: 'Manual review',
  RELIEF_DECLINED_EVIDENCE: 'Relief declined',
};

/** taxReasonWord renders why a rate applies; an unmapped or operator-typed code comes back as itself. */
export function taxReasonWord(code: string): string {
  return TAX_REASON_LABEL[code] ?? code.toLowerCase().replace(/_/g, ' ');
}

/**
 * CARD_PROFILE_STATUS_LABEL is whether a status-card exemption profile is in force.
 *
 * `tax_status_card_profiles.status` is ACTIVE | PENDING | RETIRED (migration 0042:41), and PENDING is not
 * decoration: 0042 notes the third profile stays PENDING at 13% until it is ratified, so the pill is the
 * one visible difference between a profile that grants relief and one that does not yet. That knowledge was
 * branched inside the component (tax-admin.ts statusPill hardcoded the string 'PENDING' and echoed the raw
 * enum for the rest); it belongs in a map, where a fourth state cannot fall through to the ACTIVE arm.
 */
export const CARD_PROFILE_STATUS_LABEL: Record<string, string> = {
  ACTIVE: 'Active',
  PENDING: 'Pending',
  RETIRED: 'Retired',
};

/** cardProfileStatusWord renders a profile's standing; an unmapped standing degrades honestly, as itself. */
export function cardProfileStatusWord(status: string): string {
  return CARD_PROFILE_STATUS_LABEL[status] ?? status.toLowerCase().replace(/_/g, ' ');
}

export interface StatusCardProfile {
  id: string;
  code: string;
  name: string;
  rate_bps: number;
  requires_photo: boolean;
  requires_number: boolean;
  requires_attestation: boolean;
  status: string;
  notes?: string;
}

export interface Evidence {
  id: string;
  profile_id?: string;
  card_profile_id?: string;
  order_id?: string;
  photo_ref?: string;
  number_mask: string;
  attestation: boolean;
  tcrd_expiry?: string;
  captured_by?: string;
  created_at?: string;
  attestation_status?: string;
}

export interface ReliefEligibility {
  eligible: boolean;
  block_reason?: string;
  number_mask?: string;
}

export interface TaxConfig {
  branch_id?: string;
  hst_registration_no: string;
  round_mode: string;
  avalara_enabled: boolean;
  postal_routing_enabled: boolean;
  notes?: string;
}

export interface RecomputeResult {
  from_date: string;
  to_date: string;
  scanned: number;
  inserted: number;
  skipped_exists: number;
}

export interface ReconReport {
  year: number;
  month: number;
  branch_id?: string;
  collected_cents: number;
  gl_payable_cents: number;
  gl_federal_cents: number;
  gl_ontario_cents: number;
  variance_cents: number;
  tied_out: boolean;
}

async function unwrap<T>(res: Response): Promise<T> {
  if (!res.ok) {
    let msg = `${res.status}`;
    try {
      const b = (await res.json()) as { error?: string };
      if (b.error) msg = b.error;
    } catch {
 /* keep status */
    }
    throw new Error(msg);
  }
  return (await res.json()) as T;
}

export async function listRates(): Promise<Rate[]> {
  return (await unwrap<{ rates: Rate[] }>(await api('/api/v1/tax/rates'))).rates ?? [];
}

export async function createRate(body: Partial<Rate>): Promise<Rate> {
  return unwrap<Rate>(await api('/api/v1/tax/rates', { method: 'POST', body: JSON.stringify(body) }));
}

export async function listProfiles(): Promise<StatusCardProfile[]> {
  return (await unwrap<{ profiles: StatusCardProfile[] }>(await api('/api/v1/tax/profiles'))).profiles ?? [];
}

export async function upsertProfile(body: Partial<StatusCardProfile>): Promise<StatusCardProfile> {
  return unwrap<StatusCardProfile>(await api('/api/v1/tax/profiles', { method: 'POST', body: JSON.stringify(body) }));
}

export async function reviewQueue(): Promise<StatusCardProfile[]> {
  return (await unwrap<{ pending_profiles: StatusCardProfile[] }>(await api('/api/v1/tax/review-queue'))).pending_profiles ?? [];
}

export async function listEvidence(params: { order_id?: string; profile_id?: string } = {}): Promise<Evidence[]> {
  const qs = new URLSearchParams();
  if (params.order_id) qs.set('order_id', params.order_id);
  if (params.profile_id) qs.set('profile_id', params.profile_id);
  const q = qs.toString();
  return (await unwrap<{ evidence: Evidence[] }>(await api(`/api/v1/tax/evidence${q ? `?${q}` : ''}`))).evidence ?? [];
}

export async function viewEvidence(id: string): Promise<Evidence> {
  return unwrap<Evidence>(await api(`/api/v1/tax/evidence/${id}`));
}

export async function downloadEvidence(id: string): Promise<Evidence> {
  return unwrap<Evidence>(await api(`/api/v1/tax/evidence/${id}/download`, { method: 'POST' }));
}

export async function validateEvidence(id: string, asOf?: string): Promise<ReliefEligibility> {
  const q = asOf ? `?as_of=${asOf}` : '';
  return unwrap<ReliefEligibility>(await api(`/api/v1/tax/evidence/${id}/validate${q}`, { method: 'POST' }));
}

export async function getConfig(branchId = ''): Promise<TaxConfig> {
  const q = branchId ? `?branch_id=${branchId}` : '';
  return unwrap<TaxConfig>(await api(`/api/v1/tax/config${q}`));
}

export async function updateConfig(body: Partial<TaxConfig>): Promise<TaxConfig> {
  return unwrap<TaxConfig>(await api('/api/v1/tax/config', { method: 'POST', body: JSON.stringify(body) }));
}

export async function recompute(fromDate: string, toDate: string): Promise<RecomputeResult> {
  return unwrap<RecomputeResult>(
    await api('/api/v1/tax/recompute', { method: 'POST', body: JSON.stringify({ from_date: fromDate, to_date: toDate }) }),
  );
}

export async function reconcile(year: number, month: number, branchId = ''): Promise<ReconReport> {
  const qs = new URLSearchParams({ year: String(year) });
  if (month) qs.set('month', String(month));
  if (branchId) qs.set('branch_id', branchId);
  return unwrap<ReconReport>(await api(`/api/v1/tax/recon?${qs.toString()}`));
}
