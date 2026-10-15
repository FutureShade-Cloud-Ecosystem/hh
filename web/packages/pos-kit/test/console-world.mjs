// The console legs' world router (item 7d): ONE origin for the fake FB Console, the core's auth routes and
// an app's own stub, with the credentialed CORS a real cross origin core would answer. The front door's stub
// (apps/front-door/test/stub/demo-stub.mjs) already is the fake console and the three auth routes; an app's
// stub already is that app's data routes. This router only sends each request to the right one:
//   /console/..., /api/v1/auth/..., /config.json, /_stub/console*, /_stub/config, /_stub/reset -> the auth stub
//   everything else                                                                              -> the app's stub
// and answers preflights, so a page on any local port can call it with credentials, as a page on one
// futurebuild.ai host calls the core on another. Test scaffolding only; every value is fake.
//
// CW_PORT is this router's port; CW_AUTH_PORT and CW_APP_PORT are the two stubs'.
import { createServer, request as httpRequest } from "node:http";

const port = Number(process.env.CW_PORT);
const authPort = Number(process.env.CW_AUTH_PORT);
const appPort = Number(process.env.CW_APP_PORT);

const AUTH_PREFIXES = ["/console/", "/api/v1/auth/", "/_stub/console", "/_stub/config", "/_stub/reset"];

function cors(origin, headers) {
  if (typeof origin !== "string" || origin === "") return headers;
  return { ...headers, "access-control-allow-origin": origin, "access-control-allow-credentials": "true", vary: "Origin" };
}

const server = createServer((req, res) => {
  const origin = req.headers.origin;
  if (req.url === "/healthz") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end('{"ok":true}');
    return;
  }
  if (req.method === "OPTIONS") {
    res.writeHead(
      204,
      cors(origin, {
        "access-control-allow-methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
        "access-control-allow-headers": String(req.headers["access-control-request-headers"] ?? "content-type"),
        "access-control-max-age": "60",
      }),
    );
    res.end();
    return;
  }
  const path = String(req.url ?? "/").split("?")[0];
  const toAuth = AUTH_PREFIXES.some((prefix) => path === prefix || path.startsWith(prefix));
  const upstream = httpRequest(
    { host: "127.0.0.1", port: toAuth ? authPort : appPort, method: req.method, path: req.url, headers: { ...req.headers, host: `127.0.0.1:${toAuth ? authPort : appPort}` } },
    (answer) => {
      res.writeHead(answer.statusCode ?? 502, cors(origin, answer.headers));
      answer.pipe(res);
    },
  );
  upstream.on("error", () => {
    res.writeHead(502, cors(origin, { "content-type": "application/json" }));
    res.end('{"error":"the stub behind the router is not answering"}');
  });
  req.pipe(upstream);
});

server.listen(port, "127.0.0.1", () => {
  process.stdout.write(`console world router on ${port}\n`);
});
