DOC-ID: HH-BADGE-SVG-MEASUREMENTS-r1

# HH badge SVGs: measured geometry and pixel identity proof

Source: the six 3000x3000 JPG colourways in the parent folder. Method: subpixel
edge crossings at the 50 percent contrast point (PIL + numpy), circle fits on
boundary points for every corner radius and both slot arcs, an instrument
calibration that renders a candidate SVG, JPEG compresses it with the source
files' own Adobe quantisation tables (4:4:4, near lossless) and re-measures it,
so every reported number is bias corrected. Final acceptance: each SVG is
rasterised at 3000 px by two independent renderers (cairosvg 2.9.1 and
google-chrome headless) and differenced against its JPG.

## Geometry (viewBox 0 0 3000 3000)

The mark is a geometric H: a full height left stem, a crossbar, a right lower
stem joined through the crossbar, and a detached right upper stem above a
horizontal slit. The counter between the stems is capped below by a
semicircular arc (the round notch) tangent to both stem walls; the counter
floor on the left runs into the crossbar top through a quarter arc of the same
radius, tangent to the left wall and to the crossbar top line. All corners of
the outer boundary carry the same radius. The mark box is exactly centred.

| element | value |
| --- | --- |
| left stem | x 615.795 to 1415.207 |
| right stems | x 1584.793 to 2384.205 |
| stem tops and bottoms | y 465.172 to 2534.828 |
| crossbar top | y 1391.17 |
| detached stem bottom | y 1222.0 |
| outer corner radius | 29.15 |
| slot width | 169.586 (x 1415.207 to 1584.793) |
| slot arc radius | 84.793 (half the slot width, tangent to both walls) |
| notch arc centre | (1500, 1940.10) |
| counter floor quarter arc centre | (1500, 1306.377), tangent point (1500, 1391.17) |

Colours (median of the flat region, per channel, per source file):

| file | ground | mark |
| --- | --- | --- |
| badge-on-yellow | #FFC313 | #0F2944 |
| badge-on-white | #FFFFFF | #0F2944 |
| badge-on-grey | #D9D9D9 | #0F2944 |
| badge-on-navy | #0F2944 | #FFC313 |
| badge-all-black | #FFFFFF | #050608 |
| badge-yellow-and-white | #050608 | #FFC313 |

mark.svg carries the same path with fill currentColor on a transparent ground.

## Pixel identity proof

diff is the per pixel maximum absolute channel difference between the 3000 px
rasterisation and the source JPG. Flat means 16 px or more from any edge; near
means 8 to 16 px; mid means 3 to 8 px; band is the anti aliasing band within
3 px of an edge. Violations counts pixels outside the band whose diff exceeds
the diff the JPEG itself introduces on the same rendering plus 8.

| colourway | flat mean | flat max | near max | mid max | band mean | band max | violations |
| --- | --- | --- | --- | --- | --- | --- | --- |
| on-yellow | 0.000 | 2 | 4 | 6 | 6.0 | 206 | 0 |
| on-white | 0.000 | 0 | 3 | 5 | 2.2 | 206 | 0 |
| on-grey | 0.000 | 0 | 4 | 4 | 4.6 | 174 | 0 |
| on-navy | 0.000 | 0 | 5 | 6 | 4.9 | 201 | 0 |
| all-black | 0.001 | 2 | 2 | 4 | 6.8 | 219 | 0 |
| yellow-and-white | 0.000 | 2 | 4 | 7 | 5.7 | 208 | 0 |

cairosvg and google-chrome agree to within 1 level on every statistic above.
Verdict: outside a band narrower than 3 px along the edges the maximum
difference is 7 of 255, at the JPEG noise floor of these near lossless
sources; every larger difference sits inside the one to two pixel anti
aliasing band. The diff images (diff-*.png, difference amplified 16 times)
are beside this note for verification.

END HH-BADGE-SVG-MEASUREMENTS-r1
