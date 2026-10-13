// The receivables office's stub world: AP contacts, reminders and the paper path, invoice send, document, dispute and
// payment reversal, the settings, and the extra hold reads. It answers the core's wire shapes and the core's
// sentences, in the core's order, and is held to them by test/office-stub-contract.test.mjs. The main stub owns the
// invoices, accounts, holds and payments; this module is handed a small context to read and change them.
//
// Roles: every route here passes a manager (Payables and Receivables is one); the routes the core guards with
// RequireAdmin answer the gate's shared body "Forbidden" to anyone who is not the controller.

const NOW_ISO = () => new Date().toISOString();

export function createOfficeWorld(ctx) {
  let w;

  function reset(kind = "full") {
    w = {
      kind,
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

  function handle(req, url, body) {
    const path = url.pathname;
    const method = req.method;
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

  return { reset, handle, state: () => w, gate, ok, refuse };
}
