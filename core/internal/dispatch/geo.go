package dispatch

// geo.go, distance, in Go, over plain lat/lng columns.
//
// NO POSTGIS (ruled, 2026-08-04). The extension would buy exact geodesics and spatial indexing. This
// module needs neither: a run is a dozen stops inside one county, the figure is a planning estimate
// printed on a manifest, and nothing routes on it. What PostGIS would actually cost is a deployment
// dependency that outlives the feature and a schema that cannot be restored on a plain Postgres.
//
// WHAT THE NUMBER MEANS, HONESTLY. Haversine is great-circle distance on a sphere: the straight line a
// crow flies, not the road a truck drives. Real driving distance in this geography runs roughly
// 20-40% longer. The estimate is therefore labelled "straight-line" everywhere it is shown (the
// manifest says so in the printed text) rather than presented as a drive distance someone might plan
// a shift around. An honest small number beats a confident wrong one.

import "math"

// earthRadiusKm is the mean radius. The ellipsoid's equatorial and polar radii differ by ~0.3%, which
// is an order of magnitude below the straight-line-vs-road error already declared above.
const earthRadiusKm = 6371.0088

// Point is a plain coordinate. No geometry type, no SRID, no library.
type Point struct {
	Lat float64
	Lng float64
}

// HaversineKm returns the great-circle distance between two points in kilometres.
//
// The formula is used in its haversine form (rather than the shorter spherical law of cosines)
// because the law of cosines loses precision catastrophically for SHORT distances, and every
// distance this module computes is short. Two stops on the same street are exactly the case it gets
// wrong.
func HaversineKm(a, b Point) float64 {
	lat1 := rad(a.Lat)
	lat2 := rad(b.Lat)
	dLat := rad(b.Lat - a.Lat)
	dLng := rad(b.Lng - a.Lng)

	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	// Clamp before Asin: floating-point can push h a hair above 1 for antipodal-ish inputs, and
	// math.Asin(>1) is NaN, a NaN distance would propagate silently into a printed manifest.
	if h > 1 {
		h = 1
	}
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(h))
}

// RouteKm sums the legs of depot → stops (in the given order) → depot.
//
// STOPS WITHOUT COORDINATES ARE SKIPPED, NOT ZEROED. A stop whose address never geocoded contributes
// nothing to the total and is reported separately by the caller (RunDetail.UngeocodedStops), so a run
// where half the addresses are unknown cannot render a short distance that reads like a short day.
// The caller decides whether a partial total is worth showing; this function just refuses to invent.
//
// The return trip is included because the truck comes home, a one-way total systematically
// understates the day by the longest single leg.
func RouteKm(depot Point, stops []Point) float64 {
	if len(stops) == 0 {
		return 0
	}
	total := HaversineKm(depot, stops[0])
	for i := 1; i < len(stops); i++ {
		total += HaversineKm(stops[i-1], stops[i])
	}
	total += HaversineKm(stops[len(stops)-1], depot)
	return total
}

// NearestFirst orders stops by straight-line distance from the depot, nearest first, and returns the
// permutation as indices into the input.
//
// THIS IS A SUGGESTION, NOT A ROUTE. It is a greedy sort by distance from the yard, which is a decent
// first draft for a rural run out one highway and actively wrong for a run that loops. The dispatcher
// reorders by hand on the board and their order is what ships; this only saves them the first drag.
// Calling it "optimize" would be a lie about what it computes, which is why it is not called that.
//
// Insertion sort: n is a dozen, and a stable, obvious sort makes the suggested order reproducible for
// equal distances (two stops on the same block keep the order the dispatcher entered them in).
func NearestFirst(depot Point, stops []Point) []int {
	idx := make([]int, len(stops))
	dist := make([]float64, len(stops))
	for i := range stops {
		idx[i] = i
		dist[i] = HaversineKm(depot, stops[i])
	}
	for i := 1; i < len(idx); i++ {
		j := i
		for j > 0 && dist[idx[j-1]] > dist[idx[j]] {
			idx[j-1], idx[j] = idx[j], idx[j-1]
			j--
		}
	}
	return idx
}

func rad(deg float64) float64 { return deg * math.Pi / 180 }
