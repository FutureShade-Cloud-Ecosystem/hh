// Manager Home's Vite entry: one bundle for this one micro-app. The design
// system's token sheet rides it, and the app root's registration is the only
// side effect beside it.
import "@hardscapehouse/design-system/src/tokens.css";
import { applyTenantTitle } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import "./index.js";
import "./app-root.js";

applyTenantTitle("Manager Home");
