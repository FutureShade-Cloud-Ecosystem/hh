// The Quote Desk's component registrations: importing this module runs each element's own registration side
// effect. The search and add box is registered here and reachable by tag; the workarea's mount of it (one line)
// follows the split list. The guided convert surface is exported beside it.
export * from "./quote-search-add.js";
export { QuoteConvert } from "./quote-convert.js";
