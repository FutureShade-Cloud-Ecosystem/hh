// The public surface of @hh/auth: session custody (memory-only JWT, the
// throw-safe mirror), the device lock, and the wiring into the api-client.
// src/testing.ts is the test entry point (test doubles whose every value is
// fake); it deliberately does not ride this entry into a browser bundle.
export * from "./session.js";
export * from "./lock.js";
export * from "./client.js";
