// Manager Home's Vite entry: one bundle for this one micro-app. The design
// system's token sheet rides it, and the app root's registration is the only
// side effect beside it.
import "@hardscapehouse/design-system/src/tokens.css";
import { loadAppRuntime } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";

// The served config decides the app's custody before the element is built, so the app's root (exported by index.ts) loads after it.
void loadAppRuntime().then(() => import("./index.js"));
