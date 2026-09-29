// The test entry point of @hardscapehouse/api-client. The generated structural descriptors
// (312 KB of test-time validation data, one descriptor per wire type) are
// deliberately off the generated and public entries: no runtime caller needs
// them, and keeping them here is what lets the package declare sideEffects
// false without risking their tree-shaking stakes in the browser bundle.
// Import this module from tests and gates only.
export * from "./gen/descriptors.js";
