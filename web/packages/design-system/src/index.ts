// @hardscapehouse/design-system: the public surface of the package. Consumers import the
// kits, the state system, the primitives and the tokens entry; the icon seam is
// documentation (src/icons/README.md) until the ERP icon registry lands.

// There is deliberately no raw-markup export on this surface: markup is built
// by Lit's html and css tagged templates alone, and the library's raw-markup
// escape hatches (the unsafe directives and the static-html module) are
// refused anywhere in this package and in any package that depends on it, by
// the gate in test/no-raw-markup.test.mjs. Their names are not spelled here
// because the gate refuses the mention as well as the import.
//
// There is equally no export of a template tag on this surface: the import
// list (test/_lib/positions.mjs, IMPORT_ALLOW_LIST) allows html and svg from
// lit only, under their own names only, and the two-place rule
// (test/positions.test.mjs) refuses the tag identifier in any export, so a
// re-export hands the tag to no second file, which could otherwise alias it
// again at its own import of this package. A consumer that wants the tag
// imports it from lit directly, under its own name, where the same rules
// hold. css is not a tag and stays.
export { css } from 'lit';
export { HHElement, defineElement, HHRenderError } from './base.js';
export { hhAddress } from './address.js';
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
export { HosAppHeader } from './header.js';

export { ScanConfirmKit } from './kits/scan-and-confirm.js';
export { ListDetailKit } from './kits/list-detail.js';
export { GuidedFlowKit } from './kits/guided-flow.js';
export { DashboardKit } from './kits/dashboard.js';
export { InboxKit } from './kits/inbox.js';
