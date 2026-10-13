// wa-sqlite types its main module only; the wasm glue and the example VFS
// classes are plain JavaScript. These declarations cover the three imports the
// executor makes, and nothing else.
declare module "wa-sqlite/dist/wa-sqlite.mjs" {
  const factory: (moduleOverrides?: { wasmBinary?: Uint8Array }) => Promise<unknown>;
  export default factory;
}
declare module "wa-sqlite/src/examples/MemoryVFS.js" {
  export class MemoryVFS {
    constructor();
  }
}
declare module "wa-sqlite/src/examples/AccessHandlePoolVFS.js" {
  export class AccessHandlePoolVFS {
    constructor(directory: string);
    readonly isReady: Promise<unknown>;
  }
}
