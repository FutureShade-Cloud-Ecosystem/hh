// Ported from hardscapeos_dibbits master 87d7941b app/src/_glyph.ts; adapted for HH.

import type { SVGTemplateResult } from 'lit';

/**
 * What a module's icon can be.
 *
 * A STRING: a namespaced registry id (`lucide:<name>`, `pencil:<name>`; HH-175, DIB-ICONS-1.3,
 * which replaced the emoji this union used to carry), or a DRAWING for the one module with a
 * mark of its own (TanyaAI, see _tanya-mark.ts).
 *
 * WIDENED RATHER THAN REPLACED, and that is the whole design. Every template that already did
 * `${item.glyph}` keeps working untouched; the places that draw an icon resolve a string through
 * the consumer layer (`_icon-consumer.ts`: id lookup, the checkpoint A defaults, never an empty
 * slot) and pass a drawing straight through. A string in an icon slot is an ID, never a
 * character: a character is only an icon if the machine has a font that covers it.
 *
 * It lives in its own file because many elements need it and none of them may import another:
 * app-shell mounts the switcher and the rail, so a type exported from app-shell would be a cycle.
 */
export type ModuleGlyph = string | SVGTemplateResult;
