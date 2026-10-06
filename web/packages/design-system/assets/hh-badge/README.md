# The Hardscape House badge

The brand badge of Hardscape House, as measured vector geometry. The source of
truth is the six 3000 by 3000 JPG colourways the principal dropped at
`principal/assets/hh-logos/` (Badge on Yellow, on White, on Grey, on Navy, All
Black, Yellow and White): a geometric H of rounded rectangles with a round
notch, navy on yellow and the rest. Every number in these files comes from
subpixel measurement of those JPGs, and each colourway SVG is proven pixel
identical to its JPG by rasterising at 3000 px and differencing: outside a
band narrower than 3 px along the edges the maximum per channel difference is
7 of 255, at the JPEG noise floor of the sources. The full method, the
measured geometry table and the per colourway statistics are in
`MEASUREMENTS.md` beside this note.

## Files

| file | what it is |
| --- | --- |
| `badge-on-yellow.svg` and five siblings | one SVG per source colourway, ground rect plus one path |
| `mark.svg` | the H alone, transparent ground, `fill="currentColor"` |
| `favicon.svg` | the on yellow colourway, served to browsers |
| `favicon.ico` | 16, 32 and 48 px frames, each rendered from the SVG |
| `apple-touch-icon.png` | 180 by 180 |
| `favicon-192.png`, `favicon-512.png` | the PNG sizes PWA manifests read |

Apps serve the favicon set from their own `public/favicons/` (the front door,
the Counter Till and the public desk carry the same set); the front door's
header brand mark inlines `mark.svg`'s path with `currentColor`, over the gold
fill the badge on yellow colourway pairs it with (`--hh-accent` is the badge
yellow, `--hh-accent-ink` the ink it carries).

## Regenerating

The favicon set is generated from `badge-on-yellow.svg`, never hand drawn:
render the SVG at each target size and pack (the .ico frames are the 16, 32
and 48 px renders). The geometry is not expected to change; if the brand ever
moves, remeasure from new sources the same way MEASUREMENTS.md describes and
regenerate every file here together.
