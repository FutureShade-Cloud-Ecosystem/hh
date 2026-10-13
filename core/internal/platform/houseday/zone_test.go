package houseday

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Item 3b slice 3: the business day is the dealer's own zone, injected per service. The package level
// functions stay the Ontario dealer's (a dealer with no configuration is America/Toronto exactly).

func TestZone_TheZeroZoneIsTheOntarioDealer(t *testing.T) {
	var z Zone
	if z.Name() != "America/Toronto" {
		t.Fatalf("zero zone names %q, want America/Toronto", z.Name())
	}
	at := time.Date(2026, 7, 2, 4, 30, 0, 0, time.UTC)
	if got, want := z.Today(func() time.Time { return at }), Today(func() time.Time { return at }); got != want {
		t.Fatalf("zero zone day %s, package default %s", got, want)
	}
}

// A Los Angeles dealer's evening is a different calendar day than Toronto's at the same instant.
func TestZone_ANamedZoneFilesTheDayInItsOwnCalendar(t *testing.T) {
	la, err := NewZone("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 2, 4, 30, 0, 0, time.UTC) // 00:30 in Toronto, 21:30 the evening before in Los Angeles
	now := func() time.Time { return at }
	if got := la.Today(now); got != "2026-07-01" {
		t.Fatalf("Los Angeles day = %s, want 2026-07-01", got)
	}
	if got := DefaultZone().Today(now); got != "2026-07-02" {
		t.Fatalf("Toronto day = %s, want 2026-07-02", got)
	}
	if got := la.AddDays(now, 30); got != "2026-07-31" {
		t.Fatalf("Los Angeles AddDays(30) = %s, want 2026-07-31", got)
	}
}

func TestZone_EndOfDayAndParseDayFollowTheZone(t *testing.T) {
	la, _ := NewZone("America/Los_Angeles")
	for _, day := range []string{"2026-03-08", "2026-11-01", "2026-12-31"} {
		end, err := la.EndOfDay(day)
		if err != nil {
			t.Fatal(err)
		}
		if got := la.Today(func() time.Time { return end }); got != day {
			t.Fatalf("%s: the end of day reads as %s", day, got)
		}
		if got := la.Today(func() time.Time { return end.Add(time.Microsecond) }); got == day {
			t.Fatalf("%s: a microsecond later is still the same day", day)
		}
		start, _ := la.ParseDay(day)
		if got := la.Today(func() time.Time { return start }); got != day {
			t.Fatalf("%s: ParseDay starts on %s", day, got)
		}
	}
}

func TestNewZone_RefusesWhatIsNotAZone(t *testing.T) {
	for _, bad := range []string{"", "Local", "Nowhere/Land", "toronto"} {
		if _, err := NewZone(bad); err == nil {
			t.Fatalf("zone %q was accepted", bad)
		}
	}
}

// Two dealers in one process each get their own calendar: a zone is a value, nothing leaks between them.
func TestZone_TwoDealersInOneProcessDoNotLeak(t *testing.T) {
	la, _ := NewZone("America/Los_Angeles")
	to := DefaultZone()
	at := time.Date(2026, 7, 2, 4, 30, 0, 0, time.UTC)
	now := func() time.Time { return at }
	for i := 0; i < 3; i++ {
		if la.Today(now) != "2026-07-01" || to.Today(now) != "2026-07-02" {
			t.Fatalf("round %d: the zones leaked into each other", i)
		}
	}
}

type fixedSource struct {
	z   Zone
	err error
}

func (f fixedSource) BusinessZone(context.Context) (Zone, error) { return f.z, f.err }

func TestResolve_NoSourceIsTheOntarioDealerAndAFailedReadRefuses(t *testing.T) {
	z, err := Resolve(context.Background(), nil)
	if err != nil || z.Name() != "America/Toronto" {
		t.Fatalf("no source: %v %v, want the Ontario dealer", z.Name(), err)
	}
	la, _ := NewZone("America/Los_Angeles")
	if z, err = Resolve(context.Background(), fixedSource{z: la}); err != nil || z.Name() != "America/Los_Angeles" {
		t.Fatalf("a source answers its zone: %v %v", z.Name(), err)
	}
	boom := errors.New("settings unavailable")
	if _, err = Resolve(context.Background(), fixedSource{err: boom}); !errors.Is(err, boom) {
		t.Fatalf("a failed read must refuse, got %v", err)
	}
}

type txFixed struct {
	fixedSource
	txReads, poolReads int
}

func (s *txFixed) BusinessZone(ctx context.Context) (Zone, error) {
	s.poolReads++
	return s.z, nil
}

func (s *txFixed) BusinessZoneTx(context.Context, pgx.Tx) (Zone, error) {
	s.txReads++
	return s.z, nil
}

// A service inside its caller's transaction reads the zone on that transaction, never the pool.
func TestResolveTx_ReadsOnTheCallersTransactionWhenThereIsOne(t *testing.T) {
	la, _ := NewZone("America/Los_Angeles")
	s := &txFixed{fixedSource: fixedSource{z: la}}
	var tx pgx.Tx = txStub{}
	if z, err := ResolveTx(context.Background(), s, tx); err != nil || z.Name() != "America/Los_Angeles" {
		t.Fatalf("with a tx: %v %v", z.Name(), err)
	}
	if s.txReads != 1 || s.poolReads != 0 {
		t.Fatalf("tx reads %d pool reads %d, want the transaction only", s.txReads, s.poolReads)
	}
	if _, err := ResolveTx(context.Background(), s, nil); err != nil || s.poolReads != 1 {
		t.Fatalf("with no tx the pool read serves: %v pool reads %d", err, s.poolReads)
	}
	if z, err := ResolveTx(context.Background(), nil, tx); err != nil || z.Name() != "America/Toronto" {
		t.Fatalf("no source: %v %v", z.Name(), err)
	}
}

type txStub struct{ pgx.Tx }
