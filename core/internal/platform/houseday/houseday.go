// Package houseday answers "which day is it?" for business dates: the dealer's day, in the house
// zone, never the server's zone and never the database session's (CURRENT_DATE). Code that needs
// a business date asks here and binds the answer, so the decision is made in Go where the gates
// can read it.
package houseday

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// houseZone is the dealer's own zone: Dibbits sells out of Ontario yards, and UTC rolls over in
// the evening there. TestHouseZone_EveryCopyInTheBackendNamesTheSameZone reads this constant.
const houseZone = "America/Toronto"

// zoneOnce resolves houseZone once for the process. LoadLocation re-reads and re-parses the
// tzdata on every call, and every business-date decision in the backend asks this package, so
// the parse is paid once and every caller shares one resolved zone.
var (
	zoneOnce sync.Once
	zoneLoc  *time.Location
)

// Location resolves the house zone, degrading to UTC rather than refusing work if the image ships
// no tzdata, the same trade the module copies of the zone make.
func Location() *time.Location {
	zoneOnce.Do(func() {
		loc, err := time.LoadLocation(houseZone)
		if err != nil {
			loc = time.UTC
		}
		zoneLoc = loc
	})
	return zoneLoc
}

// Today is the house zone's YYYY-MM-DD for the given clock (time.Now when nil).
func Today(now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	return dayText(now())
}

// AddDays is the house zone's day a whole calendar days from the day of the given clock (time.Now
// when nil), the shape a payment terms or a due-on-issue rule needs. The shift is applied inside
// the house zone, so it is a calendar day and not a 24 hour multiple: a term counted across a
// daylight saving change lands on the same calendar day the wall clock would name.
func AddDays(now func() time.Time, days int) string {
	if now == nil {
		now = time.Now
	}
	return now().In(Location()).AddDate(0, 0, days).Format("2006-01-02")
}

// dayText is one instant's date in the house zone.
func dayText(at time.Time) string {
	return at.In(Location()).Format("2006-01-02")
}

// JustAfterUTCDayChange is the one pinned instant every date test in the house-day work runs at:
// half past UTC midnight on the given day, the moment the UTC day has rolled over but the house
// zone (four or five hours behind) is still on the previous evening. Any date read that follows
// the UTC day is one ahead of the dealer's day at this instant, so a test pinned here is red on
// code that reads the day at Greenwich whatever the wall clock says when the suite runs. day is
// any instant on the wanted UTC day.
func JustAfterUTCDayChange(day time.Time) time.Time {
	d := day.UTC()
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 30, 0, 0, time.UTC)
}

// ParseDay reads a YYYY-MM-DD as that day in the house zone: the instant its house midnight begins.
// A bare time.Parse gives midnight at Greenwich, which every house-day reader then takes for the
// previous evening, so a date a caller names must come through here.
func ParseDay(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, Location())
}

// EndOfDay is the last instant of the named house day (a microsecond before the next house
// midnight, the finest grain the database keeps). An as-of that names a day inclusively cuts both
// a date column and a posting instant here: the house day of the answer is the day asked for, and
// every instant of that day is at or before it.
func EndOfDay(s string) (time.Time, error) {
	d, err := ParseDay(s)
	if err != nil {
		return time.Time{}, err
	}
	return d.AddDate(0, 0, 1).Add(-time.Microsecond), nil
}

// Zone is one dealer's business day zone: the calendar its dates are filed in. It is a value, so a
// service holds the zone its dealer chose and two dealers in one process never share one. The zero
// Zone is the Ontario dealer's (America/Toronto), which is what a dealer with no configuration is, so
// a service built without a zone behaves exactly as before the zone was configurable. The package
// level functions above are that default zone's shorthand; a production caller takes its zone from
// its dealer (Source) instead.
type Zone struct{ loc *time.Location }

// zoneCache memoises the tzdata parse per zone name (LoadLocation re-reads the files on every call).
// It holds only the immutable result of a lookup by name, never a dealer's choice.
var zoneCache sync.Map

// NewZone resolves an IANA zone name. It refuses an empty name, "Local" (the server's zone, which
// nobody chose) and any name the tzdata does not know, so a dealer's configuration never silently
// files its days in another calendar.
func NewZone(name string) (Zone, error) {
	if name == "" || name == "Local" {
		return Zone{}, fmt.Errorf("houseday: %q is not a business zone name", name)
	}
	if v, ok := zoneCache.Load(name); ok {
		return Zone{loc: v.(*time.Location)}, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return Zone{}, fmt.Errorf("houseday: business zone %q: %w", name, err)
	}
	v, _ := zoneCache.LoadOrStore(name, loc)
	return Zone{loc: v.(*time.Location)}, nil
}

// DefaultZone is the Ontario dealer's zone, America/Toronto.
func DefaultZone() Zone { return Zone{} }

// Location is the zone's *time.Location (the Ontario default's for the zero Zone).
func (z Zone) Location() *time.Location {
	if z.loc == nil {
		return Location()
	}
	return z.loc
}

// Name is the zone's IANA name.
func (z Zone) Name() string { return z.Location().String() }

// Today is the zone's YYYY-MM-DD for the given clock (time.Now when nil).
func (z Zone) Today(now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	return now().In(z.Location()).Format("2006-01-02")
}

// AddDays is the zone's day a whole calendar days from the day of the given clock, shifted inside the
// zone so it is a calendar day and not a 24 hour multiple.
func (z Zone) AddDays(now func() time.Time, days int) string {
	if now == nil {
		now = time.Now
	}
	return now().In(z.Location()).AddDate(0, 0, days).Format("2006-01-02")
}

// ParseDay reads a YYYY-MM-DD as that day in the zone: the instant its midnight begins.
func (z Zone) ParseDay(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, z.Location())
}

// EndOfDay is the last instant (a microsecond before the next midnight) of the named day in the zone.
func (z Zone) EndOfDay(s string) (time.Time, error) {
	d, err := z.ParseDay(s)
	if err != nil {
		return time.Time{}, err
	}
	return d.AddDate(0, 0, 1).Add(-time.Microsecond), nil
}

// Source is how a service learns its dealer's zone. The composition root adapts the settings
// module's dealer configuration into one; each ask reads it afresh (no cache), so a change of zone is
// live at the next business day decision.
type Source interface {
	BusinessZone(ctx context.Context) (Zone, error)
}

// TxSource is a Source that can also read the zone on a caller's open transaction. A service that
// writes inside a transaction its caller owns reads the zone through it, never through the pool: the
// caller's transaction holds a connection, and a pool read beside it waits on a second one, which a
// full pool of such callers never frees.
type TxSource interface {
	Source
	BusinessZoneTx(ctx context.Context, tx pgx.Tx) (Zone, error)
}

// ResolveTx is Resolve for a service running inside its caller's transaction: with a TxSource and a
// transaction the zone is read on that transaction, otherwise it is Resolve.
func ResolveTx(ctx context.Context, src Source, tx pgx.Tx) (Zone, error) {
	if ts, ok := src.(TxSource); ok && tx != nil {
		z, err := ts.BusinessZoneTx(ctx, tx)
		if err != nil {
			return Zone{}, fmt.Errorf("houseday: the dealer's business zone could not be read: %w", err)
		}
		return z, nil
	}
	return Resolve(ctx, src)
}

// Resolve answers a service's zone for one decision: the Ontario dealer's with no source wired (the
// scaffold and the test fakes), the source's otherwise. A source that fails refuses the decision
// rather than guessing a calendar.
func Resolve(ctx context.Context, src Source) (Zone, error) {
	if src == nil {
		return Zone{}, nil
	}
	z, err := src.BusinessZone(ctx)
	if err != nil {
		return Zone{}, fmt.Errorf("houseday: the dealer's business zone could not be read: %w", err)
	}
	return z, nil
}
