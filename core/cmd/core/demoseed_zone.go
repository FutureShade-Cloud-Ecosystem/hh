package main

// demoseed_zone.go, the zone the seed reads its calendar day in. The seed's day keys and the day
// stamps on the rows (demoseed_build.go's day, the Driver's org repair) are the DEALER's day, so
// the seed asks the dealer's zone and not a compiled in one: the default build seeds the Ontario
// dealer and keeps the houseday default (America/Toronto), the demo build seeds the Gable Landscape
// Supply dealer and its init (demoseed_glsworld_tax_demo.go) sets the zone the dealer configuration
// it writes names, so the rows' day and the zone the apps and the core read the business day in
// (served on the surface config read) can never disagree.

import (
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// seedZone is the seeded dealer's business day zone. Set at init by the demo build, before any plan
// is built; the zero value is the Ontario dealer's.
var seedZone = houseday.DefaultZone()

// seedLocation is the location the seed reads the dealer's calendar day in.
func seedLocation() *time.Location { return seedZone.Location() }
