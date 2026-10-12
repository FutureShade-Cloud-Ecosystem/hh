// PROBE SCAFFOLDING, removed when the real shell lands. Compiled in only with
// the `probe` cargo feature (off by default), never in a normal build.
// Probe 4 only: runs in the webview beside the front door bundle, writes one
// SQLite row and reads it back, stores one keychain value and reads it back,
// then reports the result and the webview origin to the Rust log.
(function () {
  var csp = [];
  document.addEventListener("securitypolicyviolation", function (e) {
    csp.push(e.violatedDirective + " " + e.blockedURI);
  });
  window.addEventListener("DOMContentLoaded", async function () {
    var inv = window.__TAURI_INTERNALS__.invoke;
    var out = { origin: window.location.origin, ua: navigator.userAgent };
    try {
      var db = await inv("plugin:sql|load", { db: "sqlite:probe4.db" });
      await inv("plugin:sql|execute", { db: db, query: "CREATE TABLE IF NOT EXISTS probe (id INTEGER PRIMARY KEY, note TEXT NOT NULL)", values: [] });
      var note = "row-" + Date.now();
      await inv("plugin:sql|execute", { db: db, query: "INSERT INTO probe (note) VALUES ($1)", values: [note] });
      var rows = await inv("plugin:sql|select", { db: db, query: "SELECT id, note FROM probe ORDER BY id DESC LIMIT 1", values: [] });
      out.sql = { wrote: note, read: rows[0] && rows[0].note, ok: !!rows[0] && rows[0].note === note };
    } catch (e) { out.sql = { ok: false, error: String(e) }; }
    try {
      var v = "value-" + Date.now();
      await inv("keychain_set", { slot: "probe", value: v });
      var back = await inv("keychain_get", { slot: "probe" });
      out.keychain = { ok: back === v };
      // Leave nothing behind in the user's keyring (the probe4.db file in the
      // app config dir stays; remove it by hand after a run).
      await inv("keychain_delete", { slot: "probe" });
      out.keychain.deleted = true;
    } catch (e) { out.keychain = { ok: false, error: String(e) }; }
    // Let the front door render, then record whether its role picker is on
    // screen and whether the content security policy refused anything.
    await new Promise(function (r) { setTimeout(r, 1500); });
    var host = document.querySelector("fd-app-root");
    var root = host && (host.shadowRoot || host);
    out.frontDoor = { picker: !!root && root.textContent.indexOf("Pick a role") >= 0 };
    out.csp = { violations: csp.length, first: csp.slice(0, 3) };
    var banner = document.createElement("pre");
    banner.style.cssText = "position:fixed;left:0;right:0;bottom:0;margin:0;padding:8px;background:#111;color:#0f0;font:12px monospace;z-index:99999;white-space:pre-wrap";
    banner.textContent = "PROBE4 " + JSON.stringify(out);
    document.body.appendChild(banner);
    inv("probe_report", { json: JSON.stringify(out) });
  });
})();
