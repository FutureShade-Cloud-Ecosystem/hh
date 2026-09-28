// The public surface of @hh/auth: session custody (memory-only JWT, the
// throw-safe mirror that carries the display profile only), the device lock
// (a screen courtesy that sends no audit pair), and the wiring into the
// api-client (a staff provider and a tenant provider). src/testing.ts is the
// test entry point (test doubles whose every value is fake); the exports map
// in package.json keeps it behind the ./testing subpath so it deliberately
// does not ride this entry into a browser bundle.
export * from "./session.js";
export * from "./lock.js";
export * from "./client.js";
