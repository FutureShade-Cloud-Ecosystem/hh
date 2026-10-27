// The receivables office's stub world: AP contacts, reminders and the paper path, invoice send, document, dispute and
// payment reversal, the settings, and the extra hold reads. It answers the core's wire shapes and the core's
// sentences, in the core's order, and is held to them by test/office-stub-contract.test.mjs. The main stub owns the
// invoices, accounts, holds and payments; this module is handed a small context to read and change them.
//
// Roles: every route here passes a manager (Payables and Receivables is one); the routes the core guards with
// RequireAdmin answer the gate's shared body "Forbidden" to anyone who is not the controller.

const NOW_ISO = () => new Date().toISOString();

// UUID regex used by the C8 walk's read paths (S5 Promises, S6 Payment policy, S8 Reminders) where
// a phantom id is rejected without letting a FK constraint 500.
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const DEFAULT_POLICY = {
  aging_warn_days: 60,
  aging_escalate_days: 90,
  reminder_cadence_days: 7,
  reminder_max_per_cycle: 3,
  black_hole_window_days: 45,
  black_hole_min_sends: 3,
  reminder_email_enabled: true,
  reminder_sms_enabled: false,
  untagged_fallback: "CONFIRM",
  ar_tier: "BASE",
};

// The core's BucketFor and WatermarkFor (ar_landing.go, ar_reminders.go).
const bucketFor = (days, p) => (days <= 0 ? "CURRENT" : days < p.aging_warn_days + 1 ? "WARN" : days < p.aging_escalate_days ? "LATE" : "ESCALATION");
const watermarkFor = (days, resend) => (!resend ? "" : days <= 0 ? "REISSUED" : `PAST DUE \u00b7 ${days} DAYS`);

export function createOfficeWorld(ctx) {
  let w;

  function reset(kind = "full") {
    w = {
      kind,
      landingPolicy: { ...DEFAULT_POLICY, ar_tier: kind === "pro" ? "PRO" : "BASE" },
      policyHistory: [],
      registerPolicy: { account_position_visibility: "MANAGER_UP", due_soon_days: 7, reminder_tones: ["FRIENDLY", "FIRM", "FINAL"], reminder_cooldown_hours: 24, show_credits_default: false, workspace_split_pct: 60, workspace_arrangement: "PRIMARY_LEFT" },
      nsf: { branch_id: "", fee_cents: 3500, fee_taxable: false, fee_account_code: "4200", auto_hold: true, notes: "" },
      autoHold: false,
      switchWired: kind !== "unwired",
      invoiceSends: new Map(),
      sends: [],
      sendKeys: new Map(),
      // The demo world starts with the one paper path flag the landing has always drawn (Summit Patio, three sends).
      flags: kind === "empty" ? [] : [{ flag_id: ctx.uuid("6", 1), customer_id: ctx.customerIdOfNo("SP-400"), delivered_sends: 3, window_days: 45, raised_at: NOW_ISO(), status: "OPEN" }],
      flagCounter: 1,
      sendCounter: 0,
      // The demo world tags an invented desk on four of the five accounts; the fifth (Cedar Ridge) has no contact, so a statement
      // for it queues nothing and a reminder to it needs a confirmed fallback.
      apContacts: new Map(
        ctx.taggedAccounts().map((a) => [a.id, { customer_id: a.id, contact_name: a.label, email: "ap@gls-demo.invalid", phone: "", updated_at: NOW_ISO() }]),
      ),
    };
  }
  reset();

  const ok = (body, status = 200) => ({ status, body });
  const refuse = (status, error) => ({ status, body: { error } });
  const gate = (req, level) => (level === "admin" && !ctx.isController(req) ? { status: 403, body: { error: "Forbidden" } } : null);

  // ---- AP contacts (core/internal/billing ar_reminders.go ValidateAPContact, ar_landing_handler.go) --------------

  function validateContact(c) {
    const name = String(c.contact_name ?? "").trim();
    const email = String(c.email ?? "").trim();
    const phone = String(c.phone ?? "").trim();
    if ([...name].length > 120) return { error: refuse(422, "ar: contact_name is at most 120 characters") };
    if (email.length > 254 || phone.length > 32) return { error: refuse(422, "ar: an AP email is at most 254 characters and a phone at most 32") };
    if (email !== "" && (!email.includes("@") || /[ \t]/.test(email))) return { error: refuse(422, "ar: that AP email address is not an address") };
    if (email === "" && phone === "") return { error: refuse(422, "ar: an AP contact needs an email or a phone, one of the two is what makes it a contact") };
    return { clean: { contact_name: name, email, phone } };
  }

  // ---- the landing policy (core/internal/billing ar_landing.go, ar_landing_handler.go) --------------------------------

  const gateWords = {
    base: "reminders are part of accelerated AR, this account is on the base plan",
  };

  /** The core's ValidateARLandingPolicy: defaults, the six ranges in order, the two vocabularies, then the edge order. */
  function validatePolicy(p) {
    const clean = { ...p, untagged_fallback: p.untagged_fallback || "CONFIRM", ar_tier: p.ar_tier || "BASE" };
    const ranges = [
      ["aging_warn_days", 1, 364],
      ["aging_escalate_days", 2, 365],
      ["reminder_cadence_days", 1, 90],
      ["reminder_max_per_cycle", 1, 20],
      ["black_hole_window_days", 7, 365],
      ["black_hole_min_sends", 1, 20],
    ];
    for (const [knob, lo, hi] of ranges) {
      const v = clean[knob];
      if (!Number.isInteger(v) || v < lo || v > hi) return { error: refuse(422, `ar: ${knob} must be between ${lo} and ${hi}`) };
    }
    if (clean.untagged_fallback !== "CONFIRM" && clean.untagged_fallback !== "REFUSE") return { error: refuse(422, "ar: untagged_fallback must be CONFIRM or REFUSE, a send with no tagged contact is never a blast") };
    if (clean.ar_tier !== "BASE" && clean.ar_tier !== "PRO") return { error: refuse(422, "ar: ar_tier must be BASE or PRO") };
    if (clean.aging_warn_days >= clean.aging_escalate_days) return { error: refuse(422, `ar: aging_warn_days (${clean.aging_warn_days}) must be fewer than aging_escalate_days (${clean.aging_escalate_days})`) };
    return { clean };
  }

  /** A reminder (core ar_reminders.go Send): the replay by key, the account, the policy, the context, the target. */
  function sendReminder(body) {
    if (body === null) return refuse(400, "invalid JSON body");
    const p = w.landingPolicy;
    const key = typeof body.idempotency_key === "string" ? body.idempotency_key : "";
    if (key !== "" && w.sendKeys.has(key)) return ok(w.sendKeys.get(key), 201);
    const account = ctx.accounts().find((a) => a.id === body.customer_id);
    if (account === undefined) return refuse(404, "not found");
    const channel = body.channel;
    const open = ctx.openInvoices(account.id);
    const named = body.invoice_id ? open.find((i) => i.id === body.invoice_id) : [...open].sort((a, b) => b.late - a.late)[0];
    if (named === undefined) return refuse(422, "ar: there is no open invoice to remind about for this request");
    if (channel !== "EMAIL" && channel !== "SMS") return refuse(422, "ar: channel must be EMAIL or SMS");
    if (channel === "EMAIL" && !p.reminder_email_enabled) return refuse(422, "ar: email reminders are switched off for this dealer, turn them on in AR policy first");
    if (channel === "SMS" && !p.reminder_sms_enabled) return refuse(422, "ar: text reminders are switched off for this dealer, turn them on in AR policy first");
    const tagged = w.apContacts.get(account.id);
    const reachable = tagged !== undefined && (channel === "SMS" ? tagged.phone.trim() !== "" : tagged.email.trim() !== "");
    let kind = "AP_CONTACT";
    let recipient = channel === "SMS" ? tagged?.phone : tagged?.email;
    let fallbackReason;
    if (!reachable) {
      if (p.untagged_fallback === "REFUSE") return refuse(422, "ar: no AP contact is tagged on this account, tag one and the reminder goes to the right desk");
      const candidates = channel === "EMAIL" ? [`accounts@${account.no.toLowerCase()}.invalid`] : [];
      if (candidates.length === 0) return refuse(422, "ar: this account has no address on this channel, tag an AP contact to send anything at all");
      if (body.confirm_fallback !== true) return refuse(422, "ar: nobody is tagged for AP on this account, confirm the fallback recipient or tag a contact");
      kind = "FALLBACK";
      recipient = candidates[0];
      fallbackReason = "no AP contact tagged; operator confirmed the account address";
    }
    const resend = body.resend === true;
    w.sendCounter += 1;
    const send = {
      id: ctx.uuid("5", w.sendCounter),
      customer_id: account.id,
      invoice_id: named.id,
      channel,
      recipient,
      recipient_kind: kind,
      ...(fallbackReason === undefined ? {} : { fallback_reason: fallbackReason }),
      bucket: bucketFor(named.late, p),
      resend,
      ...(watermarkFor(named.late, resend) === "" ? {} : { watermark: watermarkFor(named.late, resend) }),
      status: "DELIVERED",
      provider: "stub",
      sent_at: new Date(Date.now() + w.sendCounter).toISOString(),
      delivered_at: NOW_ISO(),
      open_count: 0,
      click_count: 0,
    };
    w.sends.push(send);
    if (key !== "") w.sendKeys.set(key, send);
    return ok(send, 201);
  }

  /** The black hole sweep (core SweepPaperPath): accounts with enough delivered sends and no open, not already flagged. */
  function sweep() {
    const p = w.landingPolicy;
    const byAccount = new Map();
    for (const s of w.sends) if (s.status === "DELIVERED") byAccount.set(s.customer_id, (byAccount.get(s.customer_id) ?? 0) + 1);
    let raised = 0;
    for (const [customerId, n] of byAccount) {
      const opened = w.sends.some((s) => s.customer_id === customerId && s.open_count > 0);
      if (opened || n < p.black_hole_min_sends) continue;
      if (w.flags.some((f) => f.customer_id === customerId && f.status === "OPEN")) continue;
      w.flagCounter += 1;
      w.flags.unshift({ flag_id: ctx.uuid("6", w.flagCounter), customer_id: customerId, delivered_sends: n, window_days: p.black_hole_window_days, raised_at: NOW_ISO(), status: "OPEN" });
      raised += 1;
    }
    return ok({ evaluated: byAccount.size, raised });
  }

  const paperEntry = (f) => {
    const a = ctx.accounts().find((x) => x.id === f.customer_id);
    return { flag_id: f.flag_id, customer_id: f.customer_id, customer_name: a?.name ?? "Account", account_no: a?.no ?? "", delivered_sends: f.delivered_sends, window_days: f.window_days, raised_at: f.raised_at };
  };

  // ---- an invoice: send, document, dispute (core/internal/billing register_service.go, register_handler.go) ----------

  const invoiceOr404 = (id) => {
    const inv = ctx.invById(id);
    return inv === undefined ? { error: refuse(404, "no such invoice") } : { inv };
  };
  const NO_CONTACT = new Set(["OB-200"]);

  function sendInvoice(id, body) {
    if (body === null) return refuse(422, "the request body must be JSON");
    const key = typeof body.idempotency_key === "string" ? body.idempotency_key : "";
    if (key === "") return refuse(422, "sending an invoice needs an idempotency key");
    const found = invoiceOr404(id);
    if (found.error) return found.error;
    const account = ctx.acct(found.inv.acct);
    if (NO_CONTACT.has(account.no)) return refuse(422, `${account.name} has no contact email on the account`);
    if (w.invoiceSends.has(key)) return ok({ sent: 0, replayed: 1, tone: "" });
    w.invoiceSends.set(key, found.inv.id);
    return ok({ sent: 1, replayed: 0, tone: "" });
  }

  function documentOf(id) {
    const found = invoiceOr404(id);
    if (found.error) return found.error;
    const inv = found.inv;
    const a = ctx.acct(inv.acct);
    const text = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Invoice ${inv.invoice_no}</title></head><body><h1>Invoice ${inv.invoice_no}</h1><p>${a.name}, account ${a.no}</p><p>Total ${inv.total_cents} cents, balance ${inv.balance_cents} cents.</p></body></html>`;
    return { status: 200, type: "text/html; charset=utf-8", text };
  }

  function openDispute(id, body) {
    if (body === null) return refuse(422, "the request body must be JSON");
    const key = typeof body.idempotency_key === "string" ? body.idempotency_key : "";
    if (key === "") return refuse(422, "opening a dispute needs an idempotency key");
    const reason = String(body.reason ?? "").trim().toUpperCase();
    if (!["SHORT_DELIVERY", "DAMAGED", "PRICING", "NOT_ORDERED", "OTHER"].includes(reason)) return refuse(422, "a dispute names a reason: short delivery, damaged, pricing, not ordered, or other");
    const found = invoiceOr404(id);
    if (found.error) return found.error;
    const inv = found.inv;
    if (body.amount_cents > inv.balance_cents) return refuse(422, "the disputed amount cannot exceed the outstanding balance");
    if (inv.dispute !== null && inv.dispute.status === "OPEN") return { status: 409, body: { error: "this invoice already has an open dispute", dispute: inv.dispute } };
    inv.dispute = { id: ctx.uuid("7", ctx.world().counter + 500), status: "OPEN", reason, note: String(body.note ?? "").trim(), amount_cents: body.amount_cents, raised_at: NOW_ISO() };
    ctx.world().counter += 1;
    return ok(inv.dispute);
  }

  function resolveDispute(id, body) {
    if (body === null) return refuse(422, "the request body must be JSON");
    const outcome = String(body.outcome ?? "").trim().toUpperCase();
    if (!["PAID", "CREDITED", "WRITTEN_OFF", "WITHDRAWN"].includes(outcome)) return refuse(422, "closing a dispute names how it ended: paid, credited, written off, or withdrawn");
    const found = invoiceOr404(id);
    if (found.error) return found.error;
    const inv = found.inv;
    if (inv.dispute === null || inv.dispute.status !== "OPEN") return ok({ resolved: false });
    inv.dispute = { ...inv.dispute, status: "RESOLVED", outcome, resolved_at: NOW_ISO() };
    return ok({ resolved: true });
  }

  // ---- a payment reversal (core/internal/payments reversal_handler.go, reversal.go) ------------------------------------

  function reversePayment(id, body) {
    if (body === null) return refuse(400, "invalid JSON body");
    const world = ctx.world();
    const p = world.payments.find((x) => x.id === id);
    const reason = String(body.reason ?? "");
    if (reason === "") return refuse(400, "payments: a reversal reason is required");
    if (p === undefined) return refuse(404, "payment not found");
    if (p.reverses !== undefined) return refuse(400, "payments: this payment cannot be reversed: it is itself a reversing entry, take the payment again as a new payment so the reason for re-charging is its own record");
    if (p.reversed === true) return refuse(409, "payments: this payment has already been reversed");
    if (p.status === "BOUNCED") return refuse(400, "payments: this payment cannot be reversed: it was bounced, and a payment is reversed or bounced, never both");
    if (p.status !== "RECEIVED" && p.status !== "DEPOSITED") return refuse(400, `payments: this payment cannot be reversed: status is ${p.status}, only a received or deposited payment can be reversed`);
    const reopened = [];
    for (const al of p.allocations) {
      const inv = world.invoices.find((i) => ctx.invNumber(i) === al.invoice_n);
      inv.balance_cents += al.amount_cents;
      reopened.push(inv.id);
    }
    p.reversed = true;
    world.counter += 1;
    const reversal = { id: ctx.uuid("c", 100 + world.counter), no: `REV-${String(100 + world.counter).padStart(4, "0")}`, acct: p.acct, method: p.method, amount_cents: p.amount_cents, status: "VOID", reason, received_at: NOW_ISO(), bounced_at: "", allocations: p.allocations.map((al) => ({ ...al })), reverses: p.id };
    world.payments.push(reversal);
    return ok({ label: "VOID", original_payment_id: p.id, reversal_payment_id: reversal.id, reversal_no: reversal.no, amount_cents: p.amount_cents, reason, same_day: true, reopened_invoices: reopened }, 201);
  }

  // ---- the money a reversal holds, applied to an invoice (core/internal/payments reapply.go, reversal.go) ----------------

  /** What the payment holds: amount less what was re-applied, for a reversed original; zero otherwise. */
  function heldOf(p) {
    const applied = p.reapplied_cents ?? 0;
    const paidOut = p.paid_out_cents ?? 0;
    return { applied, paidOut, held: p.reversed === true && p.reverses === undefined ? p.amount_cents - applied - paidOut : 0 };
  }

  function heldFor(id) {
    const p = ctx.world().payments.find((x) => x.id === id);
    if (p === undefined) return refuse(404, "payment not found");
    const { applied, paidOut, held } = heldOf(p);
    return ok({ payment_id: p.id, payment_no: p.no, amount_cents: p.amount_cents, reversed: p.reversed === true, applied_cents: applied, paid_out_cents: paidOut, held_cents: held });
  }

  function reapplyHeld(id, body) {
    if (body === null) return refuse(400, "invalid JSON body");
    const world = ctx.world();
    world.reapplyKeys ??= new Map();
    // The service's checks, in its order (reapply.go validateReapply), before the store is reached.
    const reason = String(body.reason ?? "").trim();
    if (reason === "") return refuse(400, "payments: a re-application reason is required");
    const key = String(body.idempotency_key ?? "").trim();
    if (key === "") return refuse(400, "re-applying held money needs an idempotency key");
    if (!UUID_RE.test(id)) return refuse(400, "payment id must be a uuid");
    const asked = Array.isArray(body.allocations) ? body.allocations : [];
    if (asked.length === 0) return refuse(400, "name at least one invoice to apply the held money to");
    const seen = new Set();
    for (const a of asked) {
      if (!UUID_RE.test(String(a.invoice_id ?? ""))) return refuse(400, "invoice ids must be uuids");
      if (seen.has(a.invoice_id)) return refuse(400, `the same invoice is named twice: ${a.invoice_id}`);
      seen.add(a.invoice_id);
      if (!Number.isInteger(a.amount_cents) || a.amount_cents <= 0) return refuse(400, "each amount applied must be greater than zero");
    }
    const p = world.payments.find((x) => x.id === id);
    if (p === undefined) return refuse(404, "payment not found");
    // A replay is judged under the payment, before the account and the held figure.
    const spent = world.reapplyKeys.get(key);
    if (spent !== undefined) {
      const same = spent.payment === id && spent.lines.length === asked.length && asked.every((a) => spent.lines.some((l) => l.invoice_id === a.invoice_id && l.applied_cents === a.amount_cents));
      if (!same) return refuse(409, "that idempotency key already re-applied held money differently; use a new key");
      const live = spent.lines.map((l) => {
        const inv = world.invoices.find((i) => i.id === l.invoice_id);
        return { ...l, balance_after_cents: inv.balance_cents, outcome: inv.balance_cents <= 0 ? "CLOSES" : "PART" };
      });
      const { held } = heldOf(p);
      const total = live.reduce((n, l) => n + l.applied_cents, 0);
      return ok({ payment_id: id, batch_id: spent.batch, applied_cents: total, held_before_cents: held + total, held_after_cents: held, lines: live, replayed: true });
    }
    if (p.reverses !== undefined) return refuse(409, "payments: no money is held for this payment: it is itself a reversing entry, the money is held for the payment it reversed");
    if (p.reversed !== true) return refuse(409, "payments: no money is held for this payment: it has not been reversed");
    const { held } = heldOf(p);
    const total = asked.reduce((n, a) => n + a.amount_cents, 0);
    if (total > held) return refuse(409, `payments: that is more than the payment still holds (${held} cents held, ${total} asked for)`);
    const targets = [];
    for (const a of [...asked].sort((x, y) => (x.invoice_id < y.invoice_id ? -1 : 1))) {
      const inv = world.invoices.find((i) => i.id === a.invoice_id && i.acct === p.acct);
      if (inv === undefined) return refuse(404, `invoice not found: ${a.invoice_id}`);
      if (inv.balance_cents <= 0) return refuse(400, `invoice ${inv.invoice_no} is not open, held money can only be applied to an open invoice`);
      if (a.amount_cents > inv.balance_cents) return refuse(400, `invoice ${inv.invoice_no} can take at most ${inv.balance_cents} cents right now; refresh and retry`);
      targets.push({ inv, cents: a.amount_cents });
    }
    world.counter += 1;
    const batch = ctx.uuid("d", 700 + world.counter);
    const lines = targets.map((t) => {
      t.inv.balance_cents -= t.cents;
      return { invoice_id: t.inv.id, invoice_no: t.inv.invoice_no, applied_cents: t.cents, outcome: t.inv.balance_cents <= 0 ? "CLOSES" : "PART", balance_after_cents: t.inv.balance_cents };
    });
    p.reapplied_cents = (p.reapplied_cents ?? 0) + total;
    world.reapplyKeys.set(key, { payment: id, batch, lines: lines.map((l) => ({ invoice_id: l.invoice_id, invoice_no: l.invoice_no, applied_cents: l.applied_cents })) });
    return ok({ payment_id: id, batch_id: batch, applied_cents: total, held_before_cents: held, held_after_cents: held - total, lines, replayed: false }, 201);
  }

  /**
   * SF-2m and SF-2n (core/internal/payments payout.go, reversal.go PayOutHeld), in the core's order: the service's checks,
   * then the store's. The stub's payments carry no till session, so a write off is refused as the core refuses a payment
   * rung through no session, and the branch has one open drawer a cash pay out lands on.
   */
  function payOutHeld(id, body) {
    if (body === null) return refuse(400, "invalid JSON body");
    const world = ctx.world();
    world.payoutKeys ??= new Map();
    const reason = String(body.reason ?? "").trim();
    if (reason === "") return refuse(400, "payments: a reason is required");
    const key = String(body.idempotency_key ?? "").trim();
    if (key === "") return refuse(400, "paying out held money needs an idempotency key");
    const kind = String(body.kind ?? "");
    if (!["DRAWER", "BANK", "WRITE_OFF"].includes(kind)) return refuse(400, "the kind must be DRAWER, BANK or WRITE_OFF");
    if (!Number.isInteger(body.amount_cents) || body.amount_cents <= 0) return refuse(400, "the amount must be greater than zero");
    const reference = String(body.reference ?? "").trim();
    if (kind === "BANK" && reference === "") return refuse(400, "a bank pay out needs the cheque number or transfer reference");
    if (!UUID_RE.test(id)) return refuse(400, "payment id must be a uuid");
    const p = world.payments.find((x) => x.id === id);
    if (p === undefined) return refuse(404, "payment not found");
    const { held } = heldOf(p);
    const spent = world.payoutKeys.get(key);
    if (spent !== undefined) {
      if (spent.payment !== id || spent.kind !== kind || spent.amount_cents !== body.amount_cents) return refuse(409, "that idempotency key already paid out held money differently; use a new key");
      return ok({ payment_id: id, payout_id: spent.payout, kind, amount_cents: spent.amount_cents, held_before_cents: held + spent.amount_cents, held_after_cents: held, replayed: true });
    }
    if (p.reverses !== undefined) return refuse(409, "payments: no money is held for this payment: it is itself a reversing entry, the money is held for the payment it reversed");
    if (p.reversed !== true) return refuse(409, "payments: no money is held for this payment: it has not been reversed");
    if (body.amount_cents > held) return refuse(409, `payments: that is more than the payment still holds (${held} cents held, ${body.amount_cents} asked for)`);
    if (kind === "WRITE_OFF") return refuse(409, "payments: that cannot be done with the money held for this payment: this payment was not rung through a till session, so there is no count to cover a write off");
    world.counter += 1;
    const payout = ctx.uuid("f", 800 + world.counter);
    p.paid_out_cents = (p.paid_out_cents ?? 0) + body.amount_cents;
    world.payoutKeys.set(key, { payment: id, kind, amount_cents: body.amount_cents, payout });
    return ok({ payment_id: id, payout_id: payout, kind, amount_cents: body.amount_cents, held_before_cents: held, held_after_cents: held - body.amount_cents, replayed: false }, 201);
  }

  // ---- applying open credit memos and account credits to invoices, and reversing applications (FW1-12b) -----------------

  function applyCredit(body) {
    if (body === null) return refuse(400, "invalid JSON body");
    const world = ctx.world();
    world.creditApplyKeys ??= new Map();
    const reason = String(body.reason ?? "").trim();
    if (reason === "") return refuse(400, "billing: a reason is required");
    const key = String(body.idempotency_key ?? "").trim();
    if (key === "") return refuse(400, "applying a credit needs an idempotency key");
    const kind = String(body.credit_kind ?? "").trim().toUpperCase();
    if (kind !== "MEMO" && kind !== "ACCOUNT") return refuse(400, "credit_kind must be MEMO or ACCOUNT");
    if (!Number.isInteger(body.amount_cents) || body.amount_cents <= 0) return refuse(400, "the amount applied must be greater than zero");
    const customerId = String(body.customer_id ?? "").trim();
    const creditId = String(body.credit_id ?? "").trim();
    const invoiceId = String(body.invoice_id ?? "").trim();
    if (!UUID_RE.test(customerId)) return refuse(400, "customer id must be a uuid");
    if (!UUID_RE.test(creditId)) return refuse(400, "credit id must be a uuid");
    if (!UUID_RE.test(invoiceId)) return refuse(400, "invoice id must be a uuid");

    const custLower = customerId.toLowerCase();
    const creditLower = creditId.toLowerCase();
    const invLower = invoiceId.toLowerCase();
    const inv = world.invoices.find((i) => i.id === invLower && ctx.custId(i.acct) === custLower);
    if (inv === undefined) return refuse(404, `invoice not found: ${invoiceId}`);

    const memo = kind === "MEMO" ? world.credit_memos.find((m) => m.id === creditLower && ctx.custId(m.acct) === custLower && m.status !== "VOID") : undefined;
    const acctCredit = kind === "ACCOUNT" ? (world.account_credits ?? []).find((c) => c.id === creditLower && ctx.custId(c.acct) === custLower) : undefined;
    if (kind === "MEMO" && memo === undefined) return refuse(404, `credit not found: ${creditId}`);
    if (kind === "ACCOUNT" && acctCredit === undefined) return refuse(404, `credit not found: ${creditId}`);

    const spent = world.creditApplyKeys.get(key);
    if (spent !== undefined) {
      if (
        spent.customer_id !== custLower ||
        spent.credit_kind !== kind ||
        spent.credit_id !== creditLower ||
        spent.invoice_id !== invLower ||
        spent.amount_cents !== body.amount_cents
      ) {
        return refuse(409, "that idempotency key already applied a credit differently; use a new key");
      }
      const creditNow = kind === "MEMO" ? Math.max(0, memo.amount_cents - (memo.applied_cents ?? 0)) : acctCredit.open_cents;
      return ok(
        {
          ...spent.answer,
          credit_before_cents: creditNow + spent.amount_cents,
          credit_after_cents: creditNow,
          invoice_balance_before_cents: inv.balance_cents + spent.amount_cents,
          invoice_balance_after_cents: inv.balance_cents,
          outcome: inv.balance_cents <= 0 ? "CLOSES" : "PART",
          replayed: true,
        },
        200,
      );
    }

    const creditOpen = kind === "MEMO" ? Math.max(0, memo.amount_cents - (memo.applied_cents ?? 0)) : acctCredit.open_cents;
    if (creditOpen <= 0) return refuse(409, "billing: that credit has nothing left to apply");
    if (inv.balance_cents <= 0) return refuse(400, `invoice ${inv.invoice_no} is not open, a credit can only be applied to an open invoice`);
    if (body.amount_cents > creditOpen) return refuse(400, `the credit has ${creditOpen} cents open; that is more than it holds`);
    const payable = Math.max(inv.balance_cents - (inv.holdback_cents ?? 0), 0);
    if (body.amount_cents > payable) return refuse(400, `invoice ${inv.invoice_no} can take at most ${payable} cents right now; refresh and retry`);

    world.counter += 1;
    const appId = ctx.uuid("b", 800 + world.counter);
    const journalId = ctx.uuid("e", 800 + world.counter);
    const creditNo = kind === "MEMO" ? memo.no : acctCredit.no;
    const creditBefore = creditOpen;
    const creditAfter = creditOpen - body.amount_cents;
    const invBefore = inv.balance_cents;
    const invAfter = inv.balance_cents - body.amount_cents;
    if (kind === "MEMO") {
      memo.applied_cents = (memo.applied_cents ?? 0) + body.amount_cents;
    } else {
      acctCredit.open_cents = creditAfter;
    }
    inv.balance_cents = invAfter;
    world.credit_applications ??= [];
    world.credit_applications.unshift({
      application_id: appId,
      customer_id: custLower,
      credit_kind: kind,
      credit_id: creditLower,
      credit_no: creditNo,
      invoice_id: inv.id,
      invoice_no: inv.invoice_no,
      amount_cents: body.amount_cents,
      reason,
      journal_entry_id: journalId,
      applied_at: NOW_ISO(),
    });
    const answer = {
      application_id: appId,
      customer_id: custLower,
      credit_kind: kind,
      credit_id: creditLower,
      credit_no: creditNo,
      invoice_id: inv.id,
      invoice_no: inv.invoice_no,
      applied_cents: body.amount_cents,
      credit_before_cents: creditBefore,
      credit_after_cents: creditAfter,
      invoice_balance_before_cents: invBefore,
      invoice_balance_after_cents: invAfter,
      outcome: invAfter <= 0 ? "CLOSES" : "PART",
      journal_entry_id: journalId,
      replayed: false,
    };
    world.creditApplyKeys.set(key, {
      customer_id: custLower,
      credit_kind: kind,
      credit_id: creditLower,
      invoice_id: invLower,
      amount_cents: body.amount_cents,
      answer,
    });
    return ok(answer, 201);
  }

  function reverseCreditApplication(body) {
    if (body === null) return refuse(400, "invalid JSON body");
    const world = ctx.world();
    const reason = String(body.reason ?? "").trim();
    if (reason === "") return refuse(400, "billing: a reason is required");
    const invoiceId = String(body.invoice_id ?? "").trim();
    const applicationId = String(body.application_id ?? "").trim();
    if (!UUID_RE.test(invoiceId)) return refuse(400, "invoice id must be a uuid");
    if (!UUID_RE.test(applicationId)) return refuse(400, "application id must be a uuid");

    const inv = world.invoices.find((i) => i.id === invoiceId.toLowerCase());
    if (inv === undefined) return refuse(404, `invoice not found: ${invoiceId}`);
    const app = (world.credit_applications ?? []).find((a) => a.application_id === applicationId.toLowerCase() && a.invoice_id === inv.id);
    if (app === undefined) return refuse(400, `no credit application ${applicationId} sits on invoice ${inv.invoice_no}`);
    if (app.reversed_at !== undefined) return refuse(409, "billing: that application has already been reversed");

    let creditOpenAfter = 0;
    if (app.credit_kind === "MEMO") {
      const memo = world.credit_memos.find((m) => m.id === app.credit_id);
      if (memo === undefined) return refuse(404, `credit not found: ${app.credit_id}`);
      memo.applied_cents = Math.max(0, (memo.applied_cents ?? 0) - app.amount_cents);
      creditOpenAfter = memo.amount_cents - memo.applied_cents;
    } else {
      const acctCredit = (world.account_credits ?? []).find((c) => c.id === app.credit_id);
      if (acctCredit === undefined) return refuse(404, `credit not found: ${app.credit_id}`);
      acctCredit.open_cents += app.amount_cents;
      creditOpenAfter = acctCredit.open_cents;
    }
    inv.balance_cents += app.amount_cents;
    world.counter += 1;
    const reversalId = ctx.uuid("b", 900 + world.counter);
    const journalId = ctx.uuid("e", 900 + world.counter);
    app.reversed_at = NOW_ISO();
    app.reversal_reason = reason;
    return ok(
      {
        application_id: app.application_id,
        reversal_id: reversalId,
        credit_kind: app.credit_kind,
        credit_id: app.credit_id,
        credit_no: app.credit_no,
        invoice_id: inv.id,
        invoice_no: inv.invoice_no,
        amount_cents: app.amount_cents,
        credit_open_cents: creditOpenAfter,
        invoice_balance_cents: inv.balance_cents,
        reason,
        journal_entry_id: journalId,
      },
      201,
    );
  }

  // ---- settings: the register, the returned payment fee, automatic holds ----------------------------------------------------

  const TONE_RANK = { FRIENDLY: 0, FIRM: 1, FINAL: 2 };

  /** The core's RegisterService.SetPolicy checks, in its order (register_service.go). */
  function putRegisterPolicy(body) {
    if (body === null) return refuse(422, "the request body must be JSON");
    if (!["MANAGER_UP", "ALL_STAFF", "HIDDEN"].includes(body.account_position_visibility)) return refuse(422, "account position visibility must be MANAGER_UP, ALL_STAFF or HIDDEN");
    if (!Number.isInteger(body.due_soon_days) || body.due_soon_days < 1 || body.due_soon_days > 90) return refuse(422, "the due-soon window is between 1 and 90 days");
    if (!Number.isInteger(body.reminder_cooldown_hours) || body.reminder_cooldown_hours < 0 || body.reminder_cooldown_hours > 720) return refuse(422, "the reminder cooldown is between 0 and 720 hours");
    if (!Number.isInteger(body.workspace_split_pct) || body.workspace_split_pct < 20 || body.workspace_split_pct > 80) return refuse(422, "the panel split is between 20 and 80 percent");
    if (!["PRIMARY_LEFT", "PRIMARY_RIGHT"].includes(body.workspace_arrangement)) return refuse(422, "the panel arrangement is PRIMARY_LEFT or PRIMARY_RIGHT");
    const tones = [];
    for (const raw of body.reminder_tones ?? []) {
      const t = String(raw).trim().toUpperCase();
      if (!["FRIENDLY", "FIRM", "FINAL"].includes(t)) return refuse(422, "a reminder tone is FRIENDLY, FIRM or FINAL");
      if (!tones.includes(t)) tones.push(t);
    }
    tones.sort((a, b) => TONE_RANK[a] - TONE_RANK[b]);
    w.registerPolicy = {
      account_position_visibility: body.account_position_visibility,
      due_soon_days: body.due_soon_days,
      reminder_tones: tones,
      reminder_cooldown_hours: body.reminder_cooldown_hours,
      show_credits_default: body.show_credits_default === true,
      workspace_split_pct: body.workspace_split_pct,
      workspace_arrangement: body.workspace_arrangement,
    };
    return ok({ saved: true });
  }

  function putNsf(body) {
    if (body === null) return refuse(400, "invalid_json");
    if (!Number.isInteger(body.fee_cents) || body.fee_cents < 0) return refuse(422, "till: fee_cents must be >= 0");
    w.nsf = { branch_id: "", fee_cents: body.fee_cents, fee_taxable: body.fee_taxable === true, fee_account_code: body.fee_account_code === "" || body.fee_account_code === undefined ? "4200" : body.fee_account_code, auto_hold: body.auto_hold === true, notes: String(body.notes ?? "") };
    return ok({ status: "ok" });
  }

  const autoHoldView = () => ({
    auto_hold_enabled: w.autoHold,
    scope: "ORG",
    aging_bucket_days: 30,
    aging_threshold_cents: 500_000,
    aging_hold_enabled: true,
    limit_hold_enabled: true,
    limit_tolerance_cents: 0,
    cod_enforced: true,
    thresholds_editable: false,
    switch_wired: w.switchWired,
  });

  function putAutoHold(body) {
    if (body === null) return refuse(400, "invalid JSON body");
    if (typeof body.enabled !== "boolean") return refuse(400, "enabled must be true or false, say which");
    if (!w.switchWired) return refuse(503, "the auto-hold switch is not configured on this server, the AR policy write seam is not wired");
    w.autoHold = body.enabled;
    return ok(autoHoldView());
  }

  // ---- holds a returned payment placed (core/internal/till handler.go, service.go) ----------------------------------------

  const tillHold = (h) => ({
    id: h.id,
    customer_id: h.customer_id,
    source_payment_id: h.src,
    status: h.status,
    reason: h.reason,
    ...(h.release_reason === undefined ? {} : { release_reason: h.release_reason }),
    created_at: h.created_at,
    ...(h.released_at === undefined ? {} : { released_at: h.released_at }),
  });

  function releaseTillHold(id, body) {
    if (body === null) return refuse(400, "invalid_json");
    const reason = String(body.reason ?? "");
    if (reason === "") return refuse(422, "till: a release reason is required");
    const hold = ctx.world().holds.find((h) => h.id === id && h.src !== undefined);
    if (hold === undefined || hold.status !== "ACTIVE") return refuse(404, "not found");
    hold.status = "RELEASED";
    hold.release_reason = reason;
    hold.released_at = NOW_ISO();
    return ok({ status: "RELEASED" });
  }

  /** The dispatch gate (core credit_gate.go CanDispatch): a hold with no override in force stops the order. */
  function dispatchGate(orderId) {
    const world = ctx.world();
    const inv = world.invoices.find((i) => ctx.uuid("o", ctx.invNumber(i)) === orderId);
    if (inv === undefined) return ok({ allowed: false, reason: "AR check unavailable: order not found" });
    const hold = world.holds.find((h) => h.status === "ACTIVE" && h.customer_id === ctx.custId(inv.acct));
    if (hold === undefined) return ok({ allowed: true, reason: "" });
    if (typeof hold.override_until === "string" && Date.parse(hold.override_until) > Date.now()) return ok({ allowed: true, reason: "" });
    if (hold.hold_kind === "COD") return ok({ allowed: false, reason: "PAYMENT DUE, this COD account is on hold until the balance is paid" });
    if (hold.hold_kind === "NSF") return ok({ allowed: false, reason: "ACCOUNT ON HOLD, a payment was returned; the office must clear this before delivery" });
    return ok({ allowed: false, reason: "ACCOUNT ON HOLD, the office must clear this account before delivery" });
  }

  function handle(req, url, body) {
    const path = url.pathname;
    const method = req.method;
    if (path === "/api/v1/ar/register/policy") {
      if (method === "GET") return ok(w.registerPolicy);
      if (method === "PUT") return gate(req, "admin") ?? putRegisterPolicy(body);
    }
    if (path === "/api/v1/ar/nsf-policy") {
      if (method === "GET") return ok(w.nsf);
      if (method === "PUT") return gate(req, "admin") ?? putNsf(body);
    }
    if (path === "/api/v1/ar/auto-hold") {
      if (method === "GET") return gate(req, "admin") ?? ok(autoHoldView());
      if (method === "PUT") return gate(req, "admin") ?? putAutoHold(body);
    }
    if (method === "GET" && path === "/api/v1/ar/credit-holds") {
      const status = url.searchParams.get("status") ?? "";
      return ok({ items: ctx.world().holds.filter((h) => h.src !== undefined && (status === "" || h.status === status)).map(tillHold) });
    }
    let m = path.match(/^\/api\/v1\/ar\/credit-holds\/([^/]+)\/release$/);
    if (m && method === "POST") return gate(req, "admin") ?? releaseTillHold(m[1], body);
    m = path.match(/^\/api\/v1\/ar\/dispatch-gate\/([^/]+)$/);
    if (m && method === "GET") return dispatchGate(m[1]);
    m = path.match(/^\/api\/v1\/ar\/register\/invoices\/([^/]+)\/(send|document|dispute|dispute\/resolve)$/);
    if (m) {
      if (m[2] === "send" && method === "POST") return sendInvoice(m[1], body);
      if (m[2] === "document" && method === "GET") return documentOf(m[1]);
      if (m[2] === "dispute" && method === "POST") return openDispute(m[1], body);
      if (m[2] === "dispute/resolve" && method === "POST") return resolveDispute(m[1], body);
    }
    m = path.match(/^\/api\/v1\/ar\/payments\/([^/]+)\/reverse$/);
    if (m && method === "POST") return reversePayment(m[1], body);
    m = path.match(/^\/api\/v1\/ar\/payments\/([^/]+)\/reapply$/);
    if (m && method === "POST") return reapplyHeld(m[1], body);
    m = path.match(/^\/api\/v1\/ar\/payments\/([^/]+)\/payout$/);
    if (m && method === "POST") return payOutHeld(m[1], body);
    m = path.match(/^\/api\/v1\/ar\/payments\/([^/]+)\/held$/);
    if (m && method === "GET") return heldFor(m[1]);
    if (method === "POST" && path === "/api/v1/ar/credits/apply") return applyCredit(body);
    if (method === "POST" && path === "/api/v1/ar/credits/applications/reverse") return reverseCreditApplication(body);
    if (method === "POST" && path === "/api/v1/ar/reminders") {
      // The act is gated on the capability, not only the payload: a base plan caller who hand crafts the POST is refused.
      if (w.landingPolicy.ar_tier !== "PRO") return refuse(403, gateWords.base);
      return sendReminder(body);
    }
    if (method === "GET" && path === "/api/v1/ar/reminders") {
      const customer = url.searchParams.get("customer_id") ?? "";
      const items = w.sends.filter((s) => customer === "" || s.customer_id === customer).slice().reverse();
      return ok({ items });
    }
    // Walk change 8 S8 / S5M: the office reminder rules table.
    if (method === "GET" && path === "/api/v1/ar/reminders/rules") return ok({ rules: reminderRules() });
    // Walk change 8 S8: re-enrol an exempt account into one rule.
    if (method === "POST" && path === "/api/v1/ar/reminders/enrol") return enrolReminder(body);
    // Walk change 8 S8: the inverse of enrol; the note is mandatory, refused 400 when missing.
    if (method === "POST" && path === "/api/v1/ar/reminders/exempt") return exemptReminder(body);
    // Walk change 8 S5: cross-account Promises tab read.
    if (method === "GET" && path === "/api/v1/ar/promises") return ok({ rows: promisesFor(url.searchParams), next_cursor: null });
    // Walk change 8 S6: read / write one account's payment policy.
    m = path.match(/^\/api\/v1\/ar\/customers\/([^/]+)\/payment-policy$/);
    if (m) {
      if (method === "GET") return ok(accountPaymentPolicy(m[1]));
      if (method === "PUT") return putAccountPaymentPolicy(m[1], body);
    }
    if (method === "GET" && path === "/api/v1/ar/paper-path") return ok({ items: openFlags().map(paperEntry) });
    if (method === "POST" && path === "/api/v1/ar/paper-path/sweep") return sweep();
    let pp = path.match(/^\/api\/v1\/ar\/paper-path\/([^/]+)\/resolve$/);
    if (pp && method === "POST") {
      if (body === null) return refuse(400, "invalid JSON body");
      if (body.status !== "MOVED_TO_PAPER" && body.status !== "DISMISSED") return refuse(422, "ar: a paper-path flag closes as MOVED_TO_PAPER or DISMISSED, both are decisions, and both need one");
      const note = String(body.note ?? "");
      if (note.trim() === "") return refuse(422, "ar: say why in a few words, the next person to open this account reads it, not the flag");
      if ([...note].length > 300) return refuse(422, "ar: that note is longer than 300 characters");
      const flag = w.flags.find((f) => f.flag_id === pp[1] && f.status === "OPEN");
      if (flag === undefined) return refuse(404, "not found");
      flag.status = body.status;
      return ok({ ok: true });
    }
    if (path === "/api/v1/ar/landing/policy") {
      if (method === "GET") return ok(w.landingPolicy);
      if (method === "PUT") {
        const denied = gate(req, "admin");
        if (denied !== null) return denied;
        if (body === null) return refuse(400, "invalid JSON body");
        // The CURRENT policy is the base, so a PUT that carries one knob does not silently reset the others.
        const next = { ...w.landingPolicy, ...body };
        delete next.branch_id;
        const checked = validatePolicy(next);
        if (checked.error) return checked.error;
        const changes = [];
        for (const [field, value] of Object.entries(checked.clean)) {
          if (String(value) !== String(w.landingPolicy[field])) changes.push({ field, before: String(w.landingPolicy[field]), after: String(value), who: "Financial Controller", when: NOW_ISO() });
        }
        w.policyHistory.unshift(...changes.reverse());
        w.landingPolicy = checked.clean;
        return ok(checked.clean);
      }
    }
    if (method === "GET" && path === "/api/v1/ar/landing/policy/history") {
      const denied = gate(req, "admin");
      if (denied !== null) return denied;
      return ok({ items: w.policyHistory });
    }
    m = path.match(/^\/api\/v1\/ar\/customers\/([^/]+)\/ap-contact$/);
    if (m) {
      const id = m[1];
      if (method === "GET") {
        const c = w.apContacts.get(id);
        return ok(c === undefined ? { tagged: false } : { tagged: true, contact: c });
      }
      if (method === "PUT") {
        if (body === null) return refuse(400, "invalid JSON body");
        const checked = validateContact(body);
        if (checked.error) return checked.error;
        w.apContacts.set(id, { customer_id: id, ...checked.clean, updated_at: NOW_ISO() });
        return ok({ tagged: true, contact: { customer_id: id, ...checked.clean } });
      }
      if (method === "DELETE") {
        w.apContacts.delete(id);
        return ok({ tagged: false });
      }
    }
    return null;
  }

  const openFlags = () => w.flags.filter((f) => f.status === "OPEN");

  // ---- walk change 8 S5: cross-account Promises tab read ------------------------------------------------------

  /** Promises augmented with customer name and invoice number (the Promises tab's display columns). */
  function promisesFor(params) {
    const status = (params.get("status") ?? "").split(",").map((s) => s.trim()).filter(Boolean);
    const customerId = params.get("customer_id") ?? "";
    const world = ctx.world();
    const out = [];
    for (const p of world.promises) {
      if (status.length > 0 && !status.includes(p.status)) continue;
      if (customerId !== "" && p.customer_id !== customerId) continue;
      const acct = world.accounts.find((a) => a.id === p.customer_id);
      const inv = world.invoices.find((i) => i.id === p.invoice_id);
      out.push({
        id: p.id,
        customer_id: p.customer_id,
        customer_name: acct?.name ?? "",
        invoice_id: p.invoice_id,
        invoice_no: inv !== undefined ? ctx.invNumber(inv) : "",
        promised_cents: p.promised_cents,
        promised_date: p.promised_date,
        status: p.status,
        promised_by: p.promised_by ?? "",
        note: p.note ?? "",
        taken_by: p.taken_by ?? "",
        taken_at: p.taken_at ?? "",
        settled_at: p.settled_at ?? null,
      });
    }
    out.sort((a, b) => String(b.taken_at).localeCompare(String(a.taken_at)));
    return out;
  }

  // ---- walk change 8 S6: account payment policy (read/upsert) -------------------------------------------------

  /** Walk change 8 S6: one account's payment policy. Absent row -> house default (ach_first true,
   * surcharge 240 bps, min 0). Backed by world.paymentPolicies if a row exists. */
  function accountPaymentPolicy(customerId) {
    const world = ctx.world();
    const row = world.paymentPolicies[customerId];
    if (row === undefined) {
      return {
        customer_id: customerId,
        ach_first: true,
        surcharge_rate_bps: 240,
        min_ach_cents: 0,
        updated_at: NOW_ISO(),
      };
    }
    return row;
  }

  function putAccountPaymentPolicy(customerId, body) {
    if (body === null) return refuse(400, "invalid_json");
    const world = ctx.world();
    if (world.accounts.find((a) => a.id === customerId) === undefined) return refuse(404, "not found");
    const ach_first = body.ach_first === true;
    const surcharge = Number(body.surcharge_rate_bps ?? 240);
    if (!Number.isInteger(surcharge) || surcharge < 0 || surcharge > 10000) return refuse(400, "surcharge_rate_bps must be between 0 and 10000");
    const min = Number(body.min_ach_cents ?? 0);
    if (!Number.isFinite(min) || min < 0) return refuse(400, "min_ach_cents must be >= 0");
    const row = {
      customer_id: customerId,
      ach_first,
      surcharge_rate_bps: surcharge,
      min_ach_cents: Math.trunc(min),
      updated_at: NOW_ISO(),
    };
    world.paymentPolicies[customerId] = row;
    return ok(row);
  }

  // ---- walk change 8 S8 / S5M: reminder rules + enrol / exempt ---------------------------------------------

  /** Five default rules (mirrors the boards' pick) so the office never opens with an empty table. */
  function reminderRules() {
    const world = ctx.world();
    if (world.reminderRules === undefined || world.reminderRules.length === 0) {
      world.reminderRules = [
        { id: "rr-current", code: "CURRENT", label: "On-the-day reminder", channel: "EMAIL", days_past_due: 0, max_per_window: 1, paylink_url_template: "/paylink/{token}", last_sent_at: null, amount_brought_in_cents: 0, enrolled_count: 0, exclusion_count: 0, updated_at: NOW_ISO() },
        { id: "rr-30", code: "BUCKET_30", label: "Past 30 days", channel: "EMAIL", days_past_due: 30, max_per_window: 2, paylink_url_template: "/paylink/{token}", last_sent_at: null, amount_brought_in_cents: 0, enrolled_count: 0, exclusion_count: 0, updated_at: NOW_ISO() },
        { id: "rr-60", code: "BUCKET_60", label: "Past 60 days", channel: "EMAIL", days_past_due: 60, max_per_window: 2, paylink_url_template: "/paylink/{token}", last_sent_at: null, amount_brought_in_cents: 0, enrolled_count: 0, exclusion_count: 0, updated_at: NOW_ISO() },
        { id: "rr-90", code: "BUCKET_90", label: "Past 90 days", channel: "PHONE", days_past_due: 90, max_per_window: 1, paylink_url_template: "/paylink/{token}", last_sent_at: null, amount_brought_in_cents: 0, enrolled_count: 0, exclusion_count: 0, updated_at: NOW_ISO() },
        { id: "rr-blackhole", code: "BLACK_HOLE", label: "Paper-path escalation", channel: "PHONE", days_past_due: 120, max_per_window: 1, paylink_url_template: "/paylink/{token}", last_sent_at: null, amount_brought_in_cents: 0, enrolled_count: 0, exclusion_count: 0, updated_at: NOW_ISO() },
      ];
    }
    return world.reminderRules;
  }

  function ruleById(ruleId) {
    return reminderRules().find((r) => r.id === ruleId);
  }

  function exclusionKey(customerId, ruleId) {
    return `${customerId}:${ruleId}`;
  }

  function enrolReminder(body) {
    if (body === null) return refuse(400, "invalid_json");
    const customerId = String(body.customer_id ?? "");
    const ruleId = String(body.rule_id ?? "");
    if (!UUID_RE.test(customerId)) return refuse(400, "invalid customer_id");
    if (!UUID_RE.test(ruleId)) return refuse(400, "invalid rule_id");
    const world = ctx.world();
    if (world.accounts.find((a) => a.id === customerId) === undefined) return refuse(404, "not found");
    const rule = ruleById(ruleId);
    if (rule === undefined) return refuse(404, "not found");
    const wasExempt = world.exemptions.get(exclusionKey(customerId, ruleId)) === true;
    if (wasExempt) {
      world.exemptions.delete(exclusionKey(customerId, ruleId));
      rule.exclusion_count = Math.max(0, (rule.exclusion_count ?? 0) - 1);
    } else if (!world.exemptions.has(exclusionKey(customerId, ruleId))) {
      // first-time enrol bumps the counter; idempotent repeats don't.
      rule.enrolled_count = (rule.enrolled_count ?? 0) + 1;
    }
    world.exemptions.set(exclusionKey(customerId, ruleId), false);
    rule.updated_at = NOW_ISO();
    return ok({
      customer_id: customerId,
      rule_id: ruleId,
      exempt: false,
      note: String(body.note ?? ""),
      enrolled_count: rule.enrolled_count,
      exclusion_count: rule.exclusion_count,
      updated_at: rule.updated_at,
    });
  }

  function exemptReminder(body) {
    if (body === null) return refuse(400, "invalid_json");
    const customerId = String(body.customer_id ?? "");
    const ruleId = String(body.rule_id ?? "");
    const note = String(body.note ?? "").trim();
    if (!UUID_RE.test(customerId)) return refuse(400, "invalid customer_id");
    if (!UUID_RE.test(ruleId)) return refuse(400, "invalid rule_id");
    if (note === "") return refuse(400, "note is required for exempt");
    if (note.length > 300) return refuse(400, "note must be 300 characters or fewer");
    const world = ctx.world();
    if (world.accounts.find((a) => a.id === customerId) === undefined) return refuse(404, "not found");
    const rule = ruleById(ruleId);
    if (rule === undefined) return refuse(404, "not found");
    const already = world.exemptions.get(exclusionKey(customerId, ruleId));
    if (already !== true) rule.exclusion_count = (rule.exclusion_count ?? 0) + 1;
    world.exemptions.set(exclusionKey(customerId, ruleId), true);
    rule.updated_at = NOW_ISO();
    return ok({
      customer_id: customerId,
      rule_id: ruleId,
      exempt: true,
      note,
      enrolled_count: rule.enrolled_count,
      exclusion_count: rule.exclusion_count,
      updated_at: rule.updated_at,
    });
  }

  return { reset, handle, state: () => w, gate, ok, refuse, paperPath: () => openFlags().map(paperEntry), policy: () => w.landingPolicy };
}
