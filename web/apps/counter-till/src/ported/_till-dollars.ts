// The till's money word for the sites that read a figure out of the pos-kit client: dollars with no
// currency mark ($1,234.00), the magnitude only, the caller owning any sign (row 980). It is the same
// shape as pos-kit's `money` minus the "CA" prefix; when the shared dollar formatter lands in pos-kit
// beside centsAmount, this becomes a one line re-export of it.
import { centsAmount } from '@hardscapehouse/pos-kit/src/ported/_money-format.js';

export function money(cents: number): string {
  return `$${centsAmount(cents)}`;
}
