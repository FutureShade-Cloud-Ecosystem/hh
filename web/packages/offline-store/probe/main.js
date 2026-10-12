const vfs = new URLSearchParams(location.search).get('vfs') ?? 'ahp';
const worker = new Worker(new URL('./worker.js', import.meta.url), { type: 'module' });
const out = document.getElementById('out');
let n = 0;
const pending = new Map();

worker.onmessage = ({ data }) => {
  const p = pending.get(data.id);
  if (p) {
    pending.delete(data.id);
    data.error ? p.reject(new Error(data.error)) : p.resolve(data.result);
  }
};
worker.onerror = (e) => {
  out.textContent = `worker error: ${e.message}`;
  out.dataset.state = 'error';
};

function call(op, extra = {}) {
  return new Promise((resolve, reject) => {
    const id = ++n;
    pending.set(id, { resolve, reject });
    worker.postMessage({ id, op, ...extra });
  });
}

async function show() {
  try {
    const rows = await call('read');
    out.textContent = JSON.stringify(rows);
    out.dataset.rows = String(rows.length);
    out.dataset.state = 'ready';
  } catch (e) {
    out.textContent = `error: ${e.message}`;
    out.dataset.state = 'error';
  }
}

document.getElementById('write').onclick = async () => {
  try {
    await call('insert');
    await show();
  } catch (e) {
    out.textContent = `error: ${e.message}`;
    out.dataset.state = 'error';
  }
};
call('init', { vfs }).then((kind) => {
  document.body.dataset.vfs = kind;
  return show();
}, (e) => {
  out.textContent = `error: ${e.message}`;
  out.dataset.state = 'error';
});
