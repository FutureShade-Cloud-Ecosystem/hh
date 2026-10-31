// The stub's deposit refusals held against the core's own order (the review's
// stub compare, P2-2): the core prices the lines, answers the deposit bound
// first (deposit_out_of_range: 0 < deposit < the sale total; a deposit equal
// to the total is just a paid sale and a negative one is no deposit at all),
// and only then refuses a collect now deposit, with the total as integer cents
// in the body and named in dollars in the message. The stub used to answer
// deposit_not_collect_now before it priced anything, so a collect now sale
// with a deposit at or over the total met a different refusal from the stub
// than from the server; these cases hold the order, the bound's negative arm,
// the body's total_cents, and the grouped dollar words.
//
// The stub runs here as its own process on a free port (the same script the
// Playwright harness starts), and the cases speak to it the way the till
// does: a minted bearer, the demo org header, an open drawer, one priced
// line. The Charcoal paver at list, no account: one square foot is 400 cents
// plus 13 percent tax, a 452 cent total; three hundred square feet is a
// 135600 cent total, past the first thousands group.
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import assert from "node:assert/strict";
import { after, test } from "node:test";
import { fileURLToPath } from "node:url";

const stubPath = fileURLToPath(new URL("./stub/demo-stub.mjs", import.meta.url));
const POINT_LOMA = "6f1c2b3a-0000-4000-8000-000000000001";
const HOLLAND = "p-charcoal-60";
const HOUSE_ZONE = "America/Toronto";

function houseDay() {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: HOUSE_ZONE,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date());
}

async function freePort() {
  const door = createServer();
  await new Promise((resolve) => door.listen(0, "127.0.0.1", resolve));
  const port = door.address().port;
  await new Promise((resolve) => door.close(resolve));
  return port;
}

async function healthOk(base) {
  try {
    const answer = await fetch(`${base}/healthz`);
    return answer.ok;
  } catch {
    return false;
  }
}

const port = await freePort();
const base = `http://127.0.0.1:${port}`;
const stub = spawn(process.execPath, [stubPath], { env: { CT_STUB_PORT: String(port) }, stdio: "ignore" });
after(() => {
  stub.kill();
});

for (let waited = 0; !(await healthOk(base)); waited += 100) {
  assert.ok(waited < 10_000, "the demo stub did not come up");
  await new Promise((resolve) => setTimeout(resolve, 100));
}

async function post(path, body, bearer) {
  const answer = await fetch(`${base}${path}`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      ...(bearer === undefined ? {} : { authorization: `Bearer ${bearer}` }),
      "x-org": "hh-demo",
    },
    body: JSON.stringify(body),
  });
  return { status: answer.status, body: await answer.json() };
}

const session = await post("/api/v1/demo/session", { flow: "sales" });
assert.equal(session.status, 200, "the stub did not mint a bearer");
const bearer = session.body.token;
const drawer = await post("/api/v1/till/sessions", { branch_id: POINT_LOMA, station: "1", float_cents: 30000 }, bearer);
assert.equal(drawer.status, 201, "the stub did not open the branch's drawer");

function saleBody(overrides) {
  return {
    branch_id: POINT_LOMA,
    till_session_id: drawer.body.id,
    sale_type: "COLLECT_NOW",
    tender: "CASH",
    requested_date: houseDay(),
    lines: [{ product_id: HOLLAND, qty_entered: 1 }],
    ...overrides,
  };
}

test("a collect now sale with a deposit at the total meets the bound, not the collect now word", async () => {
  const answer = await post("/api/v1/pos/sales", saleBody({ deposit_cents: 452 }), bearer);
  assert.equal(answer.status, 422);
  assert.equal(answer.body.error, "deposit_out_of_range");
  assert.equal(answer.body.deposit_cents, 452);
  assert.equal(answer.body.total_cents, 452);
  assert.equal(
    answer.body.message,
    "The deposit must be more than $0.00 and less than the sale total of $4.52.",
  );
});

test("a negative deposit meets the bound as the core's own ring answers", async () => {
  const answer = await post("/api/v1/pos/sales", saleBody({ deposit_cents: -100 }), bearer);
  assert.equal(answer.status, 422);
  assert.equal(answer.body.error, "deposit_out_of_range");
  assert.equal(answer.body.deposit_cents, -100);
  assert.equal(answer.body.total_cents, 452);
});

test("the collect now refusal carries the total as cents and names it in grouped dollars", async () => {
  const answer = await post(
    "/api/v1/pos/sales",
    saleBody({ lines: [{ product_id: HOLLAND, qty_entered: 300 }], deposit_cents: 10000 }),
    bearer,
  );
  assert.equal(answer.status, 422);
  assert.equal(answer.body.error, "deposit_not_collect_now");
  assert.equal(answer.body.deposit_cents, 10000);
  assert.equal(answer.body.total_cents, 135600);
  assert.equal(
    answer.body.message,
    "A collect now sale is paid in full, the goods do not leave on a deposit. Take the whole $1,356.00 now, or choose will call, delivery or a pay before release sale and leave a deposit.",
  );
});

test("the bound's own sentence groups the total's thousands as the server does", async () => {
  const answer = await post(
    "/api/v1/pos/sales",
    saleBody({ lines: [{ product_id: HOLLAND, qty_entered: 300 }], deposit_cents: 135600 }),
    bearer,
  );
  assert.equal(answer.status, 422);
  assert.equal(answer.body.error, "deposit_out_of_range");
  assert.equal(answer.body.total_cents, 135600);
  assert.equal(
    answer.body.message,
    "The deposit must be more than $0.00 and less than the sale total of $1,356.00.",
  );
});

test("a held sale under the bound still books: the moved refusals swallow nothing", async () => {
  const answer = await post(
    "/api/v1/pos/sales",
    saleBody({ sale_type: "COD", deposit_cents: 100, deposit_method: "CASH" }),
    bearer,
  );
  assert.equal(answer.status, 201);
  assert.equal(answer.body.total_cents, 452);
  assert.equal(answer.body.deposit_cents, 100);
  assert.equal(answer.body.tenders.some((leg) => leg.method === "CASH" && leg.amount_cents === 100), true);
});

// Round 4 (principal item 206): the standing read answers a caller the dealer's balance policy
// strips figures from with the word `withheld`, never `clear` (clear is a position too), and the
// hold is the same for every caller: the held account reads on_hold for this caller as well, with
// no money key in either answer.
test("the standing read answers a withheld caller withheld, and the hold the same as anyone", async () => {
  const get = async (customerId) => {
    const answer = await fetch(
      `${base}/api/v1/pos/account-standing?branch_id=${POINT_LOMA}&customer_id=${customerId}`,
      { headers: { authorization: `Bearer ${bearer}`, "x-org": "hh-demo" } },
    );
    return { status: answer.status, body: await answer.json() };
  };
  const withheld = await get("c-fenwick");
  assert.equal(withheld.status, 200);
  assert.equal(withheld.body.standing, "withheld");
  assert.equal(withheld.body.past_due_cents, undefined, "the overdue figure never rides the withheld answer");
  assert.equal(withheld.body.oldest_bucket, undefined);
  const held = await get("c-harbour");
  assert.equal(held.status, 200);
  assert.equal(held.body.standing, "on_hold");
  assert.equal(held.body.on_hold, true);
  assert.equal(held.body.past_due_cents, undefined);
});

test("the sale door fences a named job with the core's own words", async () => {
  // The job accounts are the demo world's own (demo-stub.mjs derives them from
  // the customer id): the customer's open job carries the sale, another
  // customer's job, a closed job and a job that is not there are refused with
  // the customer module's sentences (core/internal/customer/job_document.go).
  const maple = "c-maple-ridge";
  const fenwick = "c-fenwick";
  const ring = (customerId, jobId) => post("/api/v1/pos/sales", saleBody({ customer_id: customerId, job_id: jobId }), bearer);

  const carries = await ring(maple, `job-${maple}-drive`);
  assert.equal(carries.status, 201, `the customer's own open job carries the sale: ${JSON.stringify(carries.body)}`);

  const other = await ring(maple, `job-${fenwick}-drive`);
  assert.equal(other.status, 400);
  assert.equal(other.body.error, "that job is another customer's job");

  const closed = await ring(maple, `job-${maple}-done`);
  assert.equal(closed.status, 400);
  assert.equal(closed.body.error, "the job is closed, so it takes no new document");

  const unknown = await ring(maple, "job-that-is-no-job");
  assert.equal(unknown.status, 404);
  assert.equal(unknown.body.error, "not found");
});
