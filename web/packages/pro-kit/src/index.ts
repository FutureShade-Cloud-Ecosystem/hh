// The Pro kit's public surface of plain logic: the seam, the client, the gate, the words, the chips,
// the surface catalogue and the sign in adapters.
export { CAPABILITIES, TENANT_ROLES, ROLE_LABELS, can, capabilitiesFor, roleLabel } from "./capabilities.js";
export type { Capability } from "./capabilities.js";
export { InMemoryTenantSession, memoryStorage } from "./session.js";
export type { TenantSession, TenantProfile, TenantAnswer, TenantSignInAdapter, SignInRequest, SignInOptions } from "./session.js";
export { signInPlan, signInFromCard, demoRoleFromAddress, otherAppAddress, METHOD_LABELS, DEMO_METHODS } from "./signin-methods.js";
export type { SignInMethod, SignInPlan, SignInMethodChoice, SignInSubmit } from "./signin-methods.js";
export { DemoTenantSignInAdapter, demoTenantAdapter } from "./demo-adapter.js";
export { tenantClient } from "./client.js";
export { realExchangeAdapter } from "./real-exchange.js";
export {
  NO_PRICE,
  NO_COUNT,
  NO_DAY,
  NO_FIGURE,
  money,
  totalsLine,
  qtyWithUnit,
  houseDay,
  houseMoment,
  calendarDay,
  monoStyles,
} from "./format.js";
export { STAGES, STAGE_WORDS, statusWords, statusTone, chipStyles, stageChip, statusChip, trackingChip, trackingTone } from "./chips.js";
export { stageOfOrder, stageOfProject, stageOfQuote, trackingOf, TRACKING_WORDS, TRACKING_STATES } from "./board.js";
export type { Tracking } from "./board.js";
export { PRO_APPS, appOf, surfaceOf, openSurfaces } from "./surfaces.js";
export type { ProApp, ProAppKey, ProSurface } from "./surfaces.js";
export { stubAdapter } from "./stub-adapter.js";
// The elements (app-base, frame, signin, state-view) are not re-exported here: importing one registers a custom
// element, which needs a browser, and this entry must stay loadable in a plain node test. An app imports the base
// by its own path, @hardscapehouse/pro-kit/src/app-base.js. The types are erased, so they are free to name here.
export type { LandingSpec, ProAppConfig } from "./app-base.js";
