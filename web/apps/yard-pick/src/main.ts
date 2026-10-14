// The role app's Vite entry: one bundle for this one micro-app. Two token
// sheets ride it: the design system's vocabulary and the ported surface's
// sheet. The app root's registration and the shared components' element
// registrations are the only side effects.
import "@hardscapehouse/design-system/src/tokens.css";
import { applyTenantTitle } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import "./index.js";
import "./app-root.js";

applyTenantTitle("Yard Pick");
