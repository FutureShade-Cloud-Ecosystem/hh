// The front door's Vite entry: one bundle for this one micro-app. The design
// system's token sheet is the only sheet it rides (the front door draws no
// ported surface, so there is no second sheet), and the app root's
// registration is the only side effect.
import "@hardscapehouse/design-system/src/tokens.css";
import "./page.css";
import { applyTenantTitle } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import "./app-root.js";

// The tab carries the card's own word for the page; "Front Door" is the
// lane's internal name and never reaches a person's screen.
applyTenantTitle("Sign in");
