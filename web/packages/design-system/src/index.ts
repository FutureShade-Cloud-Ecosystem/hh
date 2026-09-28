// @hh/design-system: the public surface of the package. Consumers import the
// kits, the state system, the primitives and the tokens entry; the icon seam is
// documentation (src/icons/README.md) until the ERP icon registry lands.

// There is deliberately no raw-markup export on this surface: markup can only
// be made by html() inside the package (src/render.ts), whose trust brand is a
// module-private symbol no caller can forge.
export { HHElement, html, css, renderTemplate, defineElement } from './render.js';
export {
  KIT_STATES,
  STATE_META,
  stateIndicator,
  kitShell,
  stateStyles,
} from './state.js';
export type { KitState, DesignedState } from './state.js';
export { baseStyles, button, chip, row, spinner } from './primitives.js';
export { STATE_ICONS, icon } from './state-icons.js';

export { ScanConfirmKit } from './kits/scan-and-confirm.js';
export { ListDetailKit } from './kits/list-detail.js';
export { GuidedFlowKit } from './kits/guided-flow.js';
export { DashboardKit } from './kits/dashboard.js';
export { InboxKit } from './kits/inbox.js';
