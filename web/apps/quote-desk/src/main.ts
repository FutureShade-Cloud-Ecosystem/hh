// The Quote Desk's Vite entry: one bundle for this one micro-app. Two token
// sheets ride it: the design system's vocabulary (the base layer the internal
// apps share), and the ported surface's own sheet, rebased onto those tokens
// (the kit's styles/tokens.css: every --hos-* role the shared components
// render through; this app's own styles read the same roles through the
// fallbacks they carry). The app root's registration and the shared
// components' element registrations are the only side effects.
import "@hardscapehouse/design-system/src/tokens.css";
// The self-hosted faces the token sheet names (src/fonts.css): without them
// --hh-font-ui's first name never resolves and every user sees a fallback.
import "./fonts.css";
import "@hardscapehouse/pos-kit/src/ported/styles/tokens.css";
import { applyTenantTitle } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import "./index.js";
import { loadAppRuntime } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";

// The served config decides the app's custody before the element is built, so the element loads after it.
void loadAppRuntime().then(() => import("./app-root.js"));

applyTenantTitle("Quote Desk");
