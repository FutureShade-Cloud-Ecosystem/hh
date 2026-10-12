// Probe 4 only: runs in the webview beside the front door bundle, writes one
// SQLite row and reads it back, stores one keychain value and reads it back,
// then reports the result and the webview origin to the Rust log.
(function () {
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
      await inv("keychain_set", { name: "probe4", value: v });
      var back = await inv("keychain_get", { name: "probe4" });
      out.keychain = { ok: back === v };
    } catch (e) { out.keychain = { ok: false, error: String(e) }; }
    var banner = document.createElement("pre");
    banner.style.cssText = "position:fixed;left:0;right:0;bottom:0;margin:0;padding:8px;background:#111;color:#0f0;font:12px monospace;z-index:99999;white-space:pre-wrap";
    banner.textContent = "PROBE4 " + JSON.stringify(out);
    document.body.appendChild(banner);
    inv("probe_report", { json: JSON.stringify(out) });
  });
})();
