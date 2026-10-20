// The public surface of @hardscapehouse/shell-chrome: the pure shell state
// (pins per user, the switcher model, the sync chip words) and the chrome
// elements the shells mount, offline banner, conflict cards, header, rail
// and update prompt. The shells wire the elements to the state module and to
// the offline store and auth packages; this surface carries no template tag: markup is built by Lit's html and css tagged
// templates inside the elements, and the design system's gates hold that in
// this package as in any dependent of it.
//
// There is deliberately no storage seam on this surface: the shell injects
// its own (PinStorage) so the browser copy and the Tauri shells carry their
// own persistence, and the package never reaches for a platform global.
export * from './shell-state.js';
export * from './framed-band.js';
export * from './shell-banner.js';
export * from './shell-cards.js';
export * from './shell-header.js';
export * from './shell-rail.js';
export * from './shell-update.js';
