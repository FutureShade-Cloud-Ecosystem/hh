// SHELL PROOF SCRIPT. Compiled in only with the `shell-proof` cargo feature
// (off by default, never in a shipped build): see src/lib.rs and build.rs.
//
// It is injected into every frame of the main window. In every frame it
// answers the demo mint locally (the front door's two demo routes) and holds
// every other /api/ call unanswered, because this run has no core: the front
// door then signs a role in through its own picker and the real desk app
// bundles boot and wait, instead of failing and ending their own walk. In the
// top frame only it drives the Work shell host (web/apps/work-shell) through
// its role leg like a person would, by the host's own controls, and reports
// ONE JSON string through the one proof command. It opens no remote origin and
// reads nothing but the page.
(function () {
  "use strict";

  var USERS = {
    sales: ["demo-account-sales", "inside_sales", "Inside Sales", 3, "BRANCH"],
    "sales-manager": ["demo-account-sales-manager", "sales_manager", "Sales Manager", 2, "BRANCH"],
    "yard-manager": ["demo-account-yard-manager", "yard_manager", "Yard manager", 2, "BRANCH"],
    "logistics-manager": ["demo-account-logistics-manager", "logistics_manager", "Logistics Manager", 2, "BRANCH"],
    driver: ["demo-account-driver", "driver", "Driver", 3, "BRANCH"]
  };

  function b64url(text) {
    return btoa(text).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }

  function token(sub) {
    var exp = Math.floor(Date.now() / 1000) + 3600;
    return b64url('{"alg":"HS256","typ":"JWT"}') + "." + b64url(JSON.stringify({ sub: sub, exp: exp })) + ".proof-signature";
  }

  function answer(status, body) {
    return new Response(JSON.stringify(body), { status: status, headers: { "content-type": "application/json" } });
  }

  // CSP violations of every frame are collected on the top window.
  function collector() {
    try {
      var top = window.top;
      if (!top.__hhProofCsp) top.__hhProofCsp = [];
      return top.__hhProofCsp;
    } catch (e) {
      return [];
    }
  }
  document.addEventListener("securitypolicyviolation", function (e) {
    collector().push(location.pathname + " " + e.violatedDirective + " " + e.blockedURI);
  });

  var realFetch = window.fetch.bind(window);
  window.fetch = function (input, init) {
    var url = typeof input === "string" ? input : (input && input.url) || "";
    var path = "";
    try {
      path = new URL(url, location.href).pathname;
    } catch (e) {
      path = "";
    }
    if (path === "/api/v1/demo/flows") {
      return Promise.resolve(answer(200, { flows: [] }));
    }
    if (path === "/api/v1/demo/session") {
      var flow = "";
      try {
        flow = JSON.parse(init && init.body ? String(init.body) : "{}").flow;
      } catch (e) {
        flow = "";
      }
      var row = Object.prototype.hasOwnProperty.call(USERS, flow) ? USERS[flow] : null;
      if (row === null) return Promise.resolve(answer(400, { error: "unknown flow" }));
      return Promise.resolve(
        answer(200, {
          token: token(row[0]),
          user: { user_id: row[0], email: flow + "@demo.invalid", display_name: row[2] + " (demo)", role_code: row[1], role_name: row[2], tier: row[3], scope: row[4] }
        })
      );
    }
    if (path.indexOf("/api/") === 0) return new Promise(function () {});
    return realFetch(input, init);
  };

  // Only the top frame, only the host page, only once, drives.
  if (window.top !== window || window.__hhProofStarted) return;
  window.__hhProofStarted = true;

  function sleep(ms) {
    return new Promise(function (resolve) { setTimeout(resolve, ms); });
  }

  async function until(what, test, ms) {
    var limit = Date.now() + (ms || 15000);
    for (;;) {
      var value = null;
      try { value = test(); } catch (e) { value = null; }
      if (value) return value;
      if (Date.now() > limit) throw new Error("timed out waiting for " + what);
      await sleep(25);
    }
  }

  function shadow() {
    var host = document.querySelector("ws-host");
    return host ? host.shadowRoot : null;
  }
  function shell() { var r = shadow(); return r ? r.querySelector('[data-testid="ws-shell"]') : null; }
  function header() { var r = shadow(); return r ? r.querySelector("hh-shell-header") : null; }
  function rail() { var r = shadow(); return r ? r.querySelector("hh-shell-rail") : null; }
  function appFrame() { var r = shadow(); return r ? r.querySelector('iframe[data-testid="app-frame"]') : null; }
  function doorFrame() { var r = shadow(); return r ? r.querySelector('iframe[data-testid="door-frame"]') : null; }

  function railKeys() {
    var r = rail();
    if (!r || !r.shadowRoot) return [];
    return Array.prototype.map.call(r.shadowRoot.querySelectorAll("button.row"), function (b) { return b.getAttribute("data-key"); });
  }

  function sessionKeysPresent() {
    return ["hh-demo.front-door.session.v1", "hh-auth.session.v2"].filter(function (k) { return sessionStorage.getItem(k) !== null; });
  }

  // What the content frame holds: its path, its root element and whether that element is defined in the frame.
  function frameFacts(frame) {
    var doc = frame.contentDocument;
    var win = frame.contentWindow;
    var rootEl = doc && doc.body ? doc.body.querySelector(":scope > *:not(script)") : null;
    var tag = rootEl ? rootEl.tagName.toLowerCase() : "";
    return { path: win.location.pathname, root: tag, defined: tag.indexOf("-") > 0 && !!win.customElements.get(tag) };
  }

  async function appLoaded(expectedPath) {
    return until("the app frame at " + expectedPath, function () {
      var f = appFrame();
      if (!f || !f.contentWindow) return null;
      var facts = frameFacts(f);
      return facts.path === expectedPath && facts.defined ? facts : null;
    }, 20000);
  }

  async function signInAs(flow) {
    await until("the picker", function () {
      var s = shell();
      var d = doorFrame();
      if (!s || s.getAttribute("data-phase") !== "door" || !d || !d.contentDocument) return null;
      var root = d.contentDocument.querySelector("fd-app-root");
      return root && root.shadowRoot && root.shadowRoot.querySelector('[data-action="choose"][data-flow="' + flow + '"]');
    }).then(function (button) { button.click(); });
    await until("the desk after " + flow, function () {
      var s = shell();
      var h = header();
      return s && s.getAttribute("data-phase") === "desk" && h && h.user !== "" ? s : null;
    });
  }

  async function openSelector() {
    var h = header();
    h.shadowRoot.querySelector("button.selector").click();
    await until("the selector panel", function () { return h.shadowRoot.querySelector(".panel .mi"); });
    return h;
  }

  async function switchRole() {
    header().shadowRoot.querySelector("button.account").click();
    var button = await until("the account menu", function () {
      var r = shadow();
      var menu = r && r.querySelector('[data-testid="account-menu"]');
      return menu && menu.querySelector('button[data-action="switch-role"]');
    });
    button.click();
    await until("the picker after Switch role", function () {
      var s = shell();
      return s && s.getAttribute("data-phase") === "door" && doorFrame() ? s : null;
    });
  }

  async function trial(flow, options) {
    var out = { flow: flow };
    await signInAs(flow);
    var s = shell();
    out.role = s.getAttribute("data-role");
    var h = header();
    out.org = h.org;
    out.user = h.user;
    out.switcher = (h.groups || []).map(function (g) { return g.app.id; });
    out.landing = s.getAttribute("data-current-app");
    out.breadcrumb = h.breadcrumb;
    out.pins_on_entry = railKeys();
    if (out.landing === "") {
      out.landing_frame = null;
      out.empty_state = !!(shadow() && shadow().querySelector('[data-testid="no-apps"]'));
    } else {
      out.landing_frame = await appLoaded(shadow().querySelector('iframe[data-testid="app-frame"]').getAttribute("src"));
      if (options.pin) {
        // pins are kept per person on the device: pin only what is not pinned yet, so a rerun on the same data stays honest
        if (railKeys().indexOf(out.landing) < 0) {
          h.shadowRoot.querySelector('button[data-action="pin"]').click();
          await until("the pin in the rail", function () { return railKeys().indexOf(out.landing) >= 0; });
        }
        out.pins_after_pin = railKeys();
      }
      var other = out.switcher.filter(function (id) { return id !== out.landing; })[0];
      if (other && options.switchApp) {
        var opened = await openSelector();
        opened.shadowRoot.querySelector('.panel .mi[data-app="' + other + '"]').click();
        await until("the app " + other, function () { return shell().getAttribute("data-current-app") === other; });
        out.switched_to = other;
        out.switched_frame = await appLoaded(shadow().querySelector('iframe[data-testid="app-frame"]').getAttribute("src"));
        out.switched_breadcrumb = header().breadcrumb;
      }
    }
    await switchRole();
    out.switch_role = {
      returned_to_picker: shell().getAttribute("data-phase") === "door",
      session_keys_left: sessionKeysPresent(),
      switcher_gone: header() === null
    };
    return out;
  }

  async function drive() {
    var result = { ok: false, proof: "hh-work-shell-role-leg", origin: location.origin, trials: [] };
    try {
      await until("the host page", function () { return shell(); });
      var s = shell();
      await until("the profile", function () { return doorFrame(); });
      result.console = s.getAttribute("data-console");
      result.trials.push(await trial("sales", { pin: true, switchApp: true }));
      result.trials.push(await trial("sales-manager", { pin: false, switchApp: true }));
      result.trials.push(await trial("yard-manager", { pin: false, switchApp: false }));
      result.trials.push(await trial("driver", { pin: false, switchApp: false }));
      // the first person comes back: their pins waited for them
      result.trials.push(await trial("sales", { pin: false, switchApp: false }));
      result.ok = true;
    } catch (e) {
      result.error = String(e && e.message ? e.message : e);
    }
    result.csp_violations = collector();
    return result;
  }

  window.addEventListener("DOMContentLoaded", function () {
    if (location.pathname !== "/") return;
    drive().then(function (result) {
      window.__TAURI_INTERNALS__.invoke("proof_report", { json: JSON.stringify(result) });
    });
  });
})();
