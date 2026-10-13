// The demo server's answer to one request, as a pure function of the URL, so the
// suite holds it without a server that never ends. The page (index.html, or the
// light build on ?theme=light) answers every path except /sheets/<tag>.css, which
// answers the element sheet the page's shadow roots link, as text/css: a sheet
// request answered with HTML is refused by the browser and the page paints bare.

import { readFileSync, existsSync } from 'node:fs';
import { dirname, join, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const sheetsDir = join(here, 'sheets');

const SHEET_PATH = /^\/sheets\/([a-z0-9-]+\.css)$/;

const notFound = () => ({
  status: 404,
  headers: { 'content-type': 'text/plain; charset=utf-8' },
  body: Buffer.from('not found'),
});

const page = (name) => {
  const path = normalize(name).replace(/^([.][.][/\\])+/, '');
  const file = join(here, path);
  if (!file.startsWith(here) || !existsSync(file) || !file.endsWith('.html')) return null;
  return readFileSync(file);
};

const sheet = (pathname) => {
  let decoded;
  try {
    decoded = decodeURIComponent(pathname);
  } catch {
    return notFound();
  }
  const match = SHEET_PATH.exec(decoded);
  if (match === null) return notFound();
  const file = join(sheetsDir, match[1]);
  if (!file.startsWith(`${sheetsDir}/`) || !existsSync(file)) return notFound();
  return { status: 200, headers: { 'content-type': 'text/css; charset=utf-8' }, body: readFileSync(file) };
};

/** The answer to one request URL: { status, headers, body }. */
export function respond(rawUrl) {
  const url = new URL(rawUrl ?? '/', 'http://127.0.0.1');
  if (url.pathname.startsWith('/sheets/')) return sheet(url.pathname);
  const body = url.searchParams.get('theme') === 'light' ? page('index-light.html') : page('index.html');
  if (body === null) return notFound();
  return { status: 200, headers: { 'content-type': 'text/html; charset=utf-8' }, body };
}
