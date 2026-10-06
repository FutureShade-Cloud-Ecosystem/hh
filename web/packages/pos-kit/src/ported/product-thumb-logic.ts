// The product picture's fallback chain, as one pure function so it can be tested without a DOM.
//
// A real picture first (the byte route, when the product may have one and it has not failed), the
// category's drawn tile next (the design system's product tile, in the document's theme, which the
// host hands the thumb through ports.ts: this package does not import the design system), and the
// stacked-stone glyph last. The glyph now means only "no picture and a category the tiles do not
// know", a small and honest set. Nothing here fetches anything.

export interface ThumbSourceInput {
  /** The byte route for the product's real picture; '' when there is no product to ask about. */
  imageSrc: string;
  /** Known-ahead-of-time image presence: undefined = try and see, false = skip the request. */
  hasImage: boolean | undefined;
  /** The byte route has already said no for this product. */
  failed: boolean;
  /** The category's drawn tile in the document's theme, or null when the category has none. */
  tileSrc: string | null;
}

export type ThumbSource = { kind: 'image' | 'tile'; src: string } | { kind: 'glyph'; src: '' };

export function thumbSource(input: ThumbSourceInput): ThumbSource {
  if (input.imageSrc !== '' && input.hasImage !== false && !input.failed) {
    return { kind: 'image', src: input.imageSrc };
  }
  return input.tileSrc === null ? { kind: 'glyph', src: '' } : { kind: 'tile', src: input.tileSrc };
}
