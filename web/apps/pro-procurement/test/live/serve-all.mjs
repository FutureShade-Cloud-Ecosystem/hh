// The live legs' stand-in for the demo image's one origin: it builds the front door and the three Pro apps the
// way the image's Dockerfile does (the front door at /, each Pro app in demo mode under its own prefix), serves
// them from one port, and proxies /api and /_stub to the shared tenant stub (which answers the demo routes and
// the tenant routes in the core's shape). A local harness only: the real walk is the image's exit test.
//
//   PRO_LIVE_PORT   this server's port
//   PRO_LIVE_STUB   the stub's port
//   PRO_LIVE_OUT    a folder for the builds, outside the apps' trees (the design system's gates scan every
//                   dependent's tree, built bundles included)
import { execFileSync } from "node:child_process";
import { createServer, request as httpRequest } from "node:http";
import { readFile, mkdir } from "node:fs/promises";
import { dirname, extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const appsDir = join(here, "..", "..", "..");
const port = Number(process.env.PRO_LIVE_PORT ?? 0);
const stubPort = Number(process.env.PRO_LIVE_STUB ?? 0);
const out = process.env.PRO_LIVE_OUT ?? "";
if (port === 0 || stubPort === 0 || out === "") {
  process.stderr.write("serve-all: PRO_LIVE_PORT, PRO_LIVE_STUB and PRO_LIVE_OUT are required\n");
  process.exit(1);
}

const PREFIXES = ["pro-procurement", "pro-pay", "pro-field"];

await mkdir(out, { recursive: true });
const vite = (app, args) => execFileSync("npx", ["--no-install", "vite", "build", ...args], { cwd: join(appsDir, app), stdio: "ignore" });
vite("front-door", ["--outDir", join(out, "root"), "--emptyOutDir"]);
for (const app of PREFIXES) vite(app, ["--mode", "demo", `--base=/${app}/`, "--outDir", join(out, app), "--emptyOutDir"]);

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
  const app = PREFIXES.find((name) => url.pathname.startsWith(`/${name}/`));
  const root = join(out, app ?? "root");
  const rel = normalize(app === undefined ? url.pathname : url.pathname.slice(`/${app}`.length)).replace(/^(\.\.[/\\])+/, "");
  for (const candidate of [join(root, rel), join(root, "index.html")]) {
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
  process.stdout.write(`serve-all on ${port}\n`);
});
