// Where the card form comes from. The card field is the payment processor's, hosted in a frame of its own, and the app
// only ever receives a token from it. The frame is mounted with sandbox flags for scripts and forms only (see cardFrame in
// views.ts, and the browser leg that pins them), so its origin is opaque to the app. What it sends up is read by pay-core.ts readHostedToken. This build is sandbox only: the one address the app
// can open is the sandbox processor stand-in, served by the Vite dev server alone (import.meta.env.DEV is replaced at
// build time, so a production bundle carries only the refusal below). Wiring a real processor's hosted field is this
// function's one change, made when the sandbox gives way to a dealer's implementation.

/** The hosted field's address, or null where no processor is connected. */
export function hostedFieldAddress(): string | null {
  return import.meta.env.DEV ? "/processor-sandbox/hosted-field.html" : null;
}

export const SANDBOX_NOTE = "Sandbox payments. No money moves.";
export const NO_PROCESSOR_WORDS = "Adding a card opens when the dealer connects a payment processor.";
