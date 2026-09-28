// A minimal Chrome DevTools Protocol driver over Node 22's global WebSocket,
// zero npm dependencies: launch a headless Chromium, open one page, navigate,
// set the viewport, evaluate. The ERP's own scripts drive headless Chromium
// through Playwright, which is a dependency this package may not add; this is
// the same job over the raw protocol, small on purpose. Plain JavaScript: the
// Node type-stripping story covers the package's .ts sources, and this file
// carries no types to strip.

import { spawn, spawnSync } from 'node:child_process';
import { mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const CDP_TIMEOUT_MS = 30000;

/**
 * Finds a usable Chromium-family binary and returns its path, or null. Order:
 * the HH_DS_BROWSER override, the PATH (google-chrome, chromium and friends),
 * then the Playwright browser cache the ERP tooling uses on this machine.
 */
export function findBrowserBinary() {
  const candidates = [];
  if (process.env.HH_DS_BROWSER) candidates.push(process.env.HH_DS_BROWSER);
  candidates.push('google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser', 'chrome');
  for (const c of candidates) {
    const probe = spawnSync(c, ['--version'], { encoding: 'utf8' });
    if (probe.status === 0) return c;
  }
  const cache = join(process.env.HOME ?? '', '.cache', 'ms-playwright');
  try {
    const dirs = readdirSync(cache).sort().reverse();
    for (const dir of dirs) {
      for (const rel of [join(dir, 'chrome-linux', 'chrome'), join(dir, 'chrome-linux', 'headless_shell')]) {
        const abs = join(cache, rel);
        const probe = spawnSync(abs, ['--version'], { encoding: 'utf8' });
        if (probe.status === 0) return abs;
      }
    }
  } catch {
    // no cache directory: fall through to null
  }
  return null;
}

/**
 * Launches the browser headless with --remote-debugging-port=0, takes the
 * DevTools WebSocket endpoint from its stderr, connects, and returns a small
 * driver bound to one page target.
 */
export async function launchBrowser(binary) {
  const userDir = mkdtempSync(join(tmpdir(), 'hh-ds-cdp-'));
  const child = spawn(
    binary,
    [
      '--headless',
      '--remote-debugging-port=0',
      `--user-data-dir=${userDir}`,
      '--no-sandbox',
      '--disable-gpu',
      '--disable-dev-shm-usage',
      '--no-first-run',
      '--no-default-browser-check',
      'about:blank',
    ],
    { stdio: ['ignore', 'ignore', 'pipe'] },
  );

  let stderr = '';
  const wsUrl = await new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error(`the browser never announced a DevTools endpoint. stderr:\n${stderr}`)),
      CDP_TIMEOUT_MS,
    );
    child.stderr.on('data', (d) => {
      stderr += d;
      const m = stderr.match(/DevTools listening on (ws:\/\/\S+)/);
      if (m) {
        clearTimeout(timer);
        resolve(m[1]);
      }
    });
    child.once('exit', () => {
      clearTimeout(timer);
      reject(new Error(`the browser exited before announcing a DevTools endpoint. stderr:\n${stderr}`));
    });
  });

  // Node 22's global WebSocket (undici) is EventTarget-shaped.
  const ws = new WebSocket(wsUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', () => resolve(), { once: true });
    ws.addEventListener('error', () => reject(new Error(`the DevTools WebSocket failed to open: ${wsUrl}`)), { once: true });
  });

  let nextId = 1;
  const pending = new Map();
  const listeners = [];
  ws.addEventListener('message', (ev) => {
    const msg = JSON.parse(String(ev.data));
    if (msg.id !== undefined && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      if (msg.error) reject(new Error(`CDP ${msg.error.message} ${JSON.stringify(msg.error.data ?? '')}`));
      else resolve(msg.result);
      return;
    }
    for (const l of listeners) {
      if (l.method === msg.method && (!l.sessionId || l.sessionId === msg.sessionId)) {
        l.resolve(msg.params);
      }
    }
  });

  const send = (method, params = {}, sessionId) =>
    new Promise((resolve, reject) => {
      const id = nextId++;
      const timer = setTimeout(() => {
        pending.delete(id);
        reject(new Error(`CDP ${method} timed out`));
      }, CDP_TIMEOUT_MS);
      pending.set(id, {
        resolve: (v) => {
          clearTimeout(timer);
          resolve(v);
        },
        reject: (e) => {
          clearTimeout(timer);
          reject(e);
        },
      });
      ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
    });

  const once = (method, sessionId) =>
    new Promise((resolve) => {
      listeners.push({ method, sessionId, resolve });
    });

  const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
  await send('Page.enable', {}, sessionId);
  await send('Runtime.enable', {}, sessionId);

  return {
    sessionId,
    send,
    async navigate(url) {
      const loaded = once('Page.loadEventFired', sessionId);
      await send('Page.navigate', { url }, sessionId);
      await loaded;
    },
    async setViewport(width, height) {
      await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false }, sessionId);
    },
    async evaluate(expression) {
      const r = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }, sessionId);
      if (r.exceptionDetails) {
        throw new Error(`the page threw: ${r.exceptionDetails.exception?.description ?? r.exceptionDetails.text}`);
      }
      return r.result.value;
    },
    async close() {
      try {
        await send('Browser.close');
      } catch {
        // the browser may already be gone; the kill below settles it
      }
      await new Promise((resolve) => {
        child.once('exit', resolve);
        setTimeout(() => {
          try {
            child.kill('SIGKILL');
          } catch {}
          resolve();
        }, 5000);
      });
      rmSync(userDir, { recursive: true, force: true });
    },
  };
}
