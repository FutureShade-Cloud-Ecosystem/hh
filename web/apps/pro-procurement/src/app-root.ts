// The app root: the Pro kit's shared base wired to this app's key, its sign in
// adapter and its landing surface (landing.ts). The sign in adapter is the
// stub only where Vite serves a development build (import.meta.env.DEV is
// replaced at build time, so a production bundle carries only the named seam,
// realExchangeAdapter, which refuses in plain words until the auth lead's go
// fills it). The Pro frame is the kit's thin element; the shells cycle owns the
// real shell and replaces it without touching this file.
import { realExchangeAdapter, stubAdapter, type ProAppConfig } from "@hardscapehouse/pro-kit";
import { ProAppBase } from "@hardscapehouse/pro-kit/src/app-base.js";
import { defineElement } from "@hardscapehouse/design-system/src/base.js";
import { landing, type Item } from "./landing.js";

export class ProProcurementApp extends ProAppBase<Item> {
  protected config(): ProAppConfig<Item> {
    return { app: "procurement", adapter: import.meta.env.DEV ? stubAdapter() : realExchangeAdapter(), landing };
  }
}

defineElement("pro-procurement-app", ProProcurementApp);
