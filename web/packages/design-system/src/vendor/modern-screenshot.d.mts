// The type surface of the vendored capture library, named for what the
// feedback widget uses. The library itself is the patched ESM build beside
// this file (see README.md there for the source, version and checksums);
// it ships no types of its own.

/** The nodes a capture keeps: this widget's filter drops the widget itself. */
export type CaptureFilter = (node: Node) => boolean;

/** The options of the one entry point the widget calls. */
export interface CaptureOptions {
  backgroundColor?: string;
  filter?: CaptureFilter;
  scale?: number;
  type?: string;
  quality?: number;
}

/** Renders the node (shadow roots included) and resolves the rasterised image. */
export declare function domToBlob(node: Node, options?: CaptureOptions): Promise<Blob | null>;
