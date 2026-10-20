// The TanyAI stub world: the four staff assist routes of PANEL-API r1 section 1, answering with the core's v1
// wire shapes and its two fixed sentences, plus a few scripted questions that also emit the section 2 fields
// (citations, notice, scope, answer_id) so the panel is built against them before the core ships them. A test
// double only; the real routes are the core's (core/internal/assist). The v1 answers are held to the generated
// wire descriptors by test/assist-stub-contract.test.mjs; the section 2 answers are not in the contract yet and
// are marked here.
//
// The scripted questions are plain words a test types:
//   owe        a results table with a customer ref
//   blu        a clarify with two options; the pick completes it
//   figures    section 2: text with citations, a scope and an answer_id
//   refuse     section 2: a refused notice (cost) with no text
//   model      section 2: model_unavailable, with text
//   privately  section 2: answered_privately
//   toobroad   section 2: too_broad
//   budget     limit_reached with the core's sentence
//   nonsense   a reply that suggests two commands
//   long       a long reply and a wide, tall table
//   xss        a reply that is markup, which must render as text
//   break      the first ask fails with a database sounding text, the retry answers
//   slow       answers after a pause, so the working state can be seen
// Anything else gets a short plain reply. Every chat body is kept for the history assertions.

export const LIMIT_REACHED = "You've used this month's AI allowance, ask your admin if you need more. You can still look things up with the buttons below; those don't use any AI.";
export const CHAT_DISABLED = "The assistant's chat is switched off right now. You can still look things up with the buttons below.";

const COMMANDS = [
  { code: "ar_aging", title: "Who owes us", summary: "Open balances by how late they are.", max_tier: 2, args: [] },
  { code: "orders_open", title: "Open orders", summary: "Orders still to be filled.", max_tier: 2, args: [] },
  { code: "payment_lookup", title: "Find a payment", summary: "Look up one payment by its number.", max_tier: 2, args: [{ name: "number", label: "Payment number", example: "PAY-1042", required: true }] },
  { code: "stock", title: "Stock on hand", summary: "What is on the shelf, by branch.", max_tier: 2, args: [{ name: "product", label: "Product", example: "Brussels Block", required: true }] },
];

const CUSTOMER = { kind: "customer", id: "e0000000-0000-4000-8000-000000000001", label: "Granite Edge Landscaping (demo)" };

const owe = () => ({
  command: "ar_aging",
  headline: "Who owes us",
  empty: false,
  note: "Showing the 3 largest balances.",
  ref: CUSTOMER,
  items: [
    { title: "Granite Edge Landscaping (demo)", fields: [{ label: "Open", value: "$12,400.00" }, { label: "Past due", value: "$3,100.00" }] },
    { title: "Eastgate Builders (demo)", fields: [{ label: "Open", value: "$8,950.00" }, { label: "Past due", value: "$0.00" }] },
    { title: "Lakeside Landscaping (demo)", fields: [{ label: "Open", value: "$4,210.00" }, { label: "Past due", value: "$1,020.00" }] },
  ],
});

const wide = () => ({
  command: "orders_open",
  headline: "Open orders",
  empty: false,
  note: "Showing the first 40 of 212.",
  items: Array.from({ length: 40 }, (_, i) => ({
    title: `SO-${1000 + i}`,
    fields: ["Customer", "Branch", "Promised", "Lines", "Total", "Status", "Driver"].map((label) => ({ label, value: `${label} ${i + 1} with a longer value` })),
  })),
});

const base = (over = {}) => ({ text: "", results: [], limit_reached: false, ran: [], ...over });

export function createAssistWorld() {
  let settings;
  let calls;
  let breakOnce;
  const reset = () => {
    settings = { chat_enabled: true, over_budget: false, spend_visible: true };
    calls = [];
    breakOnce = true;
  };
  reset();

  async function chat(body) {
    const q = String(body?.question ?? "").toLowerCase();
    calls.push(body);
    if (q.includes("slow")) await new Promise((r) => setTimeout(r, 1500));
    if (q.includes("break") && breakOnce) {
      breakOnce = false;
      return { status: 500, body: { error: 'pq: relation "assist_ledger" does not exist' } };
    }
    if (!settings.chat_enabled) return { status: 200, body: base({ text: CHAT_DISABLED }) };
    if (settings.over_budget || q.includes("budget")) return { status: 200, body: base({ text: LIMIT_REACHED, limit_reached: true }) };
    const usage = { provider: "stub", model: "hos-stub-1", tokens_in: 400, tokens_out: 140 };
    if (q.includes("owe")) return { status: 200, body: base({ text: "Three accounts have an open balance.", results: [owe()], ran: [{ command: "ar_aging", args: {} }], usage }) };
    if (q.includes("blu") && !q.includes("dbs-")) {
      const clarify = {
        question: 'Two products match "Blu", which one?',
        options: [
          { label: "Techo-Bloc Blu 60 (DBS-TB-BLU60)", value: "DBS-TB-BLU60", hint: "name match" },
          { label: "Techo-Bloc Blu 80 (DBS-TB-BLU80)", value: "DBS-TB-BLU80" },
        ],
        command: "stock",
        args: {},
        arg: "product",
      };
      return { status: 200, body: base({ text: clarify.question, clarify, usage }) };
    }
    if (q.includes("dbs-")) {
      return {
        status: 200,
        body: base({
          text: "There are 410 on the shelf at Point Loma.",
          ran: [{ command: "stock", args: { product: "DBS-TB-BLU60" } }],
          carried: ["product Techo-Bloc Blu 60"],
          usage,
        }),
      };
    }
    // Section 2 (not in the contract yet): citations, scope, notice, answer_id.
    if (q.includes("figures")) {
      return {
        status: 200,
        body: base({
          text: "Granite Edge owes $12,400.00 [1] and $3,100.00 of it is past due [2].",
          results: [owe()],
          citations: [
            { n: 1, label: "Granite Edge Landscaping (demo)", ref: CUSTOMER },
            { n: 2, label: "Granite Edge Landscaping (demo), past due", ref: CUSTOMER },
          ],
          scope: { branches: [{ id: "b0000000-0000-4000-8000-000000000001", label: "Point Loma" }], period: { from: "a", to: "b", label: "this month" }, as_of: "2031-01-01T08:15:00-08:00" },
          answer_id: "ans_0001",
          usage,
        }),
      };
    }
    if (q.includes("refuse")) return { status: 200, body: base({ notice: { code: "refused", class: "cost" }, answer_id: "ans_0002" }) };
    if (q.includes("model")) return { status: 200, body: base({ text: "Here is what I can show without the chat model.", notice: { code: "model_unavailable" } }) };
    if (q.includes("privately")) return { status: 200, body: base({ text: "Two orders are open for the group.", notice: { code: "answered_privately" } }) };
    if (q.includes("toobroad")) return { status: 200, body: base({ notice: { code: "too_broad" } }) };
    if (q.includes("nonsense")) return { status: 200, body: base({ text: "I could not tell what you meant. These may help.", suggest: ["ar_aging", "orders_open"] }) };
    if (q.includes("long")) {
      const lines = Array.from({ length: 40 }, (_, i) => `Line ${i + 1} of a long reply that keeps going to prove the thread scrolls inside the panel.`).join("\n");
      return { status: 200, body: base({ text: lines, results: [wide()] }) };
    }
    if (q.includes("xss")) return { status: 200, body: base({ text: "<img src=x onerror=\"window.__tanyaiXss = true\">" }) };
    return { status: 200, body: base({ text: "I looked, and there is nothing unusual today.", usage }) };
  }

  function command(body) {
    const code = String(body?.command ?? "");
    if (code === "ar_aging") return { status: 200, body: owe() };
    if (code === "orders_open") return { status: 200, body: { ...wide(), items: wide().items.slice(0, 3), note: "Showing the first 3 of 212." } };
    if (code === "payment_lookup") {
      const number = body?.args?.number ?? "";
      if (number === "NONE") return { status: 200, body: { command: code, headline: "Payment", items: [], empty: true } };
      return { status: 200, body: { command: code, headline: `Payment ${number}`, empty: false, items: [{ title: number, fields: [{ label: "Amount", value: "$640.00" }, { label: "Method", value: "Cheque" }] }] } };
    }
    if (code === "stock") return { status: 200, body: { command: code, headline: "Stock on hand", empty: false, items: [{ title: String(body?.args?.product ?? ""), fields: [{ label: "On hand", value: "410" }] }] } };
    return { status: 404, body: { error: "not found" } };
  }

  /** Answers a request, or null when the path is not an assist route. */
  async function handle(req, url, body) {
    const path = url.pathname;
    const method = req.method;
    if (method === "POST" && path === "/_stub/assist") {
      settings = { ...settings, ...body };
      return { status: 200, body: { ok: true } };
    }
    if (method === "GET" && path === "/_stub/assist-calls") return { status: 200, body: { calls } };
    if (method === "GET" && path === "/api/v1/assist/me") {
      return {
        status: 200,
        body: {
          budget: { tokens: 100000, source: "default" },
          chat_enabled: settings.chat_enabled,
          provider: "stub",
          spend_visible: settings.spend_visible,
          is_admin: false,
          spend: { exchanges: 3, tokens_in: 1200, tokens_out: 420 },
          over_budget: settings.over_budget,
        },
      };
    }
    if (method === "GET" && path === "/api/v1/assist/commands") return { status: 200, body: { commands: COMMANDS, chat_enabled: settings.chat_enabled } };
    if (method === "POST" && path === "/api/v1/assist/command") return command(body);
    if (method === "POST" && path === "/api/v1/assist/chat") return chat(body);
    return null;
  }

  return { handle, reset, calls: () => calls };
}
