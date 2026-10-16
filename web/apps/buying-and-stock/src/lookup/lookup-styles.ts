// The Sales stock lookup's surface classes (change 5, board bs-stock-lookup-sales-search-a-product):
// a two card split that stacks on a tablet, list rows with the product's thumb and its availability
// in words before it is a colour, and the picked row carried by the accent. Every value a design
// system token, so the charcoal theme renders the same wall the light one does; no hardcoded field
// colour anywhere (the navy search field on the charcoal board was the board's own fault, entry 54).
import { css } from "lit";

export const lookupStyles = css`
  .lookup { display: grid; grid-template-columns: minmax(0, 24rem) minmax(0, 1fr); gap: var(--hh-s-16); align-items: start; }
  .lookup > * { min-width: 0; }

  .lrow { display: flex; gap: var(--hh-s-12); width: 100%; align-items: center; text-align: start; padding: var(--hh-s-10) var(--hh-s-8); background: transparent; border: 1px solid transparent; border-radius: var(--hh-radius-control); cursor: pointer; font: inherit; color: inherit; }
  .lrow + .lrow { margin-top: var(--hh-s-4); }
  .lrow:hover { background: var(--hh-field); }
  .lrow:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .lrow-picked, .lrow[aria-current="true"] { border-color: var(--hh-accent-text); background: color-mix(in srgb, var(--hh-accent) 14%, transparent); }
  .lrow-words { display: grid; gap: 2px; min-width: 0; }
  .lrow-name { font-weight: 700; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .lrow-avail { font-weight: 700; }
  .lrow-avail.stop { color: var(--hh-state-stop); }
  .lrow-avail.good { color: var(--hh-state-good); }
  .lrow-thumb { flex: 0 0 auto; width: 44px; height: 44px; border-radius: var(--hh-radius-control); background: var(--hh-field); border: 1px solid var(--hh-line); overflow: hidden; display: inline-flex; align-items: center; justify-content: center; }
  .lrow-thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }

  .lookup-headline { display: flex; gap: var(--hh-s-12); align-items: center; min-width: 0; }
  .lookup-thumb { width: 56px; height: 56px; }
  .lookup-headline h2 { margin: 0; }
  .lookup-headline p { margin: 2px 0 0; }
  .lookup-order { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 100%; }

  @media (max-width: 60rem) {
    .lookup { grid-template-columns: minmax(0, 1fr); }
  }
`;
