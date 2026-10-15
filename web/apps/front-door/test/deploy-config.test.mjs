// The demo image supplies its own config.json (mode demo): the front door has no
// default mode, so an image without the file would show the configuration error
// page. The file is served by its own exact handle behind the same phrase gate,
// with no try_files, so a missing file is a 404 ("missing") and never the page's
// HTML ("damaged").
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "..", "deploy", "demo");
const dockerfile = readFileSync(join(root, "Dockerfile"), "utf8");
const caddy = readFileSync(join(root, "Caddyfile.template"), "utf8");
const exitTest = readFileSync(join(root, "exit-test.sh"), "utf8");

test("the image writes config.json with mode demo into the front door's root", () => {
  assert.match(dockerfile, /printf '\{"mode":"demo"\}' > \/out\/root\/config\.json/);
  assert.ok(
    dockerfile.indexOf("/out/root/config.json") > dockerfile.indexOf("vite build --outDir /out/root"),
    "written after the front door build, which empties its output directory",
  );
});

test("config.json has its own handle, gated, with no try_files fallback", () => {
  const at = caddy.indexOf("handle /config.json {");
  assert.ok(at >= 0, "an exact handle for /config.json");
  const body = caddy.slice(at, caddy.indexOf("\n}\n", at));
  assert.match(body, /basic_auth/);
  assert.match(body, /root \* \/srv\/app/);
  assert.match(body, /file_server/);
  assert.equal(/try_files/.test(body), false, "a missing file must be a 404, not the page");
  assert.ok(at < caddy.indexOf("\nhandle {\n"), "before the catch all");
});

test("the exit test proves the served image carries config.json with mode demo", () => {
  assert.match(exitTest, /config\.json/);
  assert.match(exitTest, /"mode":"demo"|mode.*demo/);
});
