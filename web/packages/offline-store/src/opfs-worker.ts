// The worker entry for the OPFS backend: the shell starts this file as a
// module worker. It owns the database; every OPFS call stays off the main
// thread (synchronous access handles exist only in workers).
import { createRequestHandler } from "./opfs-protocol.js";
import { createWaSqliteExecutor } from "./wasqlite-executor.js";

const handle = createRequestHandler((dbName) =>
  createWaSqliteExecutor({ vfs: "opfs", directory: `/${dbName}`, name: dbName }),
);

self.onmessage = async (event: MessageEvent) => {
  self.postMessage(await handle(event.data));
};
