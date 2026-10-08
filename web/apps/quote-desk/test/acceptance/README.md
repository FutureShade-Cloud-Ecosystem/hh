# Quote Desk acceptance story, real demo core

`cod-q4.acceptance.ts` converts the seeded Cash On Demand quote DEMO-TRT-Q4 through the guided Convert surface to
payment before release, with Will-call, against a real demo core. It is not part of `npm test` (that runs the stub).

1. Boot the demo image on a fresh database and restart it once: the first boot builds the world, the quote world
   (DEMO-TRT-Q1 to Q4) arrives on the next boot.
2. Serve the desk in demo mode with /api on that box (a dev server whose proxy points at it, or the box's own desk).
3. Run, with the gate's phrase in the environment only:

       QD_ACCEPT_URL=http://127.0.0.1:<port>/ QD_ACCEPT_USER=demo QD_ACCEPT_PHRASE=<from the vault> \
         npx playwright test --config test/acceptance/acceptance.config.ts

The walk consumes DEMO-TRT-Q4 (it becomes an order, an invoice and a payment), so a second run wants a fresh world.
