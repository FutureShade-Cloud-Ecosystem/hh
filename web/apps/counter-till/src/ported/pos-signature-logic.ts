// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-signature-logic.ts; adapted for HH.

// pos-signature-logic.ts: the pure, DOM-free rules behind the DIB-386 typed-name attestation.
//
// The component (pos-signature.ts) is Lit and DOM-bound, so the RULE that decides whether an
// attestation may be submitted lives here and is unit-tested (the _pos.ts pure-helper idiom). The
// old stub had no rule worth testing: `@pointerdown=${() => (this.drawn = true)}`: one touch
// anywhere on a box enabled "Capture", and the sealed payload was a placeholder string.

/** What the operator has entered so far. */
export interface AttestationDraft {
  typedName: string;
  affirmed: boolean;
  busy: boolean;
}

/** The typed name as it is sent and displayed: surrounding whitespace is not part of a name. */
export function normalizeTypedName(name: string): string {
  return name.trim();
}

/**
 * DIB-386: THE GATE. An attestation needs BOTH a typed name and an explicit affirmative, and the
 * affirmative is a separate act rather than an implied consequence of typing (the DIB-57 ruling; the
 * server refuses the same two cases in TypedNameSignatureProvider.CaptureTyped). The client gate
 * exists so the operator is never invited to submit something the server will reject: it is not
 * the authority, it is the courtesy.
 */
export function canSubmitAttestation(d: AttestationDraft): boolean {
  return normalizeTypedName(d.typedName).length > 0 && d.affirmed && !d.busy;
}

/**
 * Why the button is disabled, in words the person at the counter can act on. Empty string when the
 * attestation is ready: a hint shown next to an enabled button is noise.
 */
export function attestationBlockedReason(d: AttestationDraft): string {
  if (d.busy) return 'Signing…';
  if (normalizeTypedName(d.typedName).length === 0) return 'Type the signer’s full name.';
  if (!d.affirmed) return 'Tick the confirmation to sign.';
  return '';
}
