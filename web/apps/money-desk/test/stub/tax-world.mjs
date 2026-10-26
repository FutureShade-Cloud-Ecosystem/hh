// The tax administration slice's stub world: the rate table, the exemption profiles and the review queue, the
// engine config, the evidence records, the recompute and the reconciliation, answered the way the core answers
// them. This is a test double only; the real routes are the core's (core/internal/tax). Its guards, its checks,
// its order and its sentences are held to the contract and to the core's own source by
// test/tax-stub-contract.test.mjs, so it cannot drift in silence.
//
// What it copies from the core, on purpose, quirks included:
// - the lists are keyed by name (rates, profiles, pending_profiles, evidence), as the contract declares; an
//   empty rates, profiles or evidence list is null, not [] (a Go nil slice), while the queue is never null;
// - reads are a manager's (tier 1 or 2), rate, profile, config, recompute writes are an administrator's (tier 1),
//   and capturing evidence is any staff member's;
// - the evidence record carries the mask only: the raw number is masked and thrown away, never stored or echoed;
// - the recompute only ever adds rows, and a second run skips what the first added;
// - the core's refusals keep its sentences, which still say "band-card", "status card" and "TCRD" (the app
//   rewrites them into the dealer's words; this double keeps them so the rewrite is held).
//
// The world is invented and small: Gable Landscape Supply's California sales tax at the San Diego County
// combined rate, three exemption profiles, four evidence records, and the sales of the month before today (dates
// are cut from the host's own day, so no calendar literal is written).

export const IDS = {
  controller: "de110000-0000-4000-9000-000000000008",
  payrec: "de110000-0000-4000-9000-000000000007",
  manager: "de110000-0000-4000-9000-000000000002",
  rateStandard: "7a000000-0000-4000-8000-000000000001",
  rateManual: "7a000000-0000-4000-8000-000000000002",
  rateDelivery: "7a000000-0000-4000-8000-000000000003",
  profileResale: "7b000000-0000-4000-8000-000000000001",
  profileNonprofit: "7b000000-0000-4000-8000-000000000002",
  profileFarm: "7b000000-0000-4000-8000-000000000003",
  evidenceOne: "7c000000-0000-4000-8000-000000000001",
  evidenceTwo: "7c000000-0000-4000-8000-000000000002",
  evidenceThree: "7c000000-0000-4000-8000-000000000003",
  evidenceFour: "7c000000-0000-4000-8000-000000000004",
  orderOne: "7d000000-0000-4000-8000-000000000001",
  orderTwo: "7d000000-0000-4000-8000-000000000002",
};

// The reasons the snapshot CHECK lawfully carries (core LegalReasonCodes) and the ones that bind a relief
// only with evidence attached (core evidenceGatedReasons).
export const LEGAL_REASONS = [
  "STD_ON_HST_13",
  "STD_SALES_TAX",
  "ON_FN_POS_8",
  "FED_S87_GOODS_ON_RES",
  "FED_S87_VENDOR_DELIVERY",
  "FED_S87_SERVICE_RESERVE",
  "FED_BAND_MANAGEMENT",
  "FNGST_5",
  "FNT_PRODUCT",
  "TREATY_OR_SELF_GOV_REVIEW",
  "MANUAL_TAX_REVIEW",
  "RELIEF_DECLINED_EVIDENCE",
];
const GATED = ["FED_S87_VENDOR_DELIVERY", "ON_FN_POS_8", "FNGST_5"];
const STANDARD = "STD_ON_HST_13";
const MANUAL = "MANUAL_TAX_REVIEW";
// The core's one standard-rate constant: a profile under it must force the certificate number.
const STANDARD_BPS = 1300;
const ECHO_BOUND = 48;

const pad = (n, w = 2) => String(n).padStart(w, "0");
const DATE = /^\d{4}-\d{2}-\d{2}$/;
const validDate = (s) => DATE.test(s) && !Number.isNaN(Date.parse(`${s}T00:00:00Z`)) && new Date(`${s}T00:00:00Z`).toISOString().slice(0, 10) === s;
const today = () => new Date().toISOString().slice(0, 10);
const addDays = (days) => new Date(Date.now() + days * 86_400_000).toISOString().slice(0, 10);

/** The month before today, from the host's own clock: the month the sales and the ledger tie out for. */
export function taxMonth() {
  const before = new Date(new Date().getFullYear(), new Date().getMonth() - 1, 1);
  return { year: before.getFullYear(), month: before.getMonth() + 1 };
}
/** The month before that one: the month whose ledger is a little off. */
export function olderMonth() {
  const before = new Date(new Date().getFullYear(), new Date().getMonth() - 2, 1);
  return { year: before.getFullYear(), month: before.getMonth() + 1 };
}

// The tie out the stub holds, in cents: the month before today ties; the month before that is off.
export const RECON = { tiedCents: 482_375, olderCollectedCents: 391_250, olderLedgerCents: 389_900 };

// Go's %q of a string, close enough for the reason codes the tests send (printable ASCII with quotes escaped).
const goQuote = (s) => `"${s.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
const bound = (reason) => {
  const runes = [...reason];
  return runes.length <= ECHO_BOUND ? goQuote(reason) : `${goQuote(runes.slice(0, ECHO_BOUND).join(""))}...`;
};

function maskNumber(raw) {
  const digits = [...String(raw)].filter((c) => c >= "0" && c <= "9");
  return digits.length < 4 ? "•••• ••••" : `•••• ${digits.slice(-4).join("")}`;
}

function fresh() {
  const from = (days) => addDays(days);
  return {
    rates: [
      { id: IDS.rateStandard, jurisdiction: "CA", reason_code: STANDARD, rate_bps: 775, effective_from: from(-400), notes: "San Diego County combined rate." },
      { id: IDS.rateManual, jurisdiction: "CA", reason_code: MANUAL, rate_bps: 775, effective_from: from(-400), notes: "Held for a person to decide." },
      { id: IDS.rateDelivery, jurisdiction: "CA", reason_code: "FED_S87_VENDOR_DELIVERY", rate_bps: 0, effective_from: from(-400), notes: "Exempt delivery, proof of delivery attached." },
    ],
    profiles: [
      { id: IDS.profileResale, code: "RESALE", name: "Resale certificate", rate_bps: 0, requires_photo: true, requires_number: true, requires_attestation: true, status: "ACTIVE", notes: "Contractors buying to resell." },
      { id: IDS.profileNonprofit, code: "EXEMPT_ORG", name: "Exempt organization certificate", rate_bps: 0, requires_photo: true, requires_number: true, requires_attestation: true, status: "ACTIVE" },
      { id: IDS.profileFarm, code: "FARM", name: "Agricultural exemption", rate_bps: 775, requires_photo: false, requires_number: true, requires_attestation: true, status: "PENDING", notes: "Waiting to be ratified." },
    ],
    config: { hst_registration_no: "SR-DEMO-0001", round_mode: "INVOICE_TOTAL", avalara_enabled: false, postal_routing_enabled: false, notes: "" },
    evidence: [
      { id: IDS.evidenceOne, profile_id: "", card_profile_id: IDS.profileResale, order_id: IDS.orderOne, photo_ref: "evidence/resale-1.jpg", number_mask: "•••• 4821", attestation: true, tcrd_expiry: addDays(200), captured_by: IDS.controller, created_at: `${addDays(-9)}T09:30:00Z`, seq: 1 },
      { id: IDS.evidenceTwo, profile_id: "", card_profile_id: IDS.profileResale, order_id: IDS.orderTwo, photo_ref: "evidence/resale-2.jpg", number_mask: "•••• 9034", attestation: true, tcrd_expiry: addDays(-20), captured_by: IDS.controller, created_at: `${addDays(-6)}T14:05:00Z`, seq: 2 },
      { id: IDS.evidenceThree, profile_id: "", card_profile_id: IDS.profileNonprofit, order_id: "", photo_ref: "evidence/org-1.jpg", number_mask: "•••• 1177", attestation: false, tcrd_expiry: "", captured_by: IDS.manager, created_at: `${addDays(-3)}T11:12:00Z`, seq: 3 },
      { id: IDS.evidenceFour, profile_id: "", card_profile_id: IDS.profileNonprofit, order_id: "", photo_ref: "evidence/org-2.jpg", number_mask: "•••• ••••", attestation: true, tcrd_expiry: "", captured_by: IDS.manager, created_at: `${addDays(-1)}T16:40:00Z`, seq: 4 },
    ],
    // The earlier flat snapshots a recompute re-derives: six sales in the month before today, none re-derived yet.
    sales: Array.from({ length: 6 }, (_, i) => ({ order: `7e000000-0000-4000-8000-${String(i + 1).padStart(12, "0")}`, derived: false })),
    nextRate: 4,
    nextProfile: 4,
    nextEvidence: 5,
    tick: 100,
  };
}

const clientError = (status, message) => ({ status, body: { error: message } });

export function createTaxWorld() {
  let world = fresh();
  const reset = () => {
    world = fresh();
  };

  const callerOf = (req) => {
    const token = String(req.headers.authorization ?? "").replace(/^Bearer /, "");
    try {
      const sub = JSON.parse(Buffer.from(token.split(".")[1], "base64url").toString("utf8")).sub;
      // The tiers the core's guards read: the controller is tier 1 global (admin), the Payables and Receivables
      // role and the Sales manager are tier 2 (manager), the rest are staff.
      // global is the caller's branch reach (FW1-10): the controller and the Payables and
      // Receivables role are dealer wide, the Sales manager is Point Loma alone.
      if (sub === IDS.controller) return { id: sub, tier: 1, global: true };
      if (sub === IDS.payrec) return { id: sub, tier: 2, global: true };
      if (sub === IDS.manager) return { id: sub, tier: 2, global: false };
      return { id: sub, tier: 3, global: false };
    } catch {
      return { id: "", tier: 3 };
    }
  };

  const forbidden = () => ({ status: 403, body: { error: "Forbidden" } });
  const listBody = (key, rows) => ({ status: 200, body: { [key]: rows.length === 0 ? null : rows } });

  const rateView = (r) => {
    const out = { id: r.id, jurisdiction: r.jurisdiction, reason_code: r.reason_code, rate_bps: r.rate_bps, effective_from: r.effective_from };
    if (r.effective_to) out.effective_to = r.effective_to;
    if (r.notes) out.notes = r.notes;
    return out;
  };
  const profileView = (p) => {
    const out = { id: p.id, code: p.code, name: p.name, rate_bps: p.rate_bps, requires_photo: p.requires_photo, requires_number: p.requires_number, requires_attestation: p.requires_attestation, status: p.status };
    if (p.notes) out.notes = p.notes;
    return out;
  };
  const evidenceView = (e) => {
    const out = { id: e.id };
    if (e.profile_id) out.profile_id = e.profile_id;
    if (e.card_profile_id) out.card_profile_id = e.card_profile_id;
    if (e.order_id) out.order_id = e.order_id;
    if (e.photo_ref) out.photo_ref = e.photo_ref;
    out.number_mask = e.number_mask;
    out.attestation = e.attestation;
    if (e.tcrd_expiry) out.tcrd_expiry = e.tcrd_expiry;
    if (e.captured_by) out.captured_by = e.captured_by;
    if (e.created_at) out.created_at = e.created_at;
    out.attestation_status = "PENDING_LEGAL_REVIEW";
    return out;
  };
  const configView = (c) => {
    const out = { hst_registration_no: c.hst_registration_no, round_mode: c.round_mode, avalara_enabled: c.avalara_enabled, postal_routing_enabled: c.postal_routing_enabled };
    if (c.notes) out.notes = c.notes;
    return out;
  };

  // ---- rates -------------------------------------------------------------------------------------------

  /** The rows overlapping [from, to] (to empty is open ended): the core's MaxRateInRange for the standard reason. */
  const maxStandardOver = (jurisdiction, from, to) => {
    const rows = world.rates.filter(
      (r) => r.jurisdiction === jurisdiction && r.reason_code === STANDARD && (r.effective_to === undefined || r.effective_to === "" || r.effective_to >= from) && (to === "" || r.effective_from <= to),
    );
    return rows.length === 0 ? null : Math.max(...rows.map((r) => r.rate_bps));
  };

  function createRate(input) {
    if (!input.reason_code) return clientError(400, "reason_code required");
    if (!LEGAL_REASONS.includes(input.reason_code)) return clientError(400, `unknown reason_code ${bound(String(input.reason_code))}: not one of the lawful reason codes the rate table can carry`);
    if (!Number.isInteger(input.rate_bps) || input.rate_bps < 0 || input.rate_bps > 10000) return clientError(400, "rate_bps must be 0..10000");
    const jurisdiction = input.jurisdiction || "ON";
    const from = input.effective_from || today();
    if (!validDate(from)) return clientError(400, "effective_from must be YYYY-MM-DD");
    const to = input.effective_to ?? "";
    if (input.reason_code !== STANDARD && input.reason_code !== MANUAL && !GATED.includes(input.reason_code)) {
      const std = maxStandardOver(jurisdiction, from, to);
      if (std !== null && input.rate_bps < std) {
        return clientError(
          400,
          `tax reason ${bound(input.reason_code)} resolves ${input.rate_bps} bps, below a standard of ${std} bps, without an evidence gate and without the manual exception: the rate row is misconfigured against the gate map`,
        );
      }
    }
    const row = { id: `7a000000-0000-4000-8000-${String(world.nextRate++).padStart(12, "0")}`, jurisdiction, reason_code: input.reason_code, rate_bps: input.rate_bps, effective_from: from, effective_to: to, notes: input.notes ?? "" };
    world.rates.push(row);
    return { status: 201, body: rateView(row) };
  }

  // ---- profiles ----------------------------------------------------------------------------------------

  function upsertProfile(input) {
    if (!input.code) return clientError(400, "code required");
    if (!Number.isInteger(input.rate_bps) || input.rate_bps < 0 || input.rate_bps > 10000) return clientError(400, "rate_bps must be 0..10000");
    if (input.rate_bps < STANDARD_BPS && !input.requires_number) return clientError(400, "a relief profile must force the band-card number as evidence");
    const status = input.status || "ACTIVE";
    if (!["ACTIVE", "PENDING", "RETIRED"].includes(status)) return clientError(500, "internal error");
    const at = world.profiles.findIndex((p) => p.code === input.code);
    const row = {
      id: at >= 0 ? world.profiles[at].id : `7b000000-0000-4000-8000-${String(world.nextProfile++).padStart(12, "0")}`,
      code: input.code,
      name: input.name ?? "",
      rate_bps: input.rate_bps,
      requires_photo: input.requires_photo === true,
      requires_number: input.requires_number === true,
      requires_attestation: input.requires_attestation === true,
      status,
      notes: input.notes ?? "",
    };
    if (at >= 0) world.profiles[at] = row;
    else world.profiles.push(row);
    return { status: 200, body: profileView(row) };
  }

  const profilesOrdered = () => [...world.profiles].sort((a, b) => a.rate_bps - b.rate_bps || (a.code < b.code ? -1 : a.code > b.code ? 1 : 0));

  // ---- evidence ----------------------------------------------------------------------------------------

  function captureEvidence(input, caller) {
    let profile = null;
    if (input.card_profile_code) {
      profile = world.profiles.find((p) => p.code === input.card_profile_code) ?? null;
      if (profile === null) return clientError(400, "unknown card_profile_code");
    }
    if (profile && profile.requires_photo && !input.photo_ref) return clientError(400, "band-card photo is required for this profile");
    if (profile && profile.requires_number && !input.raw_number) return clientError(400, "band-card number is required for this profile");
    if (profile && profile.requires_attestation && input.attestation !== true) return clientError(400, "attestation is required for this profile");
    const row = {
      id: `7c000000-0000-4000-8000-${String(world.nextEvidence++).padStart(12, "0")}`,
      profile_id: input.profile_id ?? "",
      card_profile_id: profile ? profile.id : "",
      order_id: input.order_id ?? "",
      photo_ref: input.photo_ref ?? "",
      // The raw number is masked here and goes no further.
      number_mask: maskNumber(input.raw_number ?? ""),
      attestation: input.attestation === true,
      tcrd_expiry: input.tcrd_expiry ?? "",
      captured_by: caller.id,
      created_at: new Date(Date.now() + world.tick++).toISOString().replace(/\.\d+Z$/, "Z"),
      seq: world.nextEvidence,
    };
    world.evidence.push(row);
    return { status: 201, body: evidenceView(row) };
  }

  function listEvidence(url) {
    const order = url.searchParams.get("order_id") ?? "";
    const profile = url.searchParams.get("profile_id") ?? "";
    let limit = Number(url.searchParams.get("limit") ?? 0);
    if (!Number.isInteger(limit) || limit <= 0 || limit > 500) limit = 200;
    const rows = world.evidence
      .filter((e) => (order === "" || e.order_id === order) && (profile === "" || e.profile_id === profile))
      .sort((a, b) => b.seq - a.seq)
      .slice(0, limit);
    return listBody("evidence", rows.map(evidenceView));
  }

  function validateEvidence(id, url) {
    const asOf = url.searchParams.get("as_of") ?? "";
    const on = asOf === "" ? today() : asOf;
    if (asOf !== "" && !validDate(asOf)) return clientError(400, "date must be YYYY-MM-DD");
    const ev = world.evidence.find((e) => e.id === id);
    if (!ev) return clientError(400, "evidence not found");
    const out = { eligible: true, number_mask: ev.number_mask };
    if (!ev.attestation) {
      out.eligible = false;
      out.block_reason = "attestation missing";
      return { status: 200, body: out };
    }
    if (ev.tcrd_expiry !== "" && ev.tcrd_expiry < on) {
      out.eligible = false;
      out.block_reason = "status card (TCRD) expired";
    }
    return { status: 200, body: out };
  }

  // ---- config ------------------------------------------------------------------------------------------

  function updateConfig(input) {
    const mode = input.round_mode || "INVOICE_TOTAL";
    if (mode !== "INVOICE_TOTAL" && mode !== "PER_LINE") return clientError(400, "round_mode must be INVOICE_TOTAL or PER_LINE");
    if (input.avalara_enabled === true || input.postal_routing_enabled === true) return clientError(400, "avalara/postal routing are dormant at W6 and cannot be enabled");
    world.config = { hst_registration_no: input.hst_registration_no ?? "", round_mode: mode, avalara_enabled: false, postal_routing_enabled: false, notes: input.notes ? input.notes : world.config.notes };
    return { status: 200, body: configView(world.config) };
  }

  // ---- recompute and recon -----------------------------------------------------------------------------

  function recompute(input) {
    const from = input.from_date ?? "";
    const to = input.to_date ?? "";
    // The core hands the dates to the database as they come: a date it cannot read is a server fault.
    if ((from !== "" && !validDate(from)) || (to !== "" && !validDate(to))) return clientError(500, "internal error");
    const out = { from_date: from, to_date: to, scanned: world.sales.length, inserted: 0, skipped_exists: 0 };
    for (const sale of world.sales) {
      if (sale.derived) out.skipped_exists += 1;
      else {
        sale.derived = true;
        out.inserted += 1;
      }
    }
    return { status: 200, body: out };
  }

  function recon(url) {
    const year = Number(url.searchParams.get("year") ?? 0);
    const month = Number(url.searchParams.get("month") ?? 0);
    if (!Number.isInteger(year) || year === 0) return clientError(400, "year required");
    const prior = taxMonth();
    const older = olderMonth();
    let collected = 0;
    let ledger = 0;
    if (year === prior.year && month === prior.month) {
      collected = RECON.tiedCents;
      ledger = RECON.tiedCents;
    } else if (year === older.year && month === older.month) {
      collected = RECON.olderCollectedCents;
      ledger = RECON.olderLedgerCents;
    }
    const out = { year, month, collected_cents: collected, gl_payable_cents: ledger, gl_federal_cents: 0, gl_ontario_cents: 0, variance_cents: ledger - collected, tied_out: ledger - collected === 0 };
    const branch = url.searchParams.get("branch_id");
    if (branch) out.branch_id = branch;
    return { status: 200, body: out };
  }

  function handle(req, url, bodyText) {
    const method = req.method;
    const path = url.pathname;
    if (method === "POST" && path === "/api/v1/tax/_reset") {
      reset();
      return { status: 200, body: { ok: true } };
    }
    if (!path.startsWith("/api/v1/tax/")) return null;
    const caller = callerOf(req);
    const route = `${method} ${path.replace(/\/[0-9a-f-]{36}(\/(?:download|validate))?$/i, (_, tail) => `/{id}${tail ?? ""}`)}`;
    const writes = ["POST /api/v1/tax/rates", "POST /api/v1/tax/profiles", "POST /api/v1/tax/config", "POST /api/v1/tax/recompute"];
    const capture = route === "POST /api/v1/tax/evidence";
    // The guards, per route: capture any staff, every write above an administrator's, every other route a manager's.
    if (!capture) {
      if (writes.includes(route) && caller.tier !== 1) return forbidden();
      if (caller.tier > 2) return forbidden();
    }
    let input = {};
    if (method === "POST" && !route.endsWith("/download") && !route.endsWith("/validate")) {
      try {
        input = JSON.parse(bodyText);
      } catch {
        return clientError(400, "invalid_json");
      }
    }
    switch (route) {
      case "GET /api/v1/tax/rates":
        return listBody("rates", [...world.rates].sort((a, b) => (a.jurisdiction + a.reason_code < b.jurisdiction + b.reason_code ? -1 : a.jurisdiction + a.reason_code > b.jurisdiction + b.reason_code ? 1 : b.effective_from.localeCompare(a.effective_from))).map(rateView));
      case "POST /api/v1/tax/rates":
        return createRate(input);
      case "GET /api/v1/tax/profiles":
        return listBody("profiles", profilesOrdered().map(profileView));
      case "POST /api/v1/tax/profiles":
        return upsertProfile(input);
      case "GET /api/v1/tax/review-queue":
        // The queue is the core's own slice, never null (make([]StatusCardProfile, 0)), unlike the other three.
        return { status: 200, body: { pending_profiles: profilesOrdered().filter((p) => p.status === "PENDING").map(profileView) } };
      case "GET /api/v1/tax/config":
        return { status: 200, body: configView(world.config) };
      case "POST /api/v1/tax/config":
        return updateConfig(input);
      case "GET /api/v1/tax/evidence":
        return listEvidence(url);
      case "POST /api/v1/tax/evidence":
        return captureEvidence(input, caller);
      case "GET /api/v1/tax/evidence/{id}": {
        const ev = world.evidence.find((e) => e.id === path.split("/").at(-1));
        return ev ? { status: 200, body: evidenceView(ev) } : clientError(404, "evidence not found");
      }
      case "POST /api/v1/tax/evidence/{id}/download": {
        const ev = world.evidence.find((e) => e.id === path.split("/").at(-2));
        return ev ? { status: 200, body: evidenceView(ev) } : clientError(404, "evidence not found");
      }
      case "POST /api/v1/tax/evidence/{id}/validate":
        return validateEvidence(path.split("/").at(-2), url);
      case "POST /api/v1/tax/recompute":
        return recompute(input);
      case "GET /api/v1/tax/recon": {
        // The reconcile is fenced to the caller's branch reach (FW1-10): a named branch must be
        // reachable, and the unnamed figure is the whole company's books, a dealer wide caller's
        // read. The Sales manager holds Point Loma alone.
        const branch = url.searchParams.get("branch_id") ?? "";
        if (!caller.global && branch === "") return clientError(403, "tax: caller cannot access this branch");
        if (!caller.global && branch !== "b0000000-0000-4000-8000-000000000001") return clientError(403, "tax: caller cannot access this branch");
        return recon(url);
      }
      default:
        return null;
    }
  }

  return { handle, reset };
}
