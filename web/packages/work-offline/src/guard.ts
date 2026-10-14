// The guard entry (`@hardscapehouse/work-offline/guard`): the no signal guard
// without the offline store. An app that is online only, or whose money
// actions only need the honest refusal, imports this and ships no store, no
// sync driver and no sqlite engine.
export * from "./signal.js";
