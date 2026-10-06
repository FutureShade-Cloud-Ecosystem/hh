// Vite's client types: the app's sources read import.meta.env (the demo
// seam's build mode gate in app-root.ts), and this reference is what types
// that read without hand-writing it. The till carries the same file
// (counter-till src/vite-env.d.ts on demo/front-door 385cd8d6).
/// <reference types="vite/client" />
