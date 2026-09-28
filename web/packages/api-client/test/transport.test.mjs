// The transport's cancellation and redirect gates, against a real local
// server. A fetch resolves at the headers, so a deadline or a caller's abort
// that expired at them would miss a body that stalls afterward; these tests
// hold a body open mid-stream and prove both still land. The redirect gate
// proves the transport refuses to follow one, on the second server's own
// request count. The recorded harness (test/_recorded.mjs) answers without a
// network; this file is the one place the suite dials, and it dials only a
// loopback server bound for the duration of one test.
//
// Every test carries its own timeout: a regression that leaves a request
// pending forever (a deadline cleared at the headers, a header provider that
// never settles) fails its one test at that timeout instead of hanging the
// whole suite after it.
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

test("a body that stalls after the headers still lands the timeout", { timeout: 5000 }, async () => {
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

test("a body that stalls after the headers still lands the caller's abort", { timeout: 5000 }, async () => {
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

test("a redirect is refused, never followed", { timeout: 5000 }, async () => {
  // The redirect points at a SECOND loopback server this test owns, so the
  // refusal is observable: with redirect: "follow" the platform would dial
  // the second server and its request count would move, failing the last
  // assertion. A redirect to an unobservable host cannot fail this way.
  let received = 0;
  const target = http.createServer((_req, res) => {
    received++;
    res.writeHead(200, { "content-type": "application/json" });
    res.end('{"captured":true}');
  });
  await new Promise((resolve) => target.listen(0, "127.0.0.1", resolve));
  let redirects = 0;
  const origin = http.createServer((_req, res) => {
    redirects++;
    res.writeHead(302, { location: `${baseUrlOf(target)}/after-the-redirect` });
    res.end();
  });
  await new Promise((resolve) => origin.listen(0, "127.0.0.1", resolve));
  try {
    const client = new api.ApiClient({ baseUrl: baseUrlOf(origin) });
    const result = await api.tenantAuth.tenantGetMe(client);
    assert.equal(received, 0, "the redirect target never received a request");
    assert.equal(redirects, 1, "the origin answered with the redirect exactly once");
    assert.equal(result.ok, false);
    if (!result.ok) assert.match(result.error, /transport error/);
  } finally {
    await shutdown(origin);
    await shutdown(target);
  }
});

// The header provider's call sits inside the deadline (it matters for auth,
// whose provider it is): a provider that never settles resolves the timeout
// Result at the deadline instead of hanging the caller past it, and a
// caller's abort lands while the provider is still pending. Neither test
// dials: the provider hangs before the transport would.
test("a header provider that never settles still lands the timeout", { timeout: 5000 }, async () => {
  const client = new api.ApiClient({
    baseUrl: "https://hh-core.example",
    headers: () => new Promise(() => {}),
  });
  const result = await api.tenantAuth.tenantGetMe(client, { timeoutMs: 50 });
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /the request timed out after 50 ms/);
});

test("a caller's abort lands while the header provider is still pending", { timeout: 5000 }, async () => {
  const controller = new AbortController();
  const client = new api.ApiClient({
    baseUrl: "https://hh-core.example",
    headers: () => new Promise(() => {}),
  });
  const pending = api.tenantAuth.tenantGetMe(client, { signal: controller.signal });
  setTimeout(() => controller.abort(), 30);
  const result = await pending;
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /the request was aborted/);
});
