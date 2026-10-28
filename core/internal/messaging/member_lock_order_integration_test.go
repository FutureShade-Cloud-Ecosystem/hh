//go:build integration

// member_lock_order_integration_test.go: one lock order in the messaging
// module, the channel first and the member second (UX-110 round 2).
//
// The rule this file pins: a transaction that writes or locks both a
// msg_channels row and a msg_members row takes the CHANNEL first. The two
// orders cannot be mixed on one room. A seat that takes the channel's lock
// first and a remove that takes the member row first close a cycle on one
// member row of one room, and the database answers that cycle with 40P01,
// which nothing in the module retries: the user sees a 500 and the operation
// does not happen.
//
// The legs race the paths that touch both tables on one room:
//
//   - a seat and a remove of ONE member of ONE group room, 250 rounds;
//   - a mixed burst on ONE room, 200 rounds: posts, reads by the posting
//     members, a seat, a remove, a re-seat and an add.
//
// Zero 40P01 and zero lock timeouts are the bar. Outside the evening guard
// the legs run on a pool whose sessions carry a lock timeout, so a lock wait
// that never clears answers 55P03 instead of hanging the leg.
//
// Under the evening guard (HH_EVENING_GUARD) the legs drop that lock
// timeout, and a 55P03 or a 57014 is counted and logged instead of failed.
// The guard runs the suite against a database whose own clock is pinned by
// libfaketime, and in that build the backend's timers ride one SIGALRM that
// the pin moves, so a timeout can answer early: ci.yml's evening guard step
// says so of statement_timeout, lock_timeout and the deadlock detector, which
// all ride that timer. A 40P01 is a cycle the database found, and a cycle is
// a cycle under any clock, so 40P01 still fails under the guard. A hang is
// still a hang: the ux110r2Settle ceiling catches it and fails it.
package messaging

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ux110r2LockTimeout is the lock timeout every session of the legs' pool
// carries outside the evening guard. A lock wait that never clears must
// answer 55P03 and be counted, not hang the leg until the test binary's own
// ceiling.
const ux110r2LockTimeout = "15s"

// ux110r2Settle is the ceiling on how long one operation may take before the
// leg calls it a hang. It is a ceiling, not a measurement. Under the evening
// guard it is also the only ceiling the legs have, because the pool carries
// no lock timeout there.
const ux110r2Settle = 60 * time.Second

// ux110r2UnderGuard reports whether this run is the evening guard's.
//
// The guard runs the integration suite against a Postgres whose own clock is
// pinned by libfaketime (ci.yml's "Evening guard, the date sensitive
// integration packages" step). In that build the backend's timers ride one
// SIGALRM, and the pin moves that alarm, so a session that carries a lock
// timeout can be answered 55P03 far sooner than the timeout, and a
// statement can be answered 57014 for the same reason. ci.yml's own note on
// that step records that statement_timeout, lock_timeout and the deadlock
// detector all ride the same timer.
//
// None of that says anything about a deadlock. A cycle the database detects
// is a cycle under any clock, so 40P01 fails under the guard exactly as it
// does everywhere else.
func ux110r2UnderGuard() bool {
	v, ok := os.LookupEnv("HH_EVENING_GUARD")
	return ok && v != ""
}

// ux110r2Pool opens a pool whose sessions carry the lock timeout outside the
// evening guard, and gives it enough connections for one burst's worth of
// concurrent operations.
func ux110r2Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, ok := os.LookupEnv("DATABASE_URL")
	if !ok || url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse the database url: %v", err)
	}
	if ux110r2UnderGuard() {
		// Under the guard the legs carry NO lock timeout. The pinned
		// database's own timer rides the SIGALRM that libfaketime moves,
		// so a timeout there can fire at the wrong moment and answer
		// 55P03 for a wait that never came close to it (ci.yml's evening
		// guard step records that statement_timeout, lock_timeout and the
		// deadlock detector share that timer). A wait that never clears
		// is still a failure: the ux110r2Settle ceiling catches it.
		delete(cfg.ConnConfig.RuntimeParams, "lock_timeout")
		delete(cfg.ConnConfig.RuntimeParams, "statement_timeout")
	} else {
		cfg.ConnConfig.RuntimeParams["lock_timeout"] = ux110r2LockTimeout
	}
	cfg.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open the legs' pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// ux110r2Kind names the failures this lane bars: the deadlock the
// database detects (40P01), the lock wait that ran out of time (55P03) and
// the statement the server canceled (57014). Anything else is "other", and
// the legs report it rather than hide it.
func ux110r2Kind(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "40P01"), strings.Contains(msg, "deadlock detected"):
		return "40P01"
	case strings.Contains(msg, "55P03"), strings.Contains(msg, "lock timeout"):
		return "55P03"
	case strings.Contains(msg, "57014"), strings.Contains(msg, "canceling statement"):
		return "57014"
	}
	return "other"
}

// ux110r2Tally counts the kinds a leg's errors can be.
type ux110r2Tally struct {
	deadlocks int
	timeouts  int
	canceled  int
	others    int
	samples   []string
}

// add files one error under its kind and keeps the first few examples of an
// unexpected one, so the log says what the other errors were.
func (t *ux110r2Tally) add(err error) {
	switch ux110r2Kind(err) {
	case "40P01":
		t.deadlocks++
	case "55P03":
		t.timeouts++
	case "57014":
		t.canceled++
	case "other":
		t.others++
		if len(t.samples) < 5 {
			t.samples = append(t.samples, err.Error())
		}
	}
}

// ux110r2CheckTimeoutKinds applies the bar for the two kinds that only a
// pinned clock can answer wrongly: the lock wait that ran out of time
// (55P03) and the statement the server canceled (57014). Outside the guard
// both are failures. Under the guard both are counted and logged, because
// the guard's own database answers them from a timer that libfaketime
// moves (ci.yml's evening guard step: statement_timeout, lock_timeout and
// the deadlock detector ride that timer). A 40P01 is never in this bar: a
// deadlock is a deadlock under any clock.
func ux110r2CheckTimeoutKinds(t *testing.T, tally *ux110r2Tally, rounds int, what string) {
	t.Helper()
	if tally.timeouts != 0 {
		if ux110r2UnderGuard() {
			t.Logf("%s: %d of %d rounds answered 55P03, counted and not failed under the evening guard",
				what, tally.timeouts, rounds)
		} else {
			t.Errorf("%s: %d of %d rounds answered 55P03", what, tally.timeouts, rounds)
		}
	}
	if tally.canceled != 0 {
		if ux110r2UnderGuard() {
			t.Logf("%s: %d of %d rounds answered 57014, counted and not failed under the evening guard",
				what, tally.canceled, rounds)
		} else {
			t.Errorf("%s: %d of %d rounds answered 57014", what, tally.canceled, rounds)
		}
	}
}

// ux110r2GroupRoom opens a group room of the leg's own and seats the leg's
// members in it. The room goes when the leg ends, so its rows stay out of
// the seeded rooms and out of every other leg's counts.
func ux110r2GroupRoom(t *testing.T, pool *pgxpool.Pool, repo *Repository, tag string) string {
	t.Helper()
	ctx := context.Background()
	group, err := repo.CreateGroup(ctx, uAlexMorgan, ux110Body("r2-"+tag), nil)
	if err != nil {
		t.Fatalf("create the leg's group room: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM msg_channels WHERE id = $1::uuid`, group.ID); err != nil {
			t.Errorf("drop the leg's group room: %v", err)
		}
	})
	return group.ID
}

// UX-110 round 2: a seat and a remove of one member of one group room.
//
// The OLD order, which this leg guards against, was the seat taking the
// channel's lock and then writing the member row, while the remove wrote
// the member row and then took the channel's lock. On one member row of one
// room those two orders are inverses, and the cycle closes: the remove's
// DELETE holds the member row, the seat's channel lock blocks the remove's
// feed bump, and the seat's INSERT waits for the member row the remove
// holds.
//
// The order today is the same on both sides: the channel first, the member
// second. This leg is the guard that keeps it there.
//
// Red on the head before the fix: the database answers 40P01 in a large share
// of the rounds, and the module does not retry it, so the user sees a 500.
func TestIntegration_SeatAndRemove_OneGroupRoom_OneMember_NeverDeadlocks(t *testing.T) {
	ctx := context.Background()
	pool := ux110r2Pool(t)
	repo := NewRepository(pool)
	group := ux110r2GroupRoom(t, pool, repo, "seat-remove")

	const rounds = 250
	var tally ux110r2Tally
	for round := 0; round < rounds; round++ {
		// The member is seated at the start of every round, so the remove
		// has a row to take and the seat has a row to conflict with.
		if _, _, err := repo.SeatGroupMember(ctx, group, uSamRivera, "the owner", "the guest"); err != nil {
			t.Fatalf("seat the member before round %d: %v", round, err)
		}

		start := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			<-start
			_, _, err := repo.SeatGroupMember(ctx, group, uSamRivera, "the owner", "the guest")
			results <- err
		}()
		go func() {
			<-start
			results <- repo.RemoveChannelMember(ctx, group, uSamRivera)
		}()
		close(start)
		for i := 0; i < 2; i++ {
			select {
			case err := <-results:
				tally.add(err)
			case <-time.After(ux110r2Settle):
				t.Fatalf("round %d: a member operation never answered", round)
			}
		}
	}

	t.Logf("seat and remove of one member of one group room: %d rounds, 40P01 %d, 55P03 %d, 57014 %d, other %d",
		rounds, tally.deadlocks, tally.timeouts, tally.canceled, tally.others)
	for i, s := range tally.samples {
		t.Logf("other error %d: %s", i+1, s)
	}
	if tally.deadlocks != 0 {
		t.Errorf("a seat and a remove of one member of one room must not deadlock: %d of %d rounds answered 40P01",
			tally.deadlocks, rounds)
	}
	ux110r2CheckTimeoutKinds(t, &tally, rounds,
		"a seat and a remove of one member of one room must not wait out a lock timeout")
}

// UX-110 round 2: the mixed burst on one room.
//
// Everything the module does to one room's membership, at once: a post, a
// read by the posting member, a seat, a remove, a re-seat and an add, all
// against one room and one churning member row. The posts and the reads
// take the channel's lock, and the seat, the remove and the add take it
// too. The bar is the same: no 40P01, no 55P03.
//
// The reads and the posts are by members nothing in the burst removes, so
// they must all succeed. The churn trio may answer not-found (the remove
// can find no row when the seat has not landed yet), which is the churn's
// own outcome and not a failure.
func TestIntegration_MixedBurst_OneGroupRoom_NeverDeadlocksAndNeverTimesOut(t *testing.T) {
	ctx := context.Background()
	pool := ux110r2Pool(t)
	repo := NewRepository(pool)
	group := ux110r2GroupRoom(t, pool, repo, "burst")

	// The reader and the poster are seated once and never churn: nothing in
	// the burst removes them, so their reads must always land.
	if _, _, err := repo.SeatGroupMember(ctx, group, uSamRivera, "the owner", "the reader"); err != nil {
		t.Fatalf("seat the reader: %v", err)
	}

	type outcome struct {
		name string
		err  error
	}

	const rounds = 200
	var tally ux110r2Tally
	var missing int
	for round := 0; round < rounds; round++ {
		// The churning member is seated at the start of every round.
		if _, _, err := repo.SeatGroupMember(ctx, group, uCaseyDoyle, "the owner", "the guest"); err != nil {
			t.Fatalf("seat the churning member before round %d: %v", round, err)
		}

		start := make(chan struct{})
		results := make(chan outcome, 7)
		post := func(userID string) {
			_, err := repo.InsertMessage(ctx, group, userID, ux110Body("burst-post"), "")
			results <- outcome{name: "post", err: err}
		}
		read := func(userID string) {
			results <- outcome{name: "read", err: repo.MarkRead(ctx, group, userID)}
		}
		go func() { <-start; post(uSamRivera) }()
		go func() { <-start; post(uAlexMorgan) }()
		go func() { <-start; read(uSamRivera) }()
		go func() { <-start; read(uAlexMorgan) }()
		go func() {
			<-start
			_, _, err := repo.SeatGroupMember(ctx, group, uCaseyDoyle, "the owner", "the guest")
			results <- outcome{name: "seat", err: err}
		}()
		go func() {
			<-start
			results <- outcome{name: "remove", err: repo.RemoveChannelMember(ctx, group, uCaseyDoyle)}
		}()
		go func() {
			<-start
			_, err := repo.AddChannelMember(ctx, AddMemberInput{
				ChannelID: group,
				UserID:    uCaseyDoyle,
				Role:      RoleMember,
			})
			results <- outcome{name: "add", err: err}
		}()
		close(start)

		for i := 0; i < 7; i++ {
			select {
			case out := <-results:
				tally.add(out.err)
				switch {
				case out.err == nil:
				case ux110r2Kind(out.err) == "other":
					// The remove can find no row when the seat has not
					// landed yet: that is the churn's own outcome.
					if out.name == "remove" && errors.Is(out.err, errNotFound) {
						missing++
						continue
					}
					t.Errorf("round %d: the %s must land: %v", round, out.name, out.err)
				case ux110r2Kind(out.err) == "40P01":
					// A deadlock is a deadlock under any clock, so it
					// fails under the guard exactly as it does
					// everywhere else.
					t.Errorf("round %d: the %s deadlocked: %v", round, out.name, out.err)
				case ux110r2UnderGuard():
					// Under the evening guard a timeout or a
					// cancellation comes from the pinned database's own
					// timer, which libfaketime moves (ci.yml's evening
					// guard step: statement_timeout, lock_timeout and the
					// deadlock detector ride that timer). It is counted
					// and logged here, not failed.
					t.Logf("round %d: the %s answered %s under the evening guard, counted and not failed: %v",
						round, out.name, ux110r2Kind(out.err), out.err)
				default:
					t.Errorf("round %d: the %s must land: %v", round, out.name, out.err)
				}
			case <-time.After(ux110r2Settle):
				t.Fatalf("round %d: an operation never answered", round)
			}
		}
	}

	t.Logf("mixed burst on one group room: %d rounds, 40P01 %d, 55P03 %d, 57014 %d, other %d, removes that found no row %d",
		rounds, tally.deadlocks, tally.timeouts, tally.canceled, tally.others, missing)
	for i, s := range tally.samples {
		t.Logf("other error %d: %s", i+1, s)
	}
	if tally.deadlocks != 0 {
		t.Errorf("the mixed burst on one room must not deadlock: %d deadlocks across %d rounds",
			tally.deadlocks, rounds)
	}
	ux110r2CheckTimeoutKinds(t, &tally, rounds,
		"the mixed burst on one room must not wait out a lock timeout")
}
