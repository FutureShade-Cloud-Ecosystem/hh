// The Counter Till's Vite entry: one bundle for this one micro-app. Two token
// sheets ride it: the design system's vocabulary (the base layer the internal
// apps share), and the ported surface's own sheet, rebased onto those tokens
// (the kit's styles/tokens.css: every --hos-* role the ported components
// render through; the sheet moved to @hardscapehouse/pos-kit with the shared
// pieces). The app root's registration and the ported surface's element
// registrations are the only side effects.
import "@hardscapehouse/design-system/src/tokens.css";
import "@hardscapehouse/pos-kit/src/ported/styles/tokens.css";
import { applyTenantTitle } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import { loadAppRuntime } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";

// The served config decides the app's custody before the element is built, so the element loads after it.
void loadAppRuntime().then(() => import("./app-root.js"));

applyTenantTitle("Counter Till");
