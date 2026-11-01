// The liens and compliance stub world: the lien jobs, the holdback ledger, the aging and the Monday triage, the
// documents and their versions, the dual gated release, the compliance calendar and the shared review status banner,
// and the staff roster the co-signer is picked from, answered the way the core answers them. This is a test double
// only; the real routes are the core's (core/internal/lien, core/internal/compliance). Its rules, its refusal order
// and its sentences are held to the contract and to the core's own source by test/lien-stub-contract.test.mjs, so it
// cannot drift in silence.
//
// What it copies from the core, on purpose, quirks included:
// - an empty list answers {"items": null} (the Go slice is nil), except the triage, which is always an array;
// - the lists answer every row with no cursor (the contract's page shape, next_cursor absent);
// - a job id or a document id that is not a uuid is a 500, as the database refuses the cast;
// - creating a job with a substantial performance date registers its holdback release deadline on the calendar;
// - the documents list is the latest version of each form, the versions list is newest first, and the render is
//   append only (a second render of one form is its next version);
// - the sync books one RETENTION row per held invoice and skips the invoices already booked, so a re run books nothing;
// - the release checks, in order: the gatekeeper is the General Manager, the co-signer is another person, the
//   co-signer resolves to an active Financial Controller or Payables and Receivables, the job exists, the holdback is
//   not payable, there is something outstanding; the release then posts one journal and answers a plain date;
// - the review status answers a draft banner of the core's own words when no row exists.
//
// RT-3b adds the holdback release the Controller makes per customer job (core/internal/billing/holdback_release.go):
// - the due to release read answers one row per held invoice with the judgement the core gives its customer job, the
//   rule of core/internal/lien/release.go (any undischarged preserved lien refuses first, then any job with no date,
//   then the latest last day over the linked jobs), and the due total by branch;
// - the release is the Controller's alone and posts the whole job at once; it replays on its key, and every refusal
//   but a role or a branch is a 409; the reversal puts one invoice's holdback back and needs a reason;
// - the lien doors (a preserved lien, its discharge, the three preservation dates) and the old job level release
//   refusing a lien job that is linked to a customer job.
//
// The world is invented and small (Gable Landscape Supply): five jobs across the two yards, the retention held on
// them aged from days to months, three held invoices to sync, and a calendar of the deadlines the jobs registered.
// Dates are cut from the host's own day, so no calendar literal is written.

export const IDS = {
  controller: "de110000-0000-4000-9000-000000000008",
  payrec: "de110000-0000-4000-9000-000000000007",
  manager: "de110000-0000-4000-9000-000000000002",
  sales: "de110000-0000-4000-9000-000000000001",
  generalManager: "de110000-0000-4000-9000-0000000000a9",
  formerPayrec: "de110000-0000-4000-9000-0000000000b9",
  branch1: "b0000000-0000-4000-8000-000000000001",
  branch2: "b0000000-0000-4000-8000-000000000002",
  customerA: "c0000000-0000-4000-8000-0000000000a1",
  customerB: "c0000000-0000-4000-8000-0000000000b1",
  job1: "10b00000-0000-4000-8000-000000000001",
  job2: "10b00000-0000-4000-8000-000000000002",
  job3: "10b00000-0000-4000-8000-000000000003",
  job4: "10b00000-0000-4000-8000-000000000004",
  job5: "10b00000-0000-4000-8000-000000000005",
  job6: "10b00000-0000-4000-8000-000000000006",
  cjA: "c70b0000-0000-4000-8000-0000000000a1",
  cjB: "c70b0000-0000-4000-8000-0000000000b1",
  cjC: "c70b0000-0000-4000-8000-0000000000c1",
};

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const DAY = 86_400_000;
const RULE_DAYS = 45;
const uuid = (kind, n) => `${kind.padEnd(8, "0")}-0000-4000-8000-${String(n).padStart(12, "0")}`;

/** A day this many days from the host's own today, as the core writes a date. */
export const dayFrom = (days, from = Date.now()) => new Date(from + days * DAY).toISOString().slice(0, 10);
const todayStart = () => Date.UTC(new Date().getUTCFullYear(), new Date().getUTCMonth(), new Date().getUTCDate());

// The draft banner every rendered document is headed by, and the default review banner, as the core words them.
const DRAFT_BANNER = "*** DRAFT, statute-derived, pending client validation ***";
const DEFAULT_BANNER = "DRAFT, statute-derived, pending client validation";

// The core's document forms (core/internal/lien/template.go): the title and the ordered merge lines.
const TEMPLATES = {
  CONDITIONAL_RELEASE: {
    title: "Conditional Release of Preserved Lien (Payment)",
    lines: [["Job / Improvement", "job_no"], ["Owner", "owner"], ["Contractor / Claimant", "claimant"], ["Amount of this payment", "payment_amount"], ["Holdback retained", "holdback_amount"], ["Condition", "condition"]],
  },
  UNCONDITIONAL_RELEASE: {
    title: "Unconditional Release of Preserved Lien",
    lines: [["Job / Improvement", "job_no"], ["Owner", "owner"], ["Contractor / Claimant", "claimant"], ["Amount received in full", "payment_amount"]],
  },
  STATUTORY_DECLARATION: {
    title: "Statutory Declaration (Construction Act)",
    lines: [["Job / Improvement", "job_no"], ["Declarant", "declarant"], ["Capacity", "capacity"], ["All accounts for labour/services/materials paid", "accounts_paid"], ["Exceptions", "exceptions"]],
  },
  DISCHARGE: {
    title: "Discharge of Preserved Lien",
    lines: [["Job / Improvement", "job_no"], ["Lien claim number", "lien_claim_no"], ["Claimant", "claimant"], ["Discharged amount", "discharged_amount"]],
  },
  NOTICE: {
    title: "Notice (Construction Act)",
    lines: [["Job / Improvement", "job_no"], ["To", "recipient"], ["Subject", "subject"], ["Body", "body"]],
  },
  OTHER: { title: "Construction Act Document", lines: [["Job / Improvement", "job_no"], ["Body", "body"]] },
};

function mergeValue(data, key) {
  const v = data[key];
  if (v === undefined || v === null) return "____";
  const s = String(v).trim();
  return s === "" ? "____" : s;
}

function render(kind, version, data) {
  const spec = TEMPLATES[kind];
  let out = `${DRAFT_BANNER}\n\n${spec.title}  (v${version})\n${"-".repeat(spec.title.length + 8)}\n`;
  for (const [label, key] of spec.lines) out += `${label}: ${mergeValue(data, key)}\n`;
  const consumed = new Set(spec.lines.map(([, key]) => key));
  const extra = Object.keys(data).filter((k) => !consumed.has(k)).sort();
  if (extra.length > 0) {
    out += "\nAdditional:\n";
    for (const k of extra) out += `  ${k}: ${mergeValue(data, k)}\n`;
  }
  return out;
}

function linked(world) {
  // The lien jobs a customer job's holdback counts from: the Summit Ridge walkway job and the Fieldstone patio job.
  world.jobs.find((j) => j.id === IDS.job6).customer_job_id = IDS.cjA;
  world.jobs.find((j) => j.id === IDS.job3).customer_job_id = IDS.cjB;
  return world;
}

function fresh() {
  const at = (ago) => new Date(Date.now() - ago * DAY).toISOString();
  const job = (id, jobNo, customer, branch, description, contract, bps, perfAgo, status, direction, createdAgo) => ({
    id,
    job_no: jobNo,
    ...(customer === "" ? {} : { customer_id: customer }),
    branch_id: branch,
    description,
    contract_cents: contract,
    holdback_pct_bps: bps,
    ...(perfAgo === null ? {} : { substantial_perf_date: dayFrom(-perfAgo), last_supply_date: dayFrom(-perfAgo - 5) }),
    status,
    holdback_direction: direction,
    hst_on_holdback: false,
    created_at: at(createdAgo),
  });
  const entry = (jobId, invoiceId, type, cents, ago, extra = {}) => ({ job_id: jobId, invoice_id: invoiceId, entry_type: type, amount_cents: cents, created_at: at(ago), ...extra });
  const deadline = (n, jobId, kind, dueAgo, status) => ({ id: uuid("c1", n), job_id: jobId, deadline_kind: kind, due_date: dayFrom(-dueAgo), rule_days: RULE_DAYS, status });
  return {
    // Newest first, as the core lists them.
    jobs: [
      job(IDS.job3, "GLS-2103", IDS.customerB, IDS.branch1, "Patio pavers, Fieldstone Pools and Patios", 4_800_000, 1000, 12, "SUBSTANTIALLY_PERFORMED", "RECEIVABLE", 14),
      job(IDS.job4, "GLS-2104", "", IDS.branch2, "Wall block supply, Granite Run Quarry subcontract", 3_200_000, 1000, 50, "SUBSTANTIALLY_PERFORMED", "PAYABLE", 40),
      job(IDS.job1, "GLS-2101", IDS.customerA, IDS.branch1, "Pool deck pavers and drainage, Summit Ridge Builders", 18_600_000, 1000, 70, "SUBSTANTIALLY_PERFORMED", "RECEIVABLE", 80),
      job(IDS.job2, "GLS-2102", IDS.customerA, IDS.branch2, "Retaining wall and coping, Ridgeview Outdoor Living", 9_250_000, 700, 130, "SUBSTANTIALLY_PERFORMED", "RECEIVABLE", 140),
      job(IDS.job6, "GLS-2105", IDS.customerA, IDS.branch1, "Walkway and steps, Summit Ridge Builders", 15_000_000, 1000, 75, "SUBSTANTIALLY_PERFORMED", "RECEIVABLE", 90),
      job(IDS.job5, "GLS-2100", IDS.customerB, IDS.branch1, "Walkway, Heronfield Stone", 5_000_000, 1000, 150, "RELEASED", "RECEIVABLE", 160),
    ],
    entries: [
      entry(IDS.job1, uuid("a1", 1), "RETENTION", 840_000, 62),
      entry(IDS.job1, uuid("a1", 2), "RETENTION", 1_020_000, 40),
      entry(IDS.job2, uuid("a1", 3), "RETENTION", 647_500, 100),
      entry(IDS.job3, uuid("a1", 4), "RETENTION", 336_000, 12),
      entry(IDS.job3, uuid("a3", 1), "RETENTION", 210_000, 12),
      entry(IDS.job6, uuid("a2", 1), "RETENTION", 630_000, 66),
      entry(IDS.job6, uuid("a2", 2), "RETENTION", 750_000, 41),
      entry(IDS.job4, uuid("a1", 5), "RETENTION", 290_000, 38),
      entry(IDS.job5, uuid("a1", 6), "RETENTION", 500_000, 120),
      entry(IDS.job5, "", "RELEASE", 500_000, 20, { gl_journal_id: uuid("7a", 1), released_at: dayFrom(-20) }),
    ],
    // The invoices carrying a holdback, as billing reports them to the sync.
    invoices: [
      { invoice_id: uuid("a1", 1), invoice_no: "INV-3201", customer_id: IDS.customerA, branch_id: IDS.branch1, holdback_cents: 840_000, finalized_at: at(70) },
      { invoice_id: uuid("a1", 2), invoice_no: "INV-3202", customer_id: IDS.customerA, branch_id: IDS.branch1, holdback_cents: 1_020_000, finalized_at: at(45) },
      { invoice_id: uuid("a1", 7), invoice_no: "INV-3301", customer_id: IDS.customerA, branch_id: IDS.branch1, holdback_cents: 420_000, finalized_at: at(3) },
      { invoice_id: uuid("a1", 8), invoice_no: "INV-3302", customer_id: IDS.customerB, branch_id: IDS.branch1, holdback_cents: 126_000, finalized_at: at(2) },
    ],
    // The held invoices of the customer jobs the Controller releases, in the order the core reads them. Each one's
    // RETENTION sits on the ledger of the lien job its row names (the same invoice id, customer and amount), and that
    // lien job is linked to the customer job; INV-4301 is the one held invoice whose customer job has no lien job.
    dueInvoices: [
      { invoice_id: uuid("a2", 1), invoice_no: "INV-4101", customer_id: IDS.customerA, customer_job_id: IDS.cjA, branch_id: IDS.branch1, holdback_cents: 630_000 },
      { invoice_id: uuid("a2", 2), invoice_no: "INV-4102", customer_id: IDS.customerA, customer_job_id: IDS.cjA, branch_id: IDS.branch1, holdback_cents: 750_000 },
      { invoice_id: uuid("a1", 4), invoice_no: "INV-4201", customer_id: IDS.customerB, customer_job_id: IDS.cjB, branch_id: IDS.branch1, holdback_cents: 336_000 },
      // The same customer job's second invoice sits at the other yard: a job split across two branches.
      { invoice_id: uuid("a3", 1), invoice_no: "INV-4202", customer_id: IDS.customerB, customer_job_id: IDS.cjB, branch_id: IDS.branch2, holdback_cents: 210_000 },
      { invoice_id: uuid("a2", 4), invoice_no: "INV-4301", customer_id: IDS.customerB, customer_job_id: IDS.cjC, branch_id: IDS.branch1, holdback_cents: 120_000 },
    ],
    // One row per invoice released: { id, invoice_id, customer_job_id, amount_cents, key, reversed }.
    releases: [],
    nextRelease: 1,
    documents: [],
    deadlines: [
      deadline(1, IDS.job1, "HOLDBACK_RELEASE", 25, "OPEN"),
      deadline(2, IDS.job2, "HOLDBACK_RELEASE", 85, "OPEN"),
      deadline(3, IDS.job3, "HOLDBACK_RELEASE", -33, "OPEN"),
      deadline(4, IDS.job4, "HOLDBACK_RELEASE", -5, "OPEN"),
      deadline(5, IDS.job5, "HOLDBACK_RELEASE", 105, "SATISFIED"),
      deadline(6, IDS.job1, "NOTICE_DEADLINE", -20, "OPEN"),
      deadline(7, IDS.job2, "PROOF_OF_CLAIM", 10, "WAIVED"),
    ],
    reviews: [],
    // The staff roster: the people a co-signer can be, and a few who cannot. The role codes are the core's stored form.
    roster: [
      { user_id: IDS.generalManager, email: "general.manager@hh-demo.invalid", display_name: "General Manager (demo)", role_code: "GENERAL_MANAGER", role_name: "General Manager", tier: 1, scope: "GLOBAL", branch_ids: [] },
      { user_id: IDS.controller, email: "financial.controller@hh-demo.invalid", display_name: "Financial Controller (demo)", role_code: "FINANCIAL_CONTROLLER", role_name: "Financial Controller", tier: 1, scope: "GLOBAL", branch_ids: [] },
      { user_id: IDS.payrec, email: "payables.receivables@hh-demo.invalid", display_name: "Payables and Receivables (demo)", role_code: "PAYABLES_RECEIVABLES", role_name: "Payables and Receivables", tier: 2, scope: "GLOBAL", branch_ids: [] },
      { user_id: IDS.formerPayrec, email: "former.payables@hh-demo.invalid", display_name: "Payables and Receivables (former, demo)", role_code: "PAYABLES_RECEIVABLES", role_name: "Payables and Receivables", tier: 2, scope: "GLOBAL", branch_ids: [] },
      { user_id: IDS.manager, email: "sales.manager@hh-demo.invalid", display_name: "Sales manager (demo)", role_code: "SALES_MANAGER", role_name: "Sales Manager", tier: 2, scope: "BRANCH", branch_ids: [IDS.branch1] },
    ],
    // The people the core would resolve as not active: the roster cannot say, the release can.
    inactive: [IDS.formerPayrec],
    nextJob: 7,
    nextDoc: 1,
    nextJournal: 2,
    nextDeadline: 8,
    tick: 0,
  };
}

const clientError = (status, message) => ({ status, body: { error: message } });
const forbidden = () => clientError(403, "Forbidden");
const internal = () => clientError(500, "internal error");
const page = (items) => ({ status: 200, body: { items: items.length === 0 ? null : items } });

export function createLienWorld() {
  let world = linked(fresh());
  const reset = () => {
    world = linked(fresh());
  };

  const callerOf = (req) => {
    const token = String(req.headers.authorization ?? "").replace(/^Bearer /, "");
    try {
      const sub = JSON.parse(Buffer.from(token.split(".")[1], "base64url").toString("utf8")).sub;
      // The tiers the core's guards read: the controller and the General Manager are tier 1, Payables and
      // Receivables and the Sales manager tier 2 (manager), the rest are staff and are refused.
      if (sub === IDS.controller) return { id: sub, tier: 1, role: "FINANCIAL_CONTROLLER" };
      if (sub === IDS.generalManager) return { id: sub, tier: 1, role: "GENERAL_MANAGER" };
      if (sub === IDS.payrec) return { id: sub, tier: 2, role: "PAYABLES_RECEIVABLES" };
      if (sub === IDS.manager) return { id: sub, tier: 2, role: "SALES_MANAGER" };
      return { id: sub, tier: 3, role: "INSIDE_SALES" };
    } catch {
      return { id: "", tier: 3, role: "" };
    }
  };

  const outstanding = (jobId) =>
    world.entries
      .filter((e) => e.job_id === jobId)
      .map((e) => (e.entry_type === "RETENTION" ? e.amount_cents : -e.amount_cents))
      .reduce((a, b) => a + b, 0);

  const wholeDays = (iso) => Math.max(0, Math.floor((Date.now() - Date.parse(iso)) / DAY));

  // The pure roll up of core/internal/lien/aging.go: outstanding per job, the oldest retention's age, the bucket.
  function agingRows(branch) {
    const rows = [];
    for (const j of world.jobs) {
      if (branch !== "" && j.branch_id !== branch) continue;
      const mine = world.entries.filter((e) => e.job_id === j.id);
      if (mine.length === 0) continue;
      const left = outstanding(j.id);
      if (left <= 0) continue;
      const retentions = mine.filter((e) => e.entry_type === "RETENTION");
      const days = retentions.length === 0 ? 0 : wholeDays(retentions.map((e) => e.created_at).sort()[0]);
      const bucket = days <= 30 ? "0-30" : days <= 60 ? "31-60" : days <= 90 ? "61-90" : "90+";
      rows.push({
        job_id: j.id,
        job_no: j.job_no,
        branch_id: j.branch_id,
        holdback_direction: j.holdback_direction,
        outstanding_cents: left,
        oldest_days: days,
        bucket,
        release_eligible: j.holdback_direction === "RECEIVABLE" && days >= RULE_DAYS,
      });
    }
    return rows.sort((a, b) => b.oldest_days - a.oldest_days || (a.job_no < b.job_no ? -1 : 1));
  }

  function triageRows(branch) {
    return agingRows(branch).map((r) => {
      const item = { job_id: r.job_id, job_no: r.job_no, branch_id: r.branch_id, holdback_direction: r.holdback_direction, outstanding_cents: r.outstanding_cents, oldest_days: r.oldest_days };
      if (r.holdback_direction === "PAYABLE") return { ...item, badge: "PAYABLE_MANUAL", note: "payable retention, release is manual (registers a deadline, does not post)" };
      if (r.oldest_days > 2 * RULE_DAYS) return { ...item, badge: "OVERDUE", note: "past the release window, action overdue" };
      if (r.oldest_days >= RULE_DAYS) return { ...item, badge: "RELEASE_DUE", note: "at/over the 45-day window, eligible to release" };
      return { ...item, badge: "AGING", note: "retention held, not yet at the release window" };
    });
  }

  const docView = (d) => {
    const { seq, ...wire } = d;
    return wire;
  };

  // ---- the preservation rule (core/internal/lien/release.go) ---------------------------------------------
  /** The display names the core reads from customers.name, in the same query as the row. */
  const CUSTOMER_NAMES = { [IDS.customerA]: "Summit Ridge Builders", [IDS.customerB]: "Fieldstone Pools and Patios" };
  const addDays = (iso, n) => dayFrom(n, Date.parse(`${iso}T00:00:00Z`));

  /** One lien job's facts under the rule's preserve days: the latest date that can start the period wins. */
  function preservationOf(job) {
    const p = { id: job.id, no: job.job_no, basis: "NONE", basisDate: "", last: "", releasable: "", preserved: job.lien_preserved_on !== undefined && job.lien_discharged_on === undefined, preservedOn: job.lien_preserved_on ?? "" };
    for (const [date, name] of [[job.csp_published_on, "CSP_PUBLICATION"], [job.substantial_perf_date, "SUBSTANTIAL_PERFORMANCE"], [job.last_supply_date, "COMPLETION"]]) {
      if (date !== undefined && (p.basisDate === "" || date > p.basisDate)) {
        p.basis = name;
        p.basisDate = date;
      }
    }
    if (p.basisDate !== "") {
      p.last = addDays(p.basisDate, RULE_DAYS);
      p.releasable = addDays(p.last, 1);
    }
    return p;
  }

  /** THE RULE for a customer job: { kind, msg, gov } with kind "" when the holdback is due. */
  function judge(customerJobId) {
    const set = world.jobs.filter((j) => j.customer_job_id === customerJobId).map(preservationOf);
    if (set.length === 0) return { kind: "NO_LIEN_JOB", msg: "no lien job is linked to this job, so there is no preservation period to count", gov: null };
    for (const p of set) {
      if (p.preserved) {
        return { kind: "LIEN_PRESERVED", gov: p, msg: `a lien was preserved on lien job ${p.no} on ${p.preservedOn} and has not been discharged, so the holdback stays held until it is discharged or settled` };
      }
    }
    for (const p of set) {
      if (p.basisDate === "") {
        return { kind: "NO_DATE", gov: p, msg: `lien job ${p.no} has no date to start the preservation period from (the publication of the certificate of substantial performance, the date of substantial performance or the last supply), so the holdback cannot be released` };
      }
    }
    let gov = set[0];
    for (const p of set.slice(1)) if (p.last > gov.last) gov = p;
    if (dayFrom(0) <= gov.last) {
      const basis = gov.basis === "COMPLETION" ? "last supply" : gov.basis === "CSP_PUBLICATION" ? "publication of the certificate of substantial performance" : "substantial performance";
      return { kind: "NOT_DUE", gov, msg: `the holdback is not due: lien job ${gov.no} counts ${RULE_DAYS} days from ${gov.basisDate} (${basis}), so a lien can be preserved through ${gov.last} and the holdback is releasable from ${gov.releasable}` };
    }
    return { kind: "", msg: "", gov };
  }

  const heldInvoices = (cj) => world.dueInvoices.filter((i) => i.customer_job_id === cj && !world.releases.some((r) => r.invoice_id === i.invoice_id && !r.reversed));

  function dueRows() {
    const rows = [];
    const seen = [];
    for (const inv of world.dueInvoices) if (!seen.includes(inv.customer_job_id)) seen.push(inv.customer_job_id);
    for (const cj of seen) {
      const v = judge(cj);
      for (const inv of heldInvoices(cj)) {
        const g = v.gov;
        rows.push({
          invoice_id: inv.invoice_id,
          invoice_no: inv.invoice_no,
          customer_id: inv.customer_id,
          customer_name: CUSTOMER_NAMES[inv.customer_id],
          customer_job_id: cj,
          ...(g === null ? {} : { lien_job_id: g.id, lien_job_no: g.no, basis: g.basis, ...(g.basisDate === "" ? {} : { basis_date: g.basisDate, last_day_to_preserve: g.last, releasable_on: g.releasable }), preserve_days: RULE_DAYS }),
          branch_id: inv.branch_id,
          holdback_cents: inv.holdback_cents,
          due: v.kind === "",
          ...(v.kind === "" ? {} : { blocked_kind: v.kind, blocked_reason: v.msg }),
        });
      }
    }
    return rows;
  }

  const releasedItem = (r) => {
    const inv = world.dueInvoices.find((i) => i.invoice_id === r.invoice_id);
    return { invoice_id: inv.invoice_id, invoice_no: inv.invoice_no, branch_id: inv.branch_id, release_id: r.id, amount_cents: r.amount_cents };
  };

  /** The routes the Controller's holdback release answers (core/internal/billing/holdback_release_handler.go). */
  function handleHoldback(method, path, caller, body, q) {
    if (method === "GET" && path === "/api/v1/ar/holdback/due-to-release") {
      const branch = q.get("branch_id") ?? "";
      if (branch !== "" && !UUID.test(branch)) return internal();
      const items = dueRows().filter((r) => branch === "" || r.branch_id === branch);
      const totals = [];
      for (const r of items) {
        let t = totals.find((x) => x.branch_id === r.branch_id);
        if (t === undefined) totals.push((t = { branch_id: r.branch_id, due_cents: 0 }));
        if (r.due) t.due_cents += r.holdback_cents;
      }
      return { status: 200, body: { items, branches: totals } };
    }
    if (method === "POST" && path === "/api/v1/ar/holdback/releases") {
      if (caller.role !== "FINANCIAL_CONTROLLER") return clientError(403, "releasing a holdback is the Controller's action");
      const cj = String(body.customer_job_id ?? "").trim();
      if (!UUID.test(cj)) return clientError(400, "customer job id must be a uuid");
      const key = String(body.idempotency_key ?? "").trim();
      if (key === "") return clientError(400, "an idempotency key is required");
      const made = world.releases.filter((r) => r.key === key);
      if (made.length > 0) {
        if (made.some((r) => r.reversed)) return clientError(409, "the release this idempotency key made was reversed, so nothing stands released; release the holdback again with a new key");
        if (made[0].customer_job_id !== cj) return clientError(409, "that idempotency key already released the holdback of another job; a key names one release, use a new key");
        const items = made.filter((r) => !r.reversed).map(releasedItem);
        return { status: 200, body: { customer_job_id: cj, released_on: dayFrom(0), total_cents: made.filter((r) => !r.reversed).reduce((n, r) => n + r.amount_cents, 0), items, replayed: true } };
      }
      const held = heldInvoices(cj);
      if (held.length === 0) {
        if (world.releases.some((r) => r.customer_job_id === cj)) return clientError(409, "the holdback of this job has already been released, and no invoice on it withholds any more");
        return clientError(409, "no open invoice on this job withholds any holdback to release");
      }
      const v = judge(cj);
      if (v.kind !== "") return clientError(409, v.msg);
      const rows = held.map((inv) => ({ id: uuid("e1", world.nextRelease++), invoice_id: inv.invoice_id, customer_job_id: cj, amount_cents: inv.holdback_cents, key, reversed: false }));
      world.releases.push(...rows);
      return { status: 201, body: { customer_job_id: cj, released_on: dayFrom(0), total_cents: rows.reduce((n, r) => n + r.amount_cents, 0), items: rows.map(releasedItem), replayed: false } };
    }
    const m = path.match(/^\/api\/v1\/ar\/holdback\/releases\/([^/]+)\/reversal$/);
    if (method === "POST" && m) {
      if (caller.role !== "FINANCIAL_CONTROLLER") return clientError(403, "reversing a holdback release is the Controller's action");
      if (String(body.reason ?? "").trim() === "") return clientError(400, "a reason is required to reverse a release");
      const row = world.releases.find((r) => r.id === m[1]);
      if (row === undefined) return clientError(404, "no such holdback release");
      if (row.reversed) return clientError(409, "that release has already been reversed");
      row.reversed = true;
      const inv = world.dueInvoices.find((i) => i.invoice_id === row.invoice_id);
      return { status: 200, body: { release_id: row.id, invoice_id: inv.invoice_id, invoice_no: inv.invoice_no, restored_cents: row.amount_cents, reversed_on: dayFrom(0) } };
    }
    return null;
  }

  /** The lien doors (core/internal/lien/release.go): the caller and the job first, then the days, in the core's order. */
  function handleDoors(method, path, body) {
    const m = path.match(/^\/api\/v1\/lien\/jobs\/([^/]+)\/(preserved-lien\/discharge|preserved-lien|preservation-dates)$/);
    if (m === null) return null;
    const door = m[2];
    if ((door === "preservation-dates") !== (method === "PUT")) return null;
    if (!UUID.test(m[1])) return internal();
    const job = world.jobs.find((j) => j.id === m[1]);
    if (job === undefined) return clientError(404, "lien: job not found");
    const day = (what, v) => {
      const t = String(v ?? "").trim();
      if (t === "") return { error: clientError(422, `lien: ${what} is required (a date, YYYY-MM-DD)`) };
      if (!/^\d{4}-\d{2}-\d{2}$/.test(t) || Number.isNaN(Date.parse(`${t}T00:00:00Z`)) || new Date(`${t}T00:00:00Z`).toISOString().slice(0, 10) !== t) return { error: clientError(422, `lien: ${what} ${JSON.stringify(t)} is not a date (YYYY-MM-DD)`) };
      return { day: t };
    };
    if (door === "preserved-lien") {
      const d = day("preserved_on", body.preserved_on);
      if (d.error) return d.error;
      job.lien_preserved_on = d.day;
      const note = String(body.note ?? "").trim();
      if (note === "") delete job.lien_preserved_note;
      else job.lien_preserved_note = note;
      delete job.lien_discharged_on;
      return { status: 200, body: job };
    }
    if (door === "preserved-lien/discharge") {
      if (job.lien_preserved_on === undefined) return clientError(422, `lien: no lien was preserved on job ${job.job_no}, so there is nothing to discharge`);
      const d = day("discharged_on", body.discharged_on);
      if (d.error) return d.error;
      if (d.day < job.lien_preserved_on) return clientError(422, `lien: a lien preserved on ${job.lien_preserved_on} cannot be discharged on ${d.day}, before it`);
      job.lien_discharged_on = d.day;
      return { status: 200, body: job };
    }
    const names = ["csp_published_on", "substantial_perf_date", "last_supply_date"];
    const named = names.filter((n) => Object.prototype.hasOwnProperty.call(body, n));
    if (named.length === 0) return clientError(422, "lien: name at least one of csp_published_on, substantial_perf_date and last_supply_date (a date, YYYY-MM-DD, or null to clear it)");
    const next = {};
    for (const n of named) {
      const raw = body[n];
      if (raw === null || String(raw).trim() === "") {
        next[n] = "";
        continue;
      }
      const d = day(n, raw);
      if (d.error) return d.error;
      next[n] = d.day;
    }
    for (const [n, v] of Object.entries(next)) {
      if (v === "") delete job[n];
      else job[n] = v;
    }
    return { status: 200, body: job };
  }

  function registerDeadline(jobId, dueDate) {
    const same = world.deadlines.find((d) => d.job_id === jobId && d.deadline_kind === "HOLDBACK_RELEASE" && d.due_date === dueDate);
    if (same !== undefined) return;
    world.deadlines.push({ id: uuid("c1", world.nextDeadline++), job_id: jobId, deadline_kind: "HOLDBACK_RELEASE", due_date: dueDate, rule_days: RULE_DAYS, status: "OPEN" });
  }

  /**
   * Answer one request or return null when it is not this world's. The shape is { status, body }, the body already
   * a JSON value.
   */
  function handle(req, url, bodyText) {
    const path = url.pathname;
    const method = req.method;
    const isLien = path.startsWith("/api/v1/lien/");
    const isCompliance = path.startsWith("/api/v1/compliance/");
    const isRoster = path === "/api/v1/staff";
    const isHoldback = path.startsWith("/api/v1/ar/holdback/");
    if (!isLien && !isCompliance && !isRoster && !isHoldback) return null;
    const caller = callerOf(req);
    // The route guards: the staff list is the dealer admin's read (a tier 1 role), every other route is RequireManager.
    if (isRoster) {
      if (method !== "GET") return null;
      if (caller.tier > 1) return forbidden();
      return page(world.roster);
    }
    if (caller.tier > 2) return forbidden();
    // The role gate beside the tier guard: the default role configuration gives payables_receivables work on
    // money-desk and manager-home, and the census puts every lien write and the review status write at manage.
    // Reads pass; every write, the release included, answers the gate's shared body.
    if (method !== "GET" && caller.role === "PAYABLES_RECEIVABLES") return forbidden();
    let body = {};
    try {
      body = bodyText === "" ? {} : JSON.parse(bodyText);
    } catch {
      return clientError(400, "invalid_json");
    }
    const q = url.searchParams;
    if (isHoldback) return handleHoldback(method, path, caller, body, q);
    const door = isLien ? handleDoors(method, path, body) : null;
    if (door !== null) return door;

    // ---- lien ------------------------------------------------------------------------------------------
    if (method === "GET" && path === "/api/v1/lien/jobs") {
      const branch = q.get("branch_id") ?? "";
      if (branch !== "" && !UUID.test(branch)) return internal();
      return page(world.jobs.filter((j) => branch === "" || j.branch_id === branch));
    }
    if (method === "POST" && path === "/api/v1/lien/jobs") {
      // The service's checks, in the core's order, then the insert.
      if (String(body.job_no ?? "").trim() === "") return clientError(422, "lien: job_no is required");
      if (String(body.branch_id ?? "").trim() === "") return clientError(422, "lien: branch_id is required");
      const direction = body.holdback_direction === undefined || body.holdback_direction === "" ? "RECEIVABLE" : body.holdback_direction;
      if (direction !== "RECEIVABLE" && direction !== "PAYABLE") return clientError(422, "lien: holdback_direction must be RECEIVABLE or PAYABLE");
      if (!UUID.test(body.branch_id) || (body.customer_id ?? "") !== "" && !UUID.test(body.customer_id)) return internal();
      const validDay = (s) => typeof s === "string" && /^\d{4}-\d{2}-\d{2}$/.test(s) && !Number.isNaN(Date.parse(`${s}T00:00:00Z`));
      const perf = validDay(body.substantial_perf_date) ? body.substantial_perf_date : "";
      const supply = validDay(body.last_supply_date) ? body.last_supply_date : "";
      const created = {
        id: uuid("10b0", world.nextJob++),
        job_no: String(body.job_no),
        ...((body.customer_id ?? "") === "" ? {} : { customer_id: body.customer_id }),
        branch_id: body.branch_id,
        description: String(body.description ?? ""),
        contract_cents: Number(body.contract_cents ?? 0),
        holdback_pct_bps: Number(body.holdback_pct_bps ?? 0) === 0 ? 1000 : Number(body.holdback_pct_bps),
        ...(perf === "" ? {} : { substantial_perf_date: perf }),
        ...(supply === "" ? {} : { last_supply_date: supply }),
        status: "OPEN",
        holdback_direction: direction,
        hst_on_holdback: body.hst_on_holdback === true,
        created_at: new Date(Date.now() + world.tick++).toISOString(),
      };
      world.jobs.unshift(created);
      if (perf !== "") registerDeadline(created.id, dayFrom(RULE_DAYS, Date.parse(`${perf}T00:00:00Z`)));
      return { status: 201, body: created };
    }
    let m = path.match(/^\/api\/v1\/lien\/jobs\/([^/]+)$/);
    if (method === "GET" && m) {
      if (!UUID.test(m[1])) return internal();
      const found = world.jobs.find((j) => j.id === m[1]);
      return found === undefined ? clientError(404, "lien: job not found") : { status: 200, body: found };
    }
    m = path.match(/^\/api\/v1\/lien\/jobs\/([^/]+)\/sync$/);
    if (method === "POST" && m) {
      if (!UUID.test(m[1])) return internal();
      const found = world.jobs.find((j) => j.id === m[1]);
      if (found === undefined) return clientError(404, "lien: job not found");
      const result = { job_id: found.id, booked_count: 0, skipped_count: 0, booked_cents: 0 };
      const booked = new Set(world.entries.filter((e) => e.job_id === found.id && e.entry_type === "RETENTION" && e.invoice_id !== "").map((e) => e.invoice_id));
      for (const inv of world.invoices.filter((i) => i.branch_id === found.branch_id)) {
        // A job with a customer sweeps that customer's invoices; a job with none sweeps its whole branch.
        if ((found.customer_id ?? "") !== "" && inv.customer_id !== "" && inv.customer_id !== found.customer_id) continue;
        if (booked.has(inv.invoice_id)) {
          result.skipped_count += 1;
          continue;
        }
        if (inv.holdback_cents <= 0) {
          result.skipped_count += 1;
          continue;
        }
        world.entries.push({ job_id: found.id, invoice_id: inv.invoice_id, entry_type: "RETENTION", amount_cents: inv.holdback_cents, created_at: new Date(Date.now() + world.tick++).toISOString(), reason: `synced from invoice ${inv.invoice_no}` });
        booked.add(inv.invoice_id);
        result.booked_count += 1;
        result.booked_cents += inv.holdback_cents;
      }
      return { status: 200, body: result };
    }
    m = path.match(/^\/api\/v1\/lien\/jobs\/([^/]+)\/documents$/);
    if (method === "GET" && m) {
      if (!UUID.test(m[1])) return internal();
      // The latest version of each form, ordered by form.
      const latest = new Map();
      for (const d of world.documents.filter((x) => x.job_id === m[1])) {
        const held = latest.get(d.template_kind);
        if (held === undefined || d.version > held.version) latest.set(d.template_kind, d);
      }
      return page([...latest.values()].sort((a, b) => (a.template_kind < b.template_kind ? -1 : 1)).map(docView));
    }
    if (method === "GET" && path === "/api/v1/lien/triage") {
      const branch = q.get("branch_id") ?? "";
      if (branch !== "" && !UUID.test(branch)) return internal();
      return { status: 200, body: { items: triageRows(branch) } };
    }
    if (method === "GET" && path === "/api/v1/lien/holdback/aging") {
      const branch = q.get("branch_id") ?? "";
      if (branch !== "" && !UUID.test(branch)) return internal();
      return page(agingRows(branch));
    }
    if (method === "POST" && path === "/api/v1/lien/documents") {
      if (String(body.job_id ?? "").trim() === "") return clientError(422, "lien: job_id is required");
      if (TEMPLATES[body.template_kind] === undefined) return clientError(422, `lien: unknown template_kind ${JSON.stringify(String(body.template_kind ?? ""))}`);
      if (!UUID.test(body.job_id)) return internal();
      if (world.jobs.find((j) => j.id === body.job_id) === undefined) return clientError(404, "lien: job not found");
      const version = world.documents.filter((d) => d.job_id === body.job_id && d.template_kind === body.template_kind).reduce((n, d) => Math.max(n, d.version), 0) + 1;
      const data = body.data !== null && typeof body.data === "object" ? body.data : {};
      const doc = {
        id: uuid("d0c0", world.nextDoc++),
        job_id: body.job_id,
        template_kind: body.template_kind,
        status: "DRAFT",
        version,
        rendered_content: render(body.template_kind, version, data),
        data,
        is_draft_banner: true,
        created_at: new Date(Date.now() + world.tick++).toISOString(),
      };
      world.documents.push(doc);
      return { status: 201, body: doc };
    }
    m = path.match(/^\/api\/v1\/lien\/documents\/([^/]+)\/versions$/);
    if (method === "GET" && m) {
      if (!UUID.test(m[1])) return internal();
      const doc = world.documents.find((d) => d.id === m[1]);
      if (doc === undefined) return clientError(404, "lien: document not found");
      return page(world.documents.filter((d) => d.job_id === doc.job_id && d.template_kind === doc.template_kind).sort((a, b) => b.version - a.version).map(docView));
    }
    m = path.match(/^\/api\/v1\/lien\/holdback\/([^/]+)\/release$/);
    if (method === "POST" && m) {
      // The dual gate, in the core's order. The co-signer's authority is resolved here from the roster, never read
      // from the body's claimed role.
      if (caller.role !== "GENERAL_MANAGER") return clientError(403, "lien: holdback release gatekeeper must be GENERAL_MANAGER");
      const approver = String(body.approver_id ?? "");
      if (approver === "" || approver === caller.id) return clientError(403, "lien: four-eyes, the co-signer must differ from the gatekeeper");
      const cosigner = world.roster.find((p) => p.user_id === approver);
      if (cosigner === undefined) return clientError(403, "lien: holdback release co-signer could not be verified");
      if (world.inactive.includes(approver)) return clientError(403, "lien: holdback release co-signer must be an active staff member");
      if (cosigner.role_code !== "FINANCIAL_CONTROLLER" && cosigner.role_code !== "PAYABLES_RECEIVABLES") {
        return clientError(403, "lien: holdback release co-signer must be FINANCIAL_CONTROLLER or PAYABLES_RECEIVABLES");
      }
      if (!UUID.test(m[1])) return internal();
      const found = world.jobs.find((j) => j.id === m[1]);
      if (found === undefined) return clientError(404, "lien: job not found");
      if (found.holdback_direction === "PAYABLE") return clientError(422, "lien: PAYABLE holdback release is manual, recorded, not posted (register a calendar deadline)");
      if (found.customer_job_id !== undefined) {
        return clientError(422, `lien: job ${found.job_no} is linked to a customer job, so its holdback is released by the Controller's holdback release (POST /api/v1/ar/holdback/releases), once its preservation period has run out, and not by this job level release`);
      }
      const left = outstanding(found.id);
      if (left <= 0) return clientError(422, "lien: no outstanding retention to release for this job");
      const journal = uuid("7a", world.nextJournal++);
      const released = dayFrom(0);
      world.entries.push({ job_id: found.id, invoice_id: "", entry_type: "RELEASE", amount_cents: left, created_at: new Date(Date.now() + world.tick++).toISOString(), gl_journal_id: journal, released_at: released });
      return { status: 200, body: { job_id: found.id, amount_cents: left, gl_journal_id: journal, released_at: released, approved_by: approver, gatekeeper_by: caller.id } };
    }

    // ---- compliance ------------------------------------------------------------------------------------
    if (method === "GET" && path === "/api/v1/compliance/calendar") {
      const status = q.get("status") ?? "";
      return page(world.deadlines.filter((d) => status === "" || d.status === status).sort((a, b) => (a.due_date < b.due_date ? -1 : a.due_date > b.due_date ? 1 : 0)));
    }
    if (method === "GET" && path === "/api/v1/compliance/upcoming") {
      const asked = Number.parseInt(q.get("horizon_days") ?? "", 10);
      const horizon = Number.isNaN(asked) || asked <= 0 ? RULE_DAYS : asked;
      const today = todayStart();
      const cutoff = today + horizon * DAY;
      const rows = world.deadlines
        .filter((d) => d.status === "OPEN")
        .filter((d) => Date.parse(`${d.due_date}T00:00:00Z`) <= cutoff)
        .map((d) => (Date.parse(`${d.due_date}T00:00:00Z`) < today ? { ...d, status: "OVERDUE" } : d))
        .sort((a, b) => (a.due_date < b.due_date ? -1 : a.due_date > b.due_date ? 1 : 0));
      return page(rows);
    }
    if (method === "GET" && path === "/api/v1/compliance/review-status") {
      const subjectId = (q.get("subject_id") ?? "").trim();
      if (subjectId === "") return clientError(400, "compliance: subject_id is required");
      if (!UUID.test(subjectId)) return internal();
      const type = q.get("subject_type") ?? "";
      const held = world.reviews.filter((r) => (type === "" || r.subject_type === type) && r.subject_id === subjectId).sort((a, b) => b.at - a.at)[0];
      if (held === undefined) return { status: 200, body: { subject_type: type, subject_id: subjectId, state: "DRAFT", banner_text: DEFAULT_BANNER, is_draft: true } };
      const { at, ...wire } = held;
      return { status: 200, body: { ...wire, is_draft: held.state === "DRAFT" || held.state === "PENDING" } };
    }
    if (method === "PUT" && path === "/api/v1/compliance/review-status") {
      if (String(body.subject_id ?? "").trim() === "") return clientError(400, "compliance: subject_id is required");
      const state = body.state === undefined || body.state === "" ? "DRAFT" : body.state;
      if (!["DRAFT", "PENDING", "APPROVED", "REJECTED"].includes(state)) return clientError(422, `compliance: invalid review state ${JSON.stringify(String(body.state))}`);
      const banner = String(body.banner_text ?? "").trim() === "" ? DEFAULT_BANNER : String(body.banner_text).trim();
      if (!UUID.test(body.subject_id)) return internal();
      const row = { subject_type: body.subject_type ?? "", subject_id: body.subject_id, state, banner_text: banner, reviewed_by: caller.id, reviewed_at: new Date(Date.now() + world.tick++).toISOString(), at: world.tick };
      const at = world.reviews.findIndex((r) => r.subject_type === row.subject_type && r.subject_id === row.subject_id);
      if (at >= 0) world.reviews[at] = row;
      else world.reviews.push(row);
      const { at: _at, ...wire } = row;
      return { status: 200, body: { ...wire, is_draft: state === "DRAFT" || state === "PENDING" } };
    }
    return null;
  }

  return { handle, reset };
}
