// Serves the committed demo pages live: the same files the build wrote, on
// 127.0.0.1, on a port of the field cycle's own range (56200 to 56299), by
// default 56250, overridable inline (SHELL_DEMO_PORT=<free port>). The
// server refuses an occupied port instead of failing later, grades its own
// page before announcing (the key words must be in the bytes it is about to
// serve), and answers ?theme=light with the light build. No build step, no
// client scripts; this is the committed page, served, with the element
// sheets it links (demo/respond.mjs answers both).
//
//   SHELL_DEMO_PORT=<free> node --import ./test/_ts-js-loader.mjs demo/serve.mjs

import { createServer } from 'node:http';
import { connect } from 'node:net';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { respond } from './respond.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const requested = Number(process.env.SHELL_DEMO_PORT ?? 56250);
if (!Number.isInteger(requested) || requested < 56200 || requested > 56299) {
  console.error(`SHELL_DEMO_PORT must be a free port in 56200 to 56299, got ${process.env.SHELL_DEMO_PORT ?? '(unset)'}`);
  process.exit(1);
}

const refuseIfOccupied = (port) =>
  new Promise((resolve, reject) => {
    const probe = connect({ port, host: '127.0.0.1' });
    probe.on('connect', () => {
      probe.destroy();
      reject(new Error(`port ${port} is occupied: pick another free port in 56200 to 56299`));
    });
    probe.on('error', () => resolve());
  });

await refuseIfOccupied(requested);

const GRADE_WORDS = [
  'Gable Landscape Supply',
  'Synced',
  'Sign in expired',
  'Pinned',
  'read only',
  'Update now',
];

const server = createServer((req, res) => {
  const answer = respond(req.url);
  res.writeHead(answer.status, answer.headers);
  res.end(answer.body);
});

server.listen(requested, '127.0.0.1', () => {
  const served = readFileSync(join(here, 'index.html'), 'utf8');
  const missing = GRADE_WORDS.filter((w) => !served.includes(w));
  if (missing.length > 0) {
    console.error(`the demo page failed its own grade; missing: ${missing.join(', ')}`);
    process.exit(1);
  }
  console.log(`serving the shell-chrome demo on http://127.0.0.1:${requested}/ (?theme=light for the light build)`);
});
