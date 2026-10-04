// The Public Desk's Vite entry: one bundle for this one micro-app. The design
// system's token sheet rides it (the base vocabulary the page renders
// through), and the page element's registration is the only side effect. No
// session code, no shell, no other surface: the token in the link is the
// credential.
import "@hardscapehouse/design-system/src/tokens.css";
import "./quote-public.js";
