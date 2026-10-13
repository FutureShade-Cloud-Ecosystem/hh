// The receivables office's stub world: AP contacts, reminders and the paper path, invoice send, document, dispute and
// payment reversal, the settings, and the extra hold reads. It answers the core's wire shapes and the core's
// sentences, in the core's order, and is held to them by test/office-stub-contract.test.mjs. The main stub owns the
// invoices, accounts, holds and payments; this module is handed a small context to read and change them.
//
// Roles: every route here passes a manager (Payables and Receivables is one); the routes the core guards with
// RequireAdmin answer the gate's shared body "Forbidden" to anyone who is not the controller.

const NOW_ISO = () => new Date().toISOString();

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

  function handle(req, url, body) {
    const path = url.pathname;
    const method = req.method;
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
    let m = path.match(/^\/api\/v1\/ar\/customers\/([^/]+)\/ap-contact$/);
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

  return { reset, handle, state: () => w, gate, ok, refuse, paperPath: () => openFlags().map(paperEntry), policy: () => w.landingPolicy };
}
