// The transport's cancellation and redirect gates, against a real local
// server. A fetch resolves at the headers, so a deadline or a caller's abort
// that expired at them would miss a body that stalls afterward; these tests
// hold a body open mid-stream and prove both still land. The redirect gate
// proves the transport refuses to follow one, on the server's own request
// count. The recorded harness (test/_recorded.mjs) answers without a
// network; this file is the one place the suite dials, and it dials only a
// loopback server bound for the duration of one test.
import { test } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import * as api from "../src/index.ts";

// A loopback server that sends its headers and the first fragment of the
// body, then stalls: the socket stays open mid-body, the shape a slow or
// wedged peer presents after its headers.
async function stallServer() {
  const server = http.createServer((_req, res) => {
    res.on("error", () => {}); // the aborted client closes the socket mid-write
    res.writeHead(200, { "content-type": "application/json" });
    res.write('{"partial":');
    // No end: the body stalls here.
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  return server;
}

const baseUrlOf = (server) => `http://127.0.0.1:${server.address().port}`;

const shutdown = async (server) => {
  server.closeAllConnections?.();
  await new Promise((resolve) => server.close(resolve));
};

test("a body that stalls after the headers still lands the timeout", async () => {
  const server = await stallServer();
  try {
    const client = new api.ApiClient({ baseUrl: baseUrlOf(server) });
    const result = await api.tenantAuth.tenantGetMe(client, { timeoutMs: 50 });
    assert.equal(result.ok, false);
    if (!result.ok) assert.match(result.error, /the request timed out after 50 ms/);
  } finally {
    await shutdown(server);
  }
});

test("a body that stalls after the headers still lands the caller's abort", async () => {
  const server = await stallServer();
  try {
    const controller = new AbortController();
    const client = new api.ApiClient({ baseUrl: baseUrlOf(server) });
    const pending = api.tenantAuth.tenantGetMe(client, { signal: controller.signal });
    setTimeout(() => controller.abort(), 30);
    const result = await pending;
    assert.equal(result.ok, false);
    if (!result.ok) assert.match(result.error, /the request was aborted/);
  } finally {
    await shutdown(server);
  }
});

test("a redirect is refused, never followed", async () => {
  let requests = 0;
  const server = http.createServer((_req, res) => {
    requests++;
    res.writeHead(302, { location: "https://elsewhere.example/after-the-redirect" });
    res.end();
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  try {
    const client = new api.ApiClient({ baseUrl: baseUrlOf(server) });
    const result = await api.tenantAuth.tenantGetMe(client);
    assert.equal(result.ok, false);
    if (!result.ok) assert.match(result.error, /transport error/);
    assert.equal(requests, 1, "the redirect target was never dialed");
  } finally {
    await shutdown(server);
  }
});
