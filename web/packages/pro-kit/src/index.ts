// The Pro kit's public surface: the seam, the client, the gate, the words, the
// chips, the sign in screen and the frame. Elements register on import of their
// own module (frame.ts, signin.ts); this entry imports them so an app needs one line.
export { CAPABILITIES, TENANT_ROLES, ROLE_LABELS, can, capabilitiesFor, roleLabel } from "./capabilities.js";
export type { Capability } from "./capabilities.js";
export { InMemoryTenantSession, memoryStorage } from "./session.js";
export type { TenantSession, TenantProfile, TenantAnswer, TenantSignInAdapter } from "./session.js";
export { tenantClient } from "./client.js";
export { realExchangeAdapter } from "./real-exchange.js";
export { NO_FIGURE, money, totalsLine, qtyWithUnit, houseDay, monoStyles } from "./format.js";
export { STAGES, STAGE_WORDS, statusWords, statusTone, chipStyles, stageChip, statusChip } from "./chips.js";
export { stageOfOrder, stageOfProject, stageOfQuote, trackingOf, TRACKING_WORDS, TRACKING_STATES } from "./board.js";
export type { Tracking } from "./board.js";
