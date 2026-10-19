// The role app's Vite entry: one bundle for this one micro-app. Two token
// sheets ride it: the design system's vocabulary and the ported surface's
// sheet. The app root's registration and the shared components' element
// registrations are the only side effects.
import "@hardscapehouse/design-system/src/tokens.css";
import { applyTenantTitle } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import { loadAppRuntime } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";

// The served config decides the app's custody before the element is built, so the app's root (exported by index.ts) loads after it.
void loadAppRuntime().then(() => import("./index.js"));

applyTenantTitle("Dispatch and Load Building");
