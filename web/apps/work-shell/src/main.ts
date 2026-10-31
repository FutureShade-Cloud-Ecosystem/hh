// The Work shell host's entry. A host that finds itself inside a frame is a
// desk app asking for the origin root (its own Switch role or sign out): it
// mounts nothing and asks its parent to switch role (src/frames.ts). Anywhere
// else it registers the host element, which the page already carries.
import "@hardscapehouse/design-system/src/tokens.css";
import "./page.css";
import { applyTenantTitle } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import { loadAppRuntime } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { announceSwitchRole, isFramed } from "./frames.js";

applyTenantTitle("Work");

if (isFramed(window)) {
  announceSwitchRole(window);
} else {
  // The served console config (a dealer's own box) is read before the host mounts, so its band can name the dealer.
  void loadAppRuntime().then(() => import("./host-element.js"));
}
