# Sign in page load flake (guards-panels.pw.ts and others)

What was seen: a test waits for a role button ("Sales manager" or "Sales") after `page.goto("/")` and the button never
appears; the page has no Lit dev mode line, so the app module never ran on that load.

What was found: a scratch spec that loaded the page 60 times with one CPU core held busy caught it once, with the page's
request log: about 90 module requests (the app's own sources and the workspace packages' generated client files) all
failed with `net::ERR_NETWORK_CHANGED`. That is the browser's own abort of every request in flight when the host's
network interfaces or addresses change. What changed the network on the machine was not found: the cause is the browser's
abort, and the likely trigger is other work on this shared machine (not verified). The app
never got its modules, so it never started. It is not a race in the app or in the test's first wait, and it hits
whichever role button the test happens to wait for.

Rate: the spec `test/guards-panels.pw.ts` run 25 times (`--repeat-each=25`, 1050 test runs) failed 5 times, every one at
the sign in wait and none anywhere else; 1045 passed. Two full runs of the suite showed 7 and 1 such failures, each
passing on its own right after. A full run with the CPU starved to one core passed 338 of 338.

Not fixed in the app because nothing in the app can recover modules the browser dropped. The test side remedy, if it is
wanted, is one retry for a failed load in the shared sign in helpers or `retries: 1` in the Playwright configuration.
