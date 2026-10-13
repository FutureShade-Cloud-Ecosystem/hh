// Browser leg for the offline store: the real OPFS backend and the real worker
// (src/opfs-worker.ts) under the real OfflineStore, with a stub server in the
// page. Playwright drives window.hh. Not part of CI (it needs a browser).
import { createOpfsBackend } from '../src/backend-opfs.ts';
import { OfflineStore } from '../src/store.ts';
import { scopeBookFor } from '../src/scopes.ts';

const out = document.getElementById('out');
const scopes = scopeBookFor(['yard-pick', 'drive-run']);
const TASK = '00000000-0000-4000-8000-000000000001';

const transport = {
  async pull(scope, since) {
    const items = scope === 'pick_tasks' && since < 5 ? [{ id: TASK, seq: 5, data: { n: 1 } }] : [];
    return { ok: true, value: { scope, items, tombstones: [], last_seq: scope === 'pick_tasks' ? Math.max(since, 5) : since, next_cursor: null } };
  },
  async push(_scope, ops) {
    return { ok: true, value: { results: ops.map((o) => ({ client_id: o.client_id, status: 'applied', seq: 6 })) } };
  },
};

async function start() {
  const backend = await createOpfsBackend({
    dbName: 'hh-offline-store-browser-leg',
    locks: navigator.locks,
    createWorker: () => new Worker(new URL('../src/opfs-worker.ts', import.meta.url), { type: 'module' }),
    onOwnership: (s) => (document.body.dataset.ownership = s),
  });
  const store = await OfflineStore.open({ backend, scopes });
  window.hh = {
    ownership: () => store.ownership(),
    pull: () => store.pull(transport),
    enqueue: () => store.enqueue({ scope: 'pick_tasks', op: 'update', payload: { id: TASK, picked: 2 }, baseSeq: 5 }),
    replay: () => store.replay(transport),
    pending: async () => (await store.outbox.pending()).length,
    rows: async () => (await store.list('pick_tasks')),
    crossOriginIsolated: () => self.crossOriginIsolated,
  };
  out.textContent = `ready ${store.ownership()}`;
  out.dataset.state = 'ready';
}

start().catch((e) => {
  out.textContent = `error: ${e && e.message ? e.message : e}`;
  out.dataset.state = 'error';
});
