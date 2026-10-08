# HH Drive: provenance of the ports

Everything here that was carried from the original HardscapeOS app (repository
`hardscapeos_dibbits`, read only) came from commit
`f8f4973db317b92296ae8ec7e62a3f54f5dc82ab`, under the roadmap's row 563 rule
(take the existing and improve it): the logic ports as plain code onto HH's
generated api-client, and the screens are rebuilt on HH's design system and the
role app template's shell. The same facts sit in the header of each file below.

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Stop lifecycle, status labels, `stopWord` | `app/src/dispatch-logic.ts` | `src/drive-logic.ts` | Matrix re-stated from the core's `transitions.go`; "Failed" reads "Not delivered" |
| What a stop offers (`stopActions`) | `app/src/dispatch-logic.ts` | `driverActions` in `src/drive-logic.ts` | Narrowed to the four moves a driver makes; no cancel |
| `runSummary`, `stopsInOrder`, `addressLine`, `qtyLabel`, `plainSentence`, `weightLabel`, `formatTime` | `app/src/dispatch-logic.ts` | `src/drive-logic.ts` | Dashes removed from words; time reads in the dealer's zone; an unmeasured weight says so |
| Proof gate and attestation words (`canCapturePOD`, `podBlockedReason`) | `app/src/dispatch-pod.ts`, `app/src/dispatch-logic.ts` | `canSign`, `signBlockedReason`, `ATTESTATION` | Wording kept, the dash replaced by a colon |
| Handback words | `app/src/dispatch-logic.ts` | `HANDBACK_TITLE`, `HANDBACK_BODY`, `HANDBACK_PROMPT` | Dash replaced by a colon |
| Close rule (`runClose`, `closeLine`, `stopHandle`, `CLOSE_RULE`) | `app/src/drive-logic.ts` | `src/drive-logic.ts` | Mirrors the core's driver gate: a cancelled stop sends the driver to dispatch (the original counted it as settled) |
| End of run recap | `app/src/drive-close.ts` | `renderClose` in `src/views.ts` | Rebuilt on the design system |
| Run sheet, stop detail, proof screen, could not deliver sheet | `app/src/dispatch-driver-run.ts`, `app/src/dispatch-pod.ts`, `app/src/drive.ts` | `src/views.ts` | Rebuilt for 390 px, gloves and sunlight; payment hold banner and scan trail dropped (money, and not the driver's first increment) |
| API call set | `app/src/dispatch-api.ts` | `src/drive-api.ts` | Over the generated client; every read goes through a whitelist projection |
| Bundle fence word list | `app/scripts/bundle-fence.mjs` | `test/_lib/fence.mjs` | Widened to cents, currency, dollar, amount due and balance, plus a money figure pattern |

The boards are FB Design's "HH: field apps" (proposed): my run and proof of
delivery, in light. The stop en route, could not deliver and the end of run were
built from the original app's phone screens, as the brief said.
