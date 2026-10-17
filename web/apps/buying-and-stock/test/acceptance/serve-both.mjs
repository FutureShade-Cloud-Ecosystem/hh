// The card leg's stand-in for the demo image's one origin: it builds the front
// door and the Buying and Stock app the way the image's Dockerfile does (the
// front door at /, the app in demo mode under /buying-and-stock/), serves both
// from one port so the front door's session storage handoff reaches the app as
// it does on the box, and proxies /api and /_stub to the app's stub (which
// mints the Procurement manager session in the core's shape). A local harness
// only: the real walk is the image's exit test.
//
//   BS_ACC_PORT   this server's port
//   BS_ACC_STUB   the stub's port
//   BS_ACC_OUT    a folder for the two builds, outside the app's tree (the design
//                 system's gates scan every dependent's tree, built bundles included)
import { execFileSync } from "node:child_process";
import { createServer, request as httpRequest } from "node:http";
import { readFile, mkdir } from "node:fs/promises";
import { dirname, extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const appsDir = join(here, "..", "..", "..");
const port = Number(process.env.BS_ACC_PORT ?? 0);
const stubPort = Number(process.env.BS_ACC_STUB ?? 0);
const out = process.env.BS_ACC_OUT ?? "";
if (port === 0 || stubPort === 0 || out === "") {
  process.stderr.write("serve-both: BS_ACC_PORT, BS_ACC_STUB and BS_ACC_OUT are required\n");
  process.exit(1);
}

await mkdir(out, { recursive: true });
const vite = (app, args) => execFileSync("npx", ["--no-install", "vite", "build", ...args], { cwd: join(appsDir, app), stdio: "ignore" });
vite("front-door", ["--outDir", join(out, "root"), "--emptyOutDir"]);
vite("buying-and-stock", ["--mode", "demo", "--base=/buying-and-stock/", "--outDir", join(out, "buying-and-stock"), "--emptyOutDir"]);

const MIME = new Map([
  [".html", "text/html; charset=utf-8"],
  [".js", "text/javascript; charset=utf-8"],
  [".css", "text/css; charset=utf-8"],
  [".svg", "image/svg+xml"],
  [".png", "image/png"],
  [".woff2", "font/woff2"],
  [".json", "application/json"],
]);

function proxy(req, res) {
  const upstream = httpRequest({ host: "127.0.0.1", port: stubPort, path: req.url, method: req.method, headers: req.headers }, (answer) => {
    res.writeHead(answer.statusCode ?? 502, answer.headers);
    answer.pipe(res);
  });
  upstream.on("error", () => {
    res.writeHead(502);
    res.end("bad gateway");
  });
  req.pipe(upstream);
}

async function serve(req, res) {
  const url = new URL(req.url ?? "/", "http://127.0.0.1");
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/_stub/") || url.pathname === "/healthz") return proxy(req, res);
  // The image writes {"mode":"demo"} at /config.json (deploy/demo/Dockerfile); without this the index.html
  // fallback answers HTML, the front door reads it as a damaged config and draws no role card.
  if (url.pathname === "/config.json") {
    res.writeHead(200, { "Content-Type": "application/json" });
    return res.end('{"mode":"demo"}');
  }
  const buying = url.pathname.startsWith("/buying-and-stock/");
  const root = join(out, buying ? "buying-and-stock" : "root");
  const rel = normalize(buying ? url.pathname.slice("/buying-and-stock".length) : url.pathname).replace(/^(\.\.[/\\])+/, "");
  const candidates = [join(root, rel), join(root, "index.html")];
  for (const candidate of candidates) {
    const file = candidate.endsWith("/") ? join(candidate, "index.html") : candidate;
    try {
      const body = await readFile(file);
      res.writeHead(200, { "Content-Type": MIME.get(extname(file)) ?? "application/octet-stream" });
      return res.end(body);
    } catch {
      /* try the fallback */
    }
  }
  res.writeHead(404);
  res.end("not found");
}

createServer((req, res) => void serve(req, res)).listen(port, "127.0.0.1", () => {
  process.stdout.write(`serve-both on ${port}\n`);
});
