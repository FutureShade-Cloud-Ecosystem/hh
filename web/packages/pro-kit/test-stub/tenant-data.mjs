// The shared tenant stub's world: Summit Ridge Builders buying from Gable
// Landscape Supply (GLS-TENANT-r3), invented data only. Every figure is integer
// cents the way the core returns it; tax is the San Diego County combined rate
// of 7.75 percent on taxable lines, rounded half up on the whole subtotal. Role
// labels stand in for people; the addresses use the reserved demo domain.
//
// Timestamps are computed from the clock when a world is created (days before
// or after now), so nothing here is a calendar date and nothing goes stale.
//
// createWorld() returns a fresh, mutable world; the stub resets by building a
// new one.

export const TAX_BASIS_POINTS = 775; // 7.75 percent

/** Half up tax on an integer subtotal, the way the core rounds money. */
export function taxOn(subtotalCents) {
  return Math.floor((subtotalCents * TAX_BASIS_POINTS + 5000) / 10000);
}

const DAY = 86_400_000;
const pad = (n, width = 12) => String(n).padStart(width, "0");
export const uid = (kind, n) => `${kind}0000-0000-4000-8000-${pad(n)}`;

export const CUSTOMER_ID = uid("c5", 1);
export const BRANCH_ID = uid("b7", 1);

/** The tenant dealer's public config: the one thing served before sign in. */
export const DEALER_CONFIG = {
  id: "gable-landscape-supply",
  dealer_name: "Gable Landscape Supply",
  logo_url: "",
  primary_color: "#FFC313",
  support_email: "pro.support@hh-demo.invalid",
  support_phone: "(619) 555-0142",
};

/** Sign in is not a core route (the tenant login routes are unregistered); this table is the stub's own. */
export const STUB_PASSWORD = "stub-password";

export const PERSONAS = {
  owner: { n: 1, role: "account_admin", name: "Contractor owner", email: "contractor.owner@hh-demo.invalid" },
  buyer: { n: 2, role: "buyer", name: "Contractor buyer", email: "contractor.buyer@hh-demo.invalid" },
  pm: { n: 3, role: "buyer_no_pay", name: "Contractor project manager", email: "contractor.pm@hh-demo.invalid" },
  ap: { n: 4, role: "ap_finance", name: "Contractor accounts payable", email: "contractor.ap@hh-demo.invalid" },
  field: { n: 5, role: "field_crew", name: "Contractor field crew", email: "contractor.field@hh-demo.invalid" },
};

export const CAPABILITY_KEYS = [
  "manage_users", "view_billing", "pay_invoices", "manage_payment_methods", "submit_rfq",
  "create_orders", "view_orders_deliveries", "edit_delivery_instructions", "manage_files",
];

// The core's matrix (core/internal/portal/rbac.go), spelled out for the stub's own gate.
const ROLE_CAPS = {
  account_admin: CAPABILITY_KEYS,
  buyer: ["view_billing", "pay_invoices", "manage_payment_methods", "submit_rfq", "create_orders", "view_orders_deliveries", "edit_delivery_instructions", "manage_files"],
  buyer_no_pay: ["view_billing", "submit_rfq", "create_orders", "view_orders_deliveries", "edit_delivery_instructions", "manage_files"],
  ap_finance: ["view_billing", "pay_invoices", "manage_payment_methods", "view_orders_deliveries", "manage_files"],
  field_crew: ["view_orders_deliveries", "edit_delivery_instructions"],
};

export function capabilitiesOf(role) {
  const held = ROLE_CAPS[role] ?? [];
  return Object.fromEntries(CAPABILITY_KEYS.map((key) => [key, held.includes(key)]));
}

export const PRODUCTS = [
  { n: 1, sku: "CPV-1204", name: "Sandstone paver 6 x 9", category: "Pavers", base_uom: "SF", manufacturer: "Copperline Pavers", unit: 512 },
  { n: 2, sku: "CPV-2210", name: "Slate gray paver 12 x 12", category: "Pavers", base_uom: "SF", manufacturer: "Copperline Pavers", unit: 689 },
  { n: 3, sku: "GRQ-4410", name: "Irregular flagstone, buff", category: "Natural stone", base_uom: "SF", manufacturer: "Granite Run Quarry", unit: 845 },
  { n: 4, sku: "HFS-3305", name: "Harbor wall block", category: "Wall block", base_uom: "EA", manufacturer: "Heronfield Stone", unit: 695 },
  { n: 5, sku: "HFS-3390", name: "Harbor wall coping", category: "Wall block", base_uom: "EA", manufacturer: "Heronfield Stone", unit: 1185 },
  { n: 6, sku: "PBS-0710", name: "Planting soil blend", category: "Soils and mulch", base_uom: "CY", manufacturer: "Prairie Bend Soils", unit: 4200 },
  { n: 7, sku: "PBS-0720", name: "Shredded cedar mulch", category: "Soils and mulch", base_uom: "CY", manufacturer: "Prairie Bend Soils", unit: 3800 },
  { n: 8, sku: "GRQ-0105", name: "Decomposed granite, gold", category: "Aggregates", base_uom: "TON", manufacturer: "Granite Run Quarry", unit: 5600 },
  { n: 9, sku: "EDG-0501", name: "Steel edging, 4 in", category: "Edging", base_uom: "LF", manufacturer: "Heronfield Stone", unit: 395 },
  { n: 10, sku: "DRN-0620", name: "Perforated drain pipe, 4 in", category: "Drainage", base_uom: "LF", manufacturer: "Copperline Pavers", unit: 215 },
  { n: 11, sku: "PSD-0901", name: "Polymeric sand, 50 lb bag", category: "Joint sand", base_uom: "BG", manufacturer: "Copperline Pavers", unit: 2850 },
  { n: 12, sku: "SLR-0950", name: "Paver sealer, 5 gallon", category: "Sealers", base_uom: "EA", manufacturer: "Copperline Pavers", unit: 14900 },
].map((p) => ({ ...p, id: uid("a1", p.n) }));

const productById = (id) => PRODUCTS.find((p) => p.id === id);

/** Line total in cents from a decimal quantity string and an integer unit price, half up. */
export function lineTotal(qty, unitCents) {
  const [whole, frac = ""] = String(qty).split(".");
  const scale = 10 ** frac.length;
  const scaled = Number(whole) * scale + Number(frac || 0);
  return Math.floor((scaled * unitCents + Math.floor(scale / 2)) / scale);
}

export function createWorld(now = Date.now()) {
  const at = (days) => new Date(now + days * DAY).toISOString();
  const day = (days) => at(days).slice(0, 10);
  const w = {
    now,
    at,
    day,
    counters: { project: 100, line: 500, payment: 9000, method: 100, order: 30500, quote: 7000, address: 100 },
  };

  // ---- the team: one person per stored role --------------------------------------------------
  w.users = Object.values(PERSONAS).map((p) => ({
    id: uid("e4", p.n),
    customer_id: CUSTOMER_ID,
    email: p.email,
    name: p.name,
    role: p.role,
    status: "active",
    notification_prefs: {},
    created_at: at(-120),
    updated_at: at(-30),
  }));
  w.invites = [];

  // ---- the account ----------------------------------------------------------------------------
  w.account = {
    customer_id: CUSTOMER_ID,
    account_no: "SRB-1001",
    name: "Summit Ridge Builders",
    customer_type: "CONTRACTOR",
    branch_id: BRANCH_ID,
    payment_terms_days: 30,
    credit_limit_cents: 7_500_000,
    on_hold: false,
    tax_exempt: false,
    balance_cents: 0,
    is_active: true,
  };

  // ---- delivery sites and job rollups ---------------------------------------------------------
  w.addresses = [
    { id: uid("d3", 1), kind: "billing", label: "Summit Ridge office", line1: "410 Cobble Way", line2: "Suite 6", city: "San Diego", province: "CA", postal: "92110", last_used_at: null },
    { id: uid("d3", 2), kind: "job_site", label: "La Jolla job", line1: "88 Terrace Rise", line2: "", city: "La Jolla", province: "CA", postal: "92037", last_used_at: at(-9) },
    { id: uid("d3", 3), kind: "job_site", label: "Santa Fe job", line1: "1520 Mesa Court", line2: "", city: "Solana Beach", province: "CA", postal: "92075", last_used_at: at(-3) },
    { id: uid("d3", 4), kind: "delivery", label: "Point Loma job", line1: "3305 Lantern Street", line2: "", city: "San Diego", province: "CA", postal: "92106", last_used_at: at(-1) },
  ];
  w.jobs = [
    { id: uid("f2", 1), name: "La Jolla terrace", job_address: "88 Terrace Rise, La Jolla, CA 92037", created_at: at(-60) },
    { id: uid("f2", 2), name: "Santa Fe courtyard", job_address: "1520 Mesa Court, Solana Beach, CA 92075", created_at: at(-40) },
    { id: uid("f2", 3), name: "Point Loma retaining wall", job_address: "3305 Lantern Street, San Diego, CA 92106", created_at: at(-20) },
  ];

  // ---- procurement projects (the planning board) ----------------------------------------------
  const line = (project, lineNo, productN, qty, uom, notes = "") => {
    const p = PRODUCTS[productN - 1];
    const total = lineTotal(qty, p.unit);
    return {
      id: uid("9c", w.counters.line++),
      project_id: project,
      line_no: lineNo,
      product_id: p.id,
      sku: p.sku,
      description: p.name,
      qty_entered: qty,
      uom,
      notes,
      indicative_unit_price_cents: p.unit,
      indicative_line_total_cents: total,
      priced: true,
    };
  };
  const mk = (n, name, status, lines, createdDays) => ({
    id: uid("9a", n),
    customer_id: CUSTOMER_ID,
    name,
    status,
    created_at: at(createdDays),
    updated_at: at(createdDays + 2),
    lines,
  });
  w.projects = [
    mk(1, "La Jolla terrace pavers", "PLANNING", [line(uid("9a", 1), 1, 1, "850", "SF"), line(uid("9a", 1), 2, 11, "18", "BG", "Two coats on the joints"), line(uid("9a", 1), 3, 12, "3", "EA")], -14),
    mk(2, "Santa Fe courtyard stone", "QUOTING", [line(uid("9a", 2), 1, 3, "620", "SF"), line(uid("9a", 2), 2, 8, "6.5", "TON")], -9),
    mk(3, "Point Loma retaining wall", "ORDERED", [line(uid("9a", 3), 1, 4, "420", "EA"), line(uid("9a", 3), 2, 5, "60", "EA"), line(uid("9a", 3), 3, 10, "80", "LF")], -20),
    mk(4, "Backyard planting beds", "PLANNING", [], -2),
  ];

  // ---- quotes ---------------------------------------------------------------------------------
  const qline = (lineNo, productN, qty, uom) => {
    const p = PRODUCTS[productN - 1];
    return { line_no: lineNo, product_id: p.id, description: p.name, qty_entered: qty, uom_entered: uom, unit_price_cents: p.unit, line_total_cents: lineTotal(qty, p.unit) };
  };
  const mq = (n, no, status, validDays, lines) => {
    const subtotal = lines.reduce((s, l) => s + l.line_total_cents, 0);
    const tax = taxOn(subtotal);
    return { id: uid("7e", n), quote_no: no, customer_id: CUSTOMER_ID, status, valid_until: day(validDays), subtotal_cents: subtotal, tax_cents: tax, total_cents: subtotal + tax, created_at: at(-8 - n), lines };
  };
  w.quotes = [
    mq(1, "Q-7001", "SENT", 12, [qline(1, 3, "620", "SF"), qline(2, 8, "6.5", "TON")]),
    mq(2, "Q-7002", "ACCEPTED", 6, [qline(1, 6, "24", "CY"), qline(2, 7, "18", "CY")]),
    mq(3, "Q-7003", "DRAFT", 20, [qline(1, 9, "240", "LF")]),
    mq(4, "Q-6990", "EXPIRED", -4, [qline(1, 2, "300", "SF")]),
  ];

  // ---- orders ---------------------------------------------------------------------------------
  const oline = (lineNo, productN, qty, uom, filled) => {
    const p = PRODUCTS[productN - 1];
    return {
      line_no: lineNo,
      product_id: p.id,
      description: p.name,
      qty_entered: qty,
      uom_entered: uom,
      qty_base: qty,
      allocated_base: filled === "none" ? "0" : qty,
      fulfilled_base: filled === "all" ? qty : "0",
      unit_price_cents: p.unit,
      line_total_cents: lineTotal(qty, p.unit),
    };
  };
  const mo = (n, status, fulfillment, createdDays, lines, extra = {}) => {
    const subtotal = lines.reduce((s, l) => s + l.line_total_cents, 0);
    const tax = taxOn(subtotal);
    return {
      id: uid("6d", n),
      order_no: `SO-${w.counters.order + n}`,
      customer_id: CUSTOMER_ID,
      quote_id: extra.quote_id ?? uid("7e", 90 + n),
      status,
      fulfillment,
      requested_date: day(createdDays + 4),
      subtotal_cents: subtotal,
      tax_cents: tax,
      total_cents: subtotal + tax,
      created_at: at(createdDays),
      has_backorder: extra.has_backorder ?? false,
      delivery_instructions: extra.delivery_instructions ?? "",
      project_job: extra.job ?? 1,
      lines,
    };
  };
  w.orders = [
    mo(1, "INVOICED", "DELIVERY", -30, [oline(1, 4, "420", "EA", "all"), oline(2, 5, "60", "EA", "all")], { job: 3, delivery_instructions: "Gate code at the lockbox. Stack on the east side." }),
    mo(2, "FULFILLED", "DELIVERY", -18, [oline(1, 1, "500", "SF", "all"), oline(2, 11, "10", "BG", "all")], { job: 1 }),
    mo(3, "PARTIALLY_FULFILLED", "DELIVERY", -9, [oline(1, 3, "400", "SF", "all"), oline(2, 8, "4", "TON", "none")], { job: 2, has_backorder: true, delivery_instructions: "Call the site lead 30 minutes before arrival." }),
    mo(4, "READY", "WILL_CALL", -4, [oline(1, 9, "160", "LF", "all"), oline(2, 10, "80", "LF", "all")], { job: 3 }),
    mo(5, "PICKING", "DELIVERY", -2, [oline(1, 6, "12", "CY", "none")], { job: 1 }),
    mo(6, "CONFIRMED", "DELIVERY", -1, [oline(1, 12, "3", "EA", "none"), oline(2, 11, "8", "BG", "none")], { job: 1 }),
  ];
  // The order a convert makes is numbered after these.
  w.nextOrderIndex = 7;

  // ---- invoices (one per fulfilled order, plus open ones) -----------------------------------------
  const mi = (n, orderIdx, status, issueDays, paidCents) => {
    const o = w.orders[orderIdx];
    const balance = status === "PAID" ? 0 : o.total_cents - paidCents;
    return {
      id: uid("8b", n),
      invoice_no: `INV-${20400 + n}`,
      customer_id: CUSTOMER_ID,
      order_id: o.id,
      status,
      issue_date: day(issueDays),
      due_date: day(issueDays + 30),
      subtotal_cents: o.subtotal_cents,
      tax_cents: o.tax_cents,
      holdback_cents: 0,
      total_cents: o.total_cents,
      balance_cents: balance,
      lines: o.lines.map((l) => ({ line_no: l.line_no, product_id: l.product_id, description: l.description, qty_entered: l.qty_entered, uom_entered: l.uom_entered, unit_price_cents: l.unit_price_cents, line_total_cents: l.line_total_cents })),
      payment_history: [],
    };
  };
  w.invoices = [
    mi(1, 0, "PAID", -28, 0),
    mi(2, 1, "FINALIZED", -16, 0),
    mi(3, 2, "PARTIALLY_PAID", -8, 150_000),
  ];

  // ---- payments and payment methods ----------------------------------------------------------------
  const pm = (n, type, brand, last4, expM, expY, isDefault) => ({
    id: uid("5f", n),
    customer_id: CUSTOMER_ID,
    gateway: "stub",
    method_type: type,
    brand,
    last4,
    exp_month: expM,
    exp_year: expY,
    is_default: isDefault,
    status: "active",
    created_at: at(-90),
    updated_at: at(-90),
  });
  w.methods = [pm(1, "CARD", "VISA", "4242", 12, new Date(now).getUTCFullYear() + 3, true), pm(2, "BANK", "Harbor Credit Union", "6789", 0, 0, false)];
  w.payments = [
    {
      id: uid("5b", 1),
      payment_no: "PMT-9001",
      customer_id: CUSTOMER_ID,
      method: "CARD",
      amount_cents: w.invoices[0].total_cents,
      status: "RECEIVED",
      gateway_ref: "stub-ref-0001",
      received_at: at(-25),
      allocations: [{ invoice_id: w.invoices[0].id, amount_cents: w.invoices[0].total_cents, created_at: at(-25) }],
    },
    {
      id: uid("5b", 2),
      payment_no: "PMT-9002",
      customer_id: CUSTOMER_ID,
      method: "EFT",
      amount_cents: 150_000,
      status: "DEPOSITED",
      gateway_ref: "stub-ref-0002",
      received_at: at(-6),
      allocations: [{ invoice_id: w.invoices[2].id, amount_cents: 150_000, created_at: at(-6) }],
    },
  ];
  w.invoices[0].payment_history = [{ id: w.payments[0].id, amount_cents: w.payments[0].amount_cents, method: "CARD", status: "DEPOSITED", received_at: at(-25) }];

  w.returnRequests = [];
  w.recompute = () => {
    w.account.balance_cents = w.invoices.filter((i) => ["FINALIZED", "PARTIALLY_PAID"].includes(i.status)).reduce((s, i) => s + i.balance_cents, 0);
  };
  w.recompute();
  return w;
}

export { productById };
