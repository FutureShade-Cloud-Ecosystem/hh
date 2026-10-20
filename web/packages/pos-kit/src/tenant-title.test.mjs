// Unit tests for the tenant-driven title helper.
import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import {
  applyTenantTitle,
  readDealerName,
  setNeutralTitle,
  __resetTenantTitleCache,
} from "./tenant-title.js";

let originalFetch;
let originalDocument;
let originalLocation;

beforeEach(() => {
  originalFetch = globalThis.fetch;
  originalDocument = globalThis.document;
  originalLocation = globalThis.location;
  globalThis.document = { title: "" };
  globalThis.location = { origin: "http://localhost" };
  __resetTenantTitleCache();
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  globalThis.document = originalDocument;
  globalThis.location = originalLocation;
});

function okConfig(dealerName) {
  const body = JSON.stringify({
    id: "demo-dealer",
    dealer_name: dealerName,
    logo_url: "",
    primary_color: "#000000",
    support_email: "",
    support_phone: "",
  });
  return () =>
    Promise.resolve({
      status: 200,
      ok: true,
      headers: new Map([["content-type", "application/json"]]),
      json: () => Promise.resolve(JSON.parse(body)),
      text: () => Promise.resolve(body),
    });
}

function failingConfig(status = 503) {
  return () =>
    Promise.resolve({
      status,
      ok: false,
      headers: new Map([["content-type", "application/json"]]),
      json: () => Promise.resolve({ error: "unavailable" }),
      text: () => Promise.resolve(JSON.stringify({ error: "unavailable" })),
    });
}

test("a successful config read sets '<dealer> · <app>'", async () => {
  globalThis.fetch = okConfig("Gable Landscape Supply");
  await applyTenantTitle("Counter Till");
  assert.equal(globalThis.document.title, "Gable Landscape Supply · Counter Till");
});

test("the quote option sets 'Your quote from <dealer>'", async () => {
  globalThis.fetch = okConfig("Gable Landscape Supply");
  await applyTenantTitle("Your quote", { quote: true });
  assert.equal(globalThis.document.title, "Your quote from Gable Landscape Supply");
});

test("a failed config read keeps the neutral app title", async () => {
  globalThis.fetch = failingConfig();
  await applyTenantTitle("Counter Till");
  assert.equal(globalThis.document.title, "Counter Till");
});

test("a failed config read with the quote option keeps 'Your quote'", async () => {
  globalThis.fetch = failingConfig();
  await applyTenantTitle("Your quote", { quote: true });
  assert.equal(globalThis.document.title, "Your quote");
});

test("the config is read only once across multiple calls", async () => {
  let calls = 0;
  globalThis.fetch = (...args) => {
    calls += 1;
    return okConfig("Gable Landscape Supply")(...args);
  };
  await applyTenantTitle("Counter Till");
  await applyTenantTitle("Quote Desk");
  assert.equal(calls, 1);
  assert.equal(globalThis.document.title, "Gable Landscape Supply · Quote Desk");
});

test("a fetch that throws keeps the neutral title", async () => {
  globalThis.fetch = () => Promise.reject(new Error("network down"));
  await applyTenantTitle("Drive");
  assert.equal(globalThis.document.title, "Drive");
});

test("setNeutralTitle sets the static title synchronously", () => {
  setNeutralTitle("Work");
  assert.equal(globalThis.document.title, "Work");
});

test("setNeutralTitle with the quote option sets 'Your quote'", () => {
  setNeutralTitle("Your quote", { quote: true });
  assert.equal(globalThis.document.title, "Your quote");
});

test("the neutral title is set synchronously before the config read lands", async () => {
  globalThis.fetch = (...args) =>
    new Promise((resolve) =>
      setTimeout(() => resolve(okConfig("Gable Landscape Supply")(...args)), 10)
    );
  const pending = applyTenantTitle("Counter Till");
  assert.equal(globalThis.document.title, "Counter Till");
  await pending;
  assert.equal(globalThis.document.title, "Gable Landscape Supply · Counter Till");
});

test("a failed config read is retried on the next call", async () => {
  let calls = 0;
  globalThis.fetch = (...args) => {
    calls += 1;
    if (calls === 1) return failingConfig()(...args);
    return okConfig("Gable Landscape Supply")(...args);
  };
  await applyTenantTitle("Counter Till");
  assert.equal(globalThis.document.title, "Counter Till");
  await applyTenantTitle("Counter Till");
  assert.equal(calls, 2);
  assert.equal(globalThis.document.title, "Gable Landscape Supply · Counter Till");
});

test("readDealerName answers the dealer's name from the config, and an empty word when the read fails", async () => {
  globalThis.fetch = okConfig("Gable Landscape Supply");
  assert.equal(await readDealerName(), "Gable Landscape Supply");
  __resetTenantTitleCache();
  globalThis.fetch = failingConfig();
  assert.equal(await readDealerName(), "");
});

test("readDealerName answers the served dealer and shares one read with the tab title", async () => {
  let reads = 0;
  globalThis.fetch = (...args) => {
    reads += 1;
    return okConfig("Coastal Stone Supply")(...args);
  };
  assert.equal(await readDealerName(), "Coastal Stone Supply");
  assert.equal(await readDealerName(), "Coastal Stone Supply");
  await applyTenantTitle("Counter Till");
  assert.equal(reads, 1);
});

test("readDealerName answers an empty name when the read fails, and a later read retries", async () => {
  globalThis.fetch = failingConfig();
  assert.equal(await readDealerName(), "");
  globalThis.fetch = okConfig("Coastal Stone Supply");
  assert.equal(await readDealerName(), "Coastal Stone Supply");
});
