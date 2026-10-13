// The Buying and Stock app's areas: each is a desk (a controller that is not an element) with the
// same four doors, so the root element holds a list of them and shows one at a time. The root owns
// the sign in and the client; a desk owns its state, its loads and what each press does.
import type { TemplateResult } from "lit";

export interface AreaDesk {
  /** The first read of the area, run when it is first shown (the purchase orders load at sign in). */
  load(): Promise<void>;
  /** Acts on a control carrying data-action; true when the press was this area's. */
  press(control: Element): boolean;
  /** The area's template. */
  render(): TemplateResult;
  /** After a render: sets starting field values (the position gate allows no bound input value). */
  seed?(set: (name: string, value: string, key?: string) => void): void;
  /** A form field changed (a repeated row's field carries its row key): lets the area keep words under it in step with what is typed. */
  typed?(name: string, value: string, key?: string): void;
}

export interface AreaEntry {
  readonly id: string;
  readonly label: string;
  readonly desk: AreaDesk;
}

/** What every desk is given: a way to repaint and a way to read the text of a form field. */
export interface DeskHost {
  readonly repaint: () => void;
  readonly field: (name: string, key?: string) => string;
  /** The signed in role's code (a read only seat is not offered the controls the wall would refuse). */
  readonly role: () => string;
}
