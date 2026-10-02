// The Counter Till's Vite entry: one bundle for this one micro-app. Two token
// sheets ride it: the design system's vocabulary (the base layer the internal
// apps share), and the ported surface's own sheet, rebased onto those tokens
// (src/ported/styles/tokens.css: every --hos-* role the ported components
// render through). The app root's registration and the ported surface's
// element registrations are the only side effects.
import "@hardscapehouse/design-system/src/tokens.css";
import "./ported/styles/tokens.css";
import "./app-root.js";
