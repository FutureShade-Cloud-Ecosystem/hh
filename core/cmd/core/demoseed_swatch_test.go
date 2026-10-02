// demoseed_swatch_test.go, the photographed products carry real pictures, not placeholders.
//
// The till's product tile trusts has_image: with the flag up it renders the bytes and never falls
// back to the glyph, so a payload that decodes into a blank square is a demo that looks broken at
// the counter, and the fallback that would have saved the tile is suppressed by the very flag the
// row set. These legs hold the image bytes the PLAN builds to the promises, with no database: each
// photographed product's bytes decode as a PNG larger than a pixel in both dimensions and byte_size
// states the payload's own length; two products' swatches decode to visibly different pictures; and
// the same product's bytes are identical across two builds, the stability the idempotent census
// needs of anything the seed writes.
package main

import (
	"bytes"
	"image"
	"image/png"
	"sort"
	"testing"
)

// swatchRows returns the plan's product_images row for every product carrying Image true, keyed by
// the product key. The row is found by the derivation products() itself writes, demoID("image:"+key),
// so a photographed product whose image row went missing shows up here as a miss, not a pass.
func swatchRows(t *testing.T) map[string]row {
	t.Helper()
	rows, _ := buildForTest(t)
	byID := map[string]row{}
	for _, r := range rows {
		if r.table == "product_images" {
			id, _ := r.vals[0].(string)
			byID[id] = r
		}
	}
	out := map[string]row{}
	for _, p := range demoProducts {
		if !p.Image {
			continue
		}
		r, ok := byID[demoID("image:"+p.Key)]
		if !ok {
			t.Fatalf("%s carries Image true, the plan builds no product_images row for it", p.Key)
		}
		out[p.Key] = r
	}
	if len(out) == 0 {
		t.Fatal("no product carries Image true, the swatch legs would pass vacuously")
	}
	return out
}

// swatchPayload lifts the row's image_bytes and byte_size out of the column/value pairs.
func swatchPayload(t *testing.T, r row) ([]byte, int) {
	t.Helper()
	var payload []byte
	var size int
	found := 0
	for i, c := range r.cols {
		switch c {
		case "image_bytes":
			b, ok := r.vals[i].([]byte)
			if !ok {
				t.Fatalf("product_images.image_bytes is %T, want []byte", r.vals[i])
			}
			payload, found = b, found+1
		case "byte_size":
			n, ok := r.vals[i].(int)
			if !ok {
				t.Fatalf("product_images.byte_size is %T, want int", r.vals[i])
			}
			size, found = n, found+1
		}
	}
	if found != 2 {
		t.Fatalf("the product_images row carries only %d of the two columns the byte route serves", found)
	}
	return payload, size
}

// TestPlan_TheSwatchesAreRealPicturesLargerThanAPixel is the leg the blank placeholder fails: the
// bytes must decode, and what they decode to must have real extent in both dimensions, because the
// tile stretches whatever is there across the thumbnail box.
func TestPlan_TheSwatchesAreRealPicturesLargerThanAPixel(t *testing.T) {
	for key, r := range swatchRows(t) {
		payload, size := swatchPayload(t, r)
		img, err := png.Decode(bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("%s: the seeded bytes do not decode as a PNG: %v", key, err)
		}
		if b := img.Bounds(); b.Dx() <= 1 || b.Dy() <= 1 {
			t.Errorf("%s: the seeded image decodes to %dx%d, the tile stretches that into an empty square and the glyph fallback never fires while has_image is true", key, b.Dx(), b.Dy())
		}
		if size != len(payload) {
			t.Errorf("%s: byte_size says %d, the payload is %d bytes, the table's own size CHECK would refuse the row", key, size, len(payload))
		}
	}
}

// TestPlan_TwoProductsSwatchesAreVisiblyDifferentPictures. Four photographed products that all
// decoded to the same picture would fail the brief's own bar of visibly different swatches, and a
// shared colour would also read on the board as one product wearing another's likeness. The
// comparison is the decoded pixels, not the encoded bytes, so an encoder detail can never make two
// honest swatches look falsely identical or two lazy ones falsely distinct.
func TestPlan_TwoProductsSwatchesAreVisiblyDifferentPictures(t *testing.T) {
	decoded := map[string]image.Image{}
	for key, r := range swatchRows(t) {
		payload, _ := swatchPayload(t, r)
		img, err := png.Decode(bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("%s: the seeded bytes do not decode as a PNG: %v", key, err)
		}
		decoded[key] = img
	}
	keys := make([]string, 0, len(decoded))
	for k := range decoded {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, a := range keys {
		for _, b := range keys[i+1:] {
			if samePicture(decoded[a], decoded[b]) {
				t.Errorf("%s and %s decode to the very same picture, each product's swatch must be visibly its own", a, b)
			}
		}
	}
}

// samePicture compares two decoded images pixel for pixel over their common extent.
func samePicture(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			r1, g1, b1, a1 := a.At(x, y).RGBA()
			r2, g2, b2, a2 := b.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				return false
			}
		}
	}
	return true
}

// TestPlan_TheSameSwatchBytesOnEveryBuild is the determinism the census stands on: a re-run
// recognises the rows it already planted by key, and it can only recognise them if the payload a
// key carries is the payload that key has always carried. The bytes must therefore be a pure
// function of the product key, with no clock and no randomness anywhere in the drawing.
func TestPlan_TheSameSwatchBytesOnEveryBuild(t *testing.T) {
	first := swatchRows(t)
	second := swatchRows(t)
	for key, r1 := range first {
		r2, ok := second[key]
		if !ok {
			t.Fatalf("%s: the second build has no image row for it", key)
		}
		p1, s1 := swatchPayload(t, r1)
		p2, s2 := swatchPayload(t, r2)
		if !bytes.Equal(p1, p2) || s1 != s2 {
			t.Errorf("%s: the swatch bytes changed between two builds on the same input, a re-run could no longer recognise the row it planted", key)
		}
	}
}
