// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-signature-api.ts; adapted for HH.

// pos-signature-api.ts: the DIB-386 typed-attestation call.
//
// A NEW route rather than a widening of POST /api/v1/pos/signatures: the old body (purpose,
// ref_type, ref_id) describes a capture that no longer exists: nothing in it says who signed or
// that they agreed.

import { api } from './ports.js';

export interface TypedAttestationResult {
  signature_ref: string;
  provider: string;
}

export async function captureTypedSignature(
  purpose: string,
  refType: string,
  refId: string,
  typedName: string,
  affirmed: boolean,
): Promise<TypedAttestationResult> {
  const res = await api('/api/v1/pos/signatures/typed', {
    method: 'POST',
    body: JSON.stringify({
      purpose,
      ref_type: refType,
      ref_id: refId,
      typed_name: typedName,
      affirmed,
    }),
  });
  if (!res.ok) {
    // The server's refusals are the operator-facing text (blank name, missing affirmative, unknown
 // purpose): surface them verbatim rather than a status code.
    let message = `signature ${res.status}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
 /* non-JSON body: keep the status message */
    }
    throw new Error(message);
  }
  return (await res.json()) as TypedAttestationResult;
}
