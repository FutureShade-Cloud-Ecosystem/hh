// UX-127: the page copy of a demo page says so; a dealer's own box (one served a console config) does not.
/** The words a demo page adds to its page copy, with the space that joins them; a dealer's own box adds nothing. */
export function demoNote(served: unknown): string {
  return served === null ? " Demo data only." : "";
}
