// Package houseday answers "which day is it?" for business dates: the dealer's day, in the house
// zone, never the server's zone and never the database session's (CURRENT_DATE). Code that needs
// a business date asks here and binds the answer, so the decision is made in Go where the gates
// can read it.
package houseday

import "time"

// houseZone is the dealer's own zone: Dibbits sells out of Ontario yards, and UTC rolls over in
// the evening there. TestHouseZone_EveryCopyInTheBackendNamesTheSameZone reads this constant.
const houseZone = "America/Toronto"

// Location resolves the house zone, degrading to UTC rather than refusing work if the image ships
// no tzdata, the same trade the module copies of the zone make.
func Location() *time.Location {
	loc, err := time.LoadLocation(houseZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Today is the house zone's YYYY-MM-DD for the given clock (time.Now when nil).
func Today(now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	return now().In(Location()).Format("2006-01-02")
}
