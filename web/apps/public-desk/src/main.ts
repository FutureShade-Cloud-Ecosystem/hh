// The Public Desk's Vite entry: one bundle for this one micro-app. The design
// system's token sheet rides it (the base vocabulary the page renders
// through), and the page element's registration is the only side effect. No
// session code, no shell, no other surface: the token in the link is the
// credential.
// The face ships with that same sheet: its @font-face rules register the
// vendored Inter (the type pass, row 942), so this app carries no fonts of
// its own any more and a customer document renders the same face as every
// other surface, self-hosted from the bundle, never a font host.
import "@hardscapehouse/design-system/src/tokens.css";
import "./quote-public.js";
