-- 0089: the typed-name attestation provider (DIB-386, bundle W1A-B).
--
-- pos_signature.provider was constrained to SIMULATED | RUN_TERMINAL | THIRD_PARTY when the capture
-- seam was a stub: SimulatedSignatureProvider sealed the LITERAL string
-- 'SIMULATED:<purpose>:<ref>' and stamped provider='SIMULATED'. There was no captured act anywhere
-- in that row — the "signature" was the row's own id, encrypted.
--
-- The ruling (2026-07-30, restated for DIB-386): what this business actually needs is an electronic
-- ATTESTATION — a typed full name plus an explicit affirmative — not a drawing pad. A pad implies
-- ink that was never captured, and the DIB-57 quote acceptance already ships the honest version of
-- this act. TYPED_NAME_V1 is that capture, and the provider string finally reflects reality: a row
-- stamped TYPED_NAME_V1 means a named person affirmed something, and signature_enc holds WHAT they
-- typed and affirmed rather than a placeholder.
--
-- The value is VERSIONED (V1) on purpose. The sealed payload's shape is part of the evidence — a
-- future capture that adds or changes a field is a NEW provider value, not a silent reinterpretation
-- of rows already written. The old values stay permitted: SIMULATED rows already exist and remain
-- exactly as true (and as weak) as they were the day they were written.
ALTER TABLE pos_signature DROP CONSTRAINT pos_signature_provider_check;
ALTER TABLE pos_signature ADD CONSTRAINT pos_signature_provider_check
  CHECK (provider IN ('SIMULATED','RUN_TERMINAL','THIRD_PARTY','TYPED_NAME_V1'));
