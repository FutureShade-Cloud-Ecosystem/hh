/// <reference types="vite/client" />
// This app's public surface: the root element and its money logic.
export { ProPayApp } from "./app-root.js";
export { PaySender, classifyPayment, readHostedToken } from "./pay-core.js";
