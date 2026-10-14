// This app's public surface: the root element and the plain logic its tests
// read. The screens themselves are template functions in views.ts, driven by
// the root.
export { ProFieldApp } from "./app-root.js";
export { liveFieldPort, type FieldPort } from "./port.js";
export { sectionsFor, ordersSurfaceOf, ORDERS_SURFACES, type Section, type SectionRef } from "./sections.js";
export { flowFor, timelineSteps, promisedWords, lateWords, houseToday, fulfillmentWords } from "./timeline.js";
export { orderRowWords, type Phase } from "./views.js";
