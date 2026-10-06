// The front door's Vite entry: one bundle for this one micro-app. The design
// system's token sheet is the only sheet it rides (the front door draws no
// ported surface, so there is no second sheet), and the app root's
// registration is the only side effect.
import "@hardscapehouse/design-system/src/tokens.css";
import "./app-root.js";
