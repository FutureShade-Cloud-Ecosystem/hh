// The acceptance run's world leg: the facts the screens stand on, read from
// the demo image's own core as each rule names it. The margin glance refuses
// the clerk and answers the manager (the box's policy word MANAGER_UP, the
// hardening train's glance now honouring it server-side), the card rail
// answers simulated true, the dealer's approval amount
// and its approvers are the configuration's, the role gate admits the manager
// past the counter's walls and refuses the clerk, the other branch's sale is
// refused at this counter, the seeded card sale carries no card reference, and
// the world's newest rows are dated to the moment the box was rebuilt.
import {
  expect,
  JULIAN_ID,
  SEEDED,
  POINT_LOMA_ID,
  houseDay,
  houseDaysAgo,
  test,
  marginPercent,
  mint,
  openDrawer,
  shot,
  staffGet,
  staffPost,
} from "./helpers.js";

const TEN_PRODUCTS = [
  "DEMO-PAV-CHARCOAL",
  "DEMO-PAV-SANDSTONE",
  "DEMO-PAV-BASALT",
  "DEMO-WALL-BLOCK",
  "DEMO-STEP-SLAB",
  "DEMO-AGG-BEDDING",
  "DEMO-AGG-BASE",
  "DEMO-AGG-SAND",
  "DEMO-MULCH-BLACK",
  "DEMO-EDGE-ALU",
];

// The names the demo dealer, Gable Landscape Supply, sells them under: invented trade names, none a
// real brand and none carrying a marker suffix.
const TEN_NAMES: Record<string, string> = {
  "DEMO-PAV-CHARCOAL": "Interlocking paver 60 mm, charcoal",
  "DEMO-PAV-SANDSTONE": "Dimensional paver, sandstone",
  "DEMO-PAV-BASALT": "Large format paver, basalt",
  "DEMO-WALL-BLOCK": "Retaining wall block 8x18",
  "DEMO-STEP-SLAB": "Step slab 24x48, natural",
  "DEMO-AGG-BEDDING": "Paver bedding stone",
  "DEMO-AGG-BASE": "Class 2 base rock",
  "DEMO-AGG-SAND": "Concrete Sand",
  "DEMO-MULCH-BLACK": "Black cedar mulch",
  "DEMO-EDGE-ALU": "Aluminum edging 8 ft",
};

test("the way in's data: nine flows with role codes and labels", async ({ page }) => {
  const flows = await page.request.get("/api/v1/demo/flows");
  expect(flows.ok()).toBe(true);
  const body = (await flows.json()) as {
    flows: Array<{ flow: string; role_code: string; label: string; class?: string }>;
  };
  // The table also lists the five contractor flows (class "tenant"), which the staff apps' pickers
  // do not show; the staff rows are the nine this leg names.
  expect(body.flows.filter((row) => row.class === "tenant")).toHaveLength(5);
  body.flows = body.flows.filter((row) => row.class !== "tenant");
  expect(body.flows).toHaveLength(9);
  expect(body.flows.map((row) => row.flow)).toEqual([
    "sales",
    "sales-manager",
    "yard-manager",
    "yard-team",
    "driver",
    "payables-receivables",
    "procurement-manager",
    "financial-controller",
    "logistics-manager",
  ]);
  expect(body.flows.map((row) => row.role_code)).toEqual([
    "inside_sales",
    "sales_manager",
    "yard_manager",
    "yard_team",
    "driver",
    "payables_receivables",
    "procurement_manager",
    "financial_controller",
    "logistics_manager",
  ]);
  expect(body.flows.map((row) => row.label)).toEqual([
    "Sales",
    "Sales manager",
    "Yard manager",
    "Yard Team",
    "Driver",
    "Payables and Receivables",
    "Procurement manager",
    "Financial Controller",
    "Logistics Manager",
  ]);
  // The picture is the table itself, as the browser shows the core's answer:
  // the page loads the flows route and waits for its rows to be on the
  // screen (the test's own request never loads a page).
  await page.goto("/api/v1/demo/flows");
  await expect(page.locator("body")).toContainText('"sales-manager"');
  await expect(page.locator("body")).toContainText('"inside_sales"');
  await expect(page.locator("body")).toContainText('"yard-manager"');
  await shot(page, "00-world-flows", test.info());
});

test("the branch the demo login reaches is Point Loma alone", async ({ page }) => {
  const bearer = await mint(page, "sales");
  const answer = await staffGet(page, bearer, "/api/v1/branches");
  expect(answer.status).toBe(200);
  const rows = answer.body as unknown as Array<Record<string, unknown>>;
  expect(rows).toHaveLength(1);
  expect(rows[0].code).toBe("PLM");
  expect(rows[0].id).toBe(POINT_LOMA_ID);
});

test("the catalogue's ten demo products answer under invented trade names", async ({
  page,
}) => {
  const bearer = await mint(page, "sales");
  for (const sku of TEN_PRODUCTS) {
    const answer = await staffGet(page, bearer, `/api/v1/products/counter-search?q=${sku}`);
    expect(answer.status).toBe(200);
    const items = answer.body.items as Array<Record<string, unknown>>;
    expect(items.length).toBeGreaterThanOrEqual(1);
    const hit = items.find((item) => item.sku === sku);
    expect(hit, `${sku} answers the counter search`).toBeTruthy();
    expect(hit?.name).toBe(TEN_NAMES[sku]);
  }
  // The naming rule the page states: every DEMO coded hit is an invented trade name, none
  // carrying a marker suffix.
  const all = await staffGet(page, bearer, "/api/v1/products/counter-search?q=DEMO-");
  const items = all.body.items as Array<Record<string, unknown>>;
  expect(items.length).toBeGreaterThanOrEqual(TEN_PRODUCTS.length);
  for (const item of items) {
    if (String(item.sku).startsWith("DEMO-")) {
      expect(String(item.name).includes("(demo")).toBe(false);
    }
  }
});

test("the account picker answers the branch's demo accounts under invented names", async ({
  page,
}) => {
  const bearer = await mint(page, "sales");
  const answer = await staffGet(
    page,
    bearer,
    `/api/v1/customers/search?q=demo&branch_id=${POINT_LOMA_ID}`,
  );
  expect(answer.status).toBe(200);
  const rows = answer.body as unknown as Array<Record<string, unknown>>;
  expect(rows.length).toBeGreaterThan(0);
  // The seed's Point Loma accounts answer the picker, under their invented names;
  // the naming rule holds for every DEMO coded account row the picker
  // returns (migration planted rows beside them are no part of the nine).
  const accounts = rows.filter((row) => String(row.account_no).startsWith("DEMO-C"));
  expect(accounts.length).toBeGreaterThanOrEqual(4);
  for (const row of accounts) {
    // The retail walk-up accounts read "Homeowner A" and so on: a role label, never a person.
    expect(String(row.name).includes("(demo")).toBe(false);
  }
  for (const accountNo of ["DEMO-C02", "DEMO-C03", "DEMO-C05", "DEMO-C07"]) {
    expect(rows.some((row) => row.account_no === accountNo)).toBe(true);
  }
});

test("the clerk's margin glance is refused, the manager's answers: the policy word names who sees cost and margin", async ({
  page,
}) => {
  const bearer = await mint(page, "sales");
  const charcoal = await staffGet(page, bearer, "/api/v1/products/counter-search?q=DEMO-PAV-CHARCOAL");
  const productId = (charcoal.body.items as Array<Record<string, unknown>>)[0].product_id as string;
  const priced = await staffPost(page, bearer, "/api/v1/pos/counter-prices", {
    branch_id: POINT_LOMA_ID,
    customer_id: "",
    items: [{ product_id: productId, qty_entered: 10, uom_entered: "PC" }],
  });
  expect(priced.status).toBe(200);
  const customerType = priced.body.customer_type as string;
  // The box's policy word is MANAGER_UP and the hardening train made the
  // glance honour it server-side (row 651): the clerk's answer is the zero
  // document, every margin figure byte-absent, never an empty lines array
  // that could read as "priced but marginless".
  const glanced = await staffPost(page, bearer, "/api/v1/pos/margin-glance", {
    branch_id: POINT_LOMA_ID,
    customer_id: "",
    customer_type: customerType,
    lines: [{ product_id: productId, qty_entered: 10, uom_entered: "PC" }],
  });
  expect(glanced.status).toBe(200);
  expect(glanced.body).toEqual({});
  // The manager is the policy word's named reader: the same glance answers
  // the line, and the rendered column shows the same words the glance gave.
  const manager = await mint(page, "sales-manager");
  const managerGlance = await staffPost(page, manager, "/api/v1/pos/margin-glance", {
    branch_id: POINT_LOMA_ID,
    customer_id: "",
    customer_type: customerType,
    lines: [{ product_id: productId, qty_entered: 10, uom_entered: "PC" }],
  });
  expect(managerGlance.status).toBe(200);
  const lines = managerGlance.body.lines as Array<{ margin_bps?: number }>;
  expect(Array.isArray(lines)).toBe(true);
  expect(lines).toHaveLength(1);
  expect(typeof lines[0].margin_bps).toBe("number");
  expect(marginPercent(lines[0].margin_bps as number)).toMatch(/%$/);
});

test("the walls the box holds: the role matrix, and the price rule write beside it", async ({
  page,
}) => {
  const manager = await mint(page, "sales-manager");
  const clerk = await mint(page, "sales");
  // The role matrix holds on the demo box (the gate's attach landed on the
  // goal, a884020): a dispatch read the manager's configuration admits is
  // refused to the clerk, before anything else answers.
  const clerkDispatch = await staffGet(
    page,
    clerk,
    `/api/v1/dispatch/trucks?branch_id=${POINT_LOMA_ID}`,
  );
  expect(clerkDispatch.status).toBe(403);
  const managerDispatch = await staffGet(
    page,
    manager,
    `/api/v1/dispatch/trucks?branch_id=${POINT_LOMA_ID}`,
  );
  expect(managerDispatch.status).toBe(200);
  // A price rule write: refused to the clerk, and the manager's empty body is
  // refused by the route's own validation (400), which is past the gate, and
  // writes nothing.
  const clerkWrite = await staffPost(page, clerk, "/api/v1/pricing/rules", {});
  expect(clerkWrite.status).toBe(403);
  const managerWrite = await staffPost(page, manager, "/api/v1/pricing/rules", {});
  expect([400, 422]).toContain(managerWrite.status);
});

test("the card rail answers simulated true, the demo's own word", async ({ page }) => {
  const bearer = await mint(page, "sales");
  const answer = await staffGet(page, bearer, "/api/v1/payments/config");
  expect(answer.status).toBe(200);
  expect(answer.body.simulated).toBe(true);
});

test("the seeded card sale carries no card reference, so the desk cannot refund it by card", async ({
  page,
}) => {
  const bearer = await mint(page, "sales");
  const pointLomaCardSale = "DEMO-PLM-S03";
  const lookup = await staffGet(
    page,
    bearer,
    `/api/v1/pos/returns/lookup?order_no=${encodeURIComponent(pointLomaCardSale)}`,
  );
  expect(lookup.status).toBe(200);
  expect(lookup.body.sale_tender).toBe("CARD");
  expect(lookup.body.card_refundable).toBe(false);
});

test("a Julian sale is refused at this counter: the lookup, its ticket, and all three refund rails", async ({
  page,
}) => {
  const bearer = await mint(page, "sales");
  const lookup = await staffGet(
    page,
    bearer,
    `/api/v1/pos/returns/lookup?order_no=${encodeURIComponent(SEEDED.julianSale)}`,
  );
  expect(lookup.status).toBe(403);
  expect(String(lookup.body.error)).toContain("not reachable");
  // The branch's parked list answers Point Loma's ticket and never Julian's;
  // the served core answers the list as the bare array its page shape wraps.
  const parked = await staffGet(page, bearer, `/api/v1/pos/parked?branch_id=${POINT_LOMA_ID}`);
  expect(parked.status).toBe(200);
  const rawItems = Array.isArray(parked.body) ? parked.body : parked.body.items;
  const items = rawItems as Array<{ ticket_no: string; branch_id: string }>;
  expect(items.some((row) => row.ticket_no === SEEDED.pointLomaTicket)).toBe(true);
  expect(items.some((row) => row.ticket_no === SEEDED.julianTicket)).toBe(false);
  for (const row of items) {
    expect(row.branch_id).toBe(POINT_LOMA_ID);
  }
  // The other branch's id answers a fenced read with the wall, not with rows.
  const julianParked = await staffGet(
    page,
    bearer,
    `/api/v1/pos/parked?branch_id=${JULIAN_ID}`,
  );
  expect(julianParked.status).toBe(403);

  // All three refund rails against the Julian sale itself, before any
  // money moves. The wall refuses the lookup that would hand this counter
  // the sale's lines, so the exit harness reads one line id of the Julian
  // sale from the throwaway database it owns (CT_KGN_LINE) and the sends go
  // out against the real row: cash from the open drawer, card, and account
  // credit, each refused with the branch wall's words.
  const lineId = process.env.CT_KGN_LINE ?? "";
  expect(lineId, "the exit harness supplies the Julian sale's line id").not.toBe("");
  const drawerId = await openDrawer(page, bearer);
  for (const method of ["CASH", "CARD", "ACCOUNT_CREDIT"]) {
    const sent = await staffPost(page, bearer, "/api/v1/pos/returns", {
      idempotency_key: crypto.randomUUID(),
      order_id: "",
      order_no: SEEDED.julianSale,
      lines: [{ order_line_id: lineId, qty_base: 1, reason_code: "OTHER" }],
      refund_method: method,
      reason: "",
      restock: true,
      till_session_id: drawerId,
    });
    expect(sent.status, `${method} against the Julian sale is refused`).toBe(403);
    expect(String(sent.body.error), `${method} names the branch wall`).toContain("not reachable");
  }
});

test("the world's newest rows are dated to the moment the box was rebuilt", async ({ page }) => {
  const bearer = await mint(page, "sales");
  // The seed writes its day-keyed counter sale under a stamp of its own
  // rebuild's house zone day; the run reads the stamp off its own clock and
  // asks the returns desk for that sale. The rebuild and the run sit on the
  // same day for every honest run of this suite, and a run that starts just
  // either side of the house zone's day change reads both stamps rather
  // than failing the box for the clock's turn.
  const today = houseDay().replaceAll("-", "");
  const yesterday = houseDaysAgo(1).replaceAll("-", "");
  const candidates = [...new Set([today, yesterday])];
  let found = false;
  let lastStatus = 0;
  for (const stamp of candidates) {
    const orderNo = `DEMO-PLM-T2-${stamp}`;
    const lookup = await staffGet(
      page,
      bearer,
      `/api/v1/pos/returns/lookup?order_no=${encodeURIComponent(orderNo)}`,
    );
    lastStatus = lookup.status;
    if (lookup.status === 200) {
      expect(lookup.body.order_no).toBe(orderNo);
      expect(lookup.body.branch_id).toBe(POINT_LOMA_ID);
      found = true;
      break;
    }
  }
  expect(found, `the day-keyed sale answers under one of the run's own stamps (last ${lastStatus})`).toBe(true);
});

test("the reads a technical tester reaches: the manager's reach the clerk's lacks, none fenced to a branch", async ({
  page,
}) => {
  const clerk = await mint(page, "sales");
  const manager = await mint(page, "sales-manager");
  // The box's own truth about the reads beyond the till, under the role
  // matrix: the manager's sign-in reaches the will-call queue and the scan
  // trail (their own validation answers next, 400), either sign-in reaches
  // the transaction trail, and the audit list is walled to both. None of the
  // three is fenced to a branch yet. The page's known issues say exactly
  // this and no more.
  const clerkWillCall = await staffGet(
    page,
    clerk,
    `/api/v1/dispatch/will-call?branch_id=${POINT_LOMA_ID}`,
  );
  expect(clerkWillCall.status).toBe(403);
  const clerkScanTrail = await staffGet(
    page,
    clerk,
    `/api/v1/dispatch/scan-trail?branch_id=${POINT_LOMA_ID}`,
  );
  expect(clerkScanTrail.status).toBe(403);
  for (const [who, bearer] of [
    ["the clerk", clerk],
    ["the manager", manager],
  ] as const) {
    const trail = await staffGet(page, bearer, "/api/v1/audit/transaction/pos.sale/x");
    expect(trail.status, `the transaction trail answers ${who} past the gate`).toBe(400);
    const audit = await staffGet(page, bearer, "/api/v1/audit?limit=5");
    expect(audit.status, `the audit list stays walled for ${who}`).toBe(403);
  }
  for (const path of [
    `/api/v1/dispatch/will-call?branch_id=${POINT_LOMA_ID}`,
    `/api/v1/dispatch/scan-trail?branch_id=${POINT_LOMA_ID}`,
  ]) {
    const answer = await staffGet(page, manager, path);
    expect(answer.status, `${path} is past the gate for the manager`).toBe(400);
  }
});
