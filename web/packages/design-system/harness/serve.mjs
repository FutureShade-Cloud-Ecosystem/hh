// The harness server: Node 22 only, no dependencies, no build. It refuses to
// start if the port is already occupied (a server left behind from any other
// checkout would otherwise answer with the wrong program), and after listening
// it fetches its own page and asserts the served title is this package's, so a
// gate or a human can never silently grade someone else's harness.
//
// This file is plain JavaScript, on purpose: Node's type stripping covers the
// package's .ts sources but not .mjs machinery, so this file carries no type
// annotations and the types it cares about live in src/harness-page.ts.
//
//   /snap/bin/node --import ./src/_ts-js-loader.mjs harness/serve.mjs
//   then open http://127.0.0.1:4173/  (?theme=light, ?brand=hh-pro)
//
//   HARNESS_PORT=0 asks the kernel for a genuinely free port: the occupied
//   port guard has nothing to probe, and the announce line carries the port
//   the kernel actually assigned.

import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';

import { buildHarnessPage, harnessSheets, HARNESS_TITLE } from '../src/harness-page.js';

const here = dirname(fileURLToPath(import.meta.url));
const PORT = Number(process.env.HARNESS_PORT ?? 4173);
const HOST = '127.0.0.1';

const THEMES = ['dark', 'light'];
const BRANDS = ['base', 'hh-pro'];

function assertPortFree(port) {
  return new Promise((resolve, reject) => {
    const probe = net.createConnection({ port, host: HOST });
    probe.once('connect', () => {
      probe.destroy();
      reject(new Error(`port ${port} is already in use; refusing to start against a server this script did not start`));
    });
    probe.once('error', () => {
      resolve();
    });
  });
}

async function main() {
  // Port 0 cannot be occupied: it asks the kernel to assign a genuinely free
  // port, so the guard below has nothing to probe and is skipped. The port
  // the kernel actually assigned is read back from the listening socket in
  // the listen callback and is what the self-check fetches and the announce
  // line carries.
  if (PORT !== 0) {
    await assertPortFree(PORT);
  }

  let bound = PORT;

  const server = createServer((req, res) => {
    const url = new URL(req.url ?? '/', `http://${HOST}:${bound}`);
    if (url.pathname === '/tokens.css') {
      res.writeHead(200, { 'content-type': 'text/css; charset=utf-8' });
      res.end(readFileSync(join(here, '..', 'src', 'tokens.css')));
      return;
    }
    // The per-kit sheets the page's declarative shadow roots link.
    const sheet = url.pathname.match(/^\/sheets\/([a-z0-9-]+)\.css$/);
    if (sheet) {
      const kit = harnessSheets().find((s) => s.tag === sheet[1]);
      if (kit) {
        res.writeHead(200, { 'content-type': 'text/css; charset=utf-8' });
        res.end(kit.css);
        return;
      }
    }
    if (url.pathname !== '/') {
      res.writeHead(404, { 'content-type': 'text/plain; charset=utf-8' });
      res.end('not found');
      return;
    }
    const theme = THEMES.includes(url.searchParams.get('theme')) ? url.searchParams.get('theme') : 'dark';
    const brand = BRANDS.includes(url.searchParams.get('brand')) ? url.searchParams.get('brand') : 'base';
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
    res.end(buildHarnessPage({ theme, brand }));
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(PORT, HOST, () => {
      bound = server.address().port;
      resolve();
    });
  });

  // The self-check: fetch our own page and prove it is ours before announcing.
  const page = await fetch(`http://${HOST}:${bound}/`).then((r) => r.text());
  if (!page.includes(`<title>${HARNESS_TITLE}</title>`)) {
    console.error('the server answered, but not with this harness; refusing to announce it');
    process.exit(1);
  }

  console.log(`@hardscapehouse/design-system harness on http://${HOST}:${bound}/`);
  console.log('variants: /?theme=light  /?brand=hh-pro  /?theme=light&brand=hh-pro');
}

main().catch((err) => {
  console.error(err instanceof Error ? err.message : err);
  process.exit(1);
});
