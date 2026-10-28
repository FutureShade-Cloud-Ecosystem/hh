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
// Zero 40P01 and zero lock timeouts are the bar. The legs run on a pool
// whose sessions carry a lock timeout, so a lock wait that never clears
// answers 55P03 instead of hanging the leg.
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
// carries. A lock wait that never clears must answer 55P03 and be counted,
// not hang the leg until the test binary's own ceiling.
const ux110r2LockTimeout = "15s"

// ux110r2Settle is the ceiling on how long one operation may take before the
// leg calls it a hang. It is a ceiling, not a measurement.
const ux110r2Settle = 60 * time.Second

// ux110r2Pool opens a pool whose sessions carry the lock timeout, and gives
// it enough connections for one burst's worth of concurrent operations.
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
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = ux110r2LockTimeout
	cfg.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open the legs' pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// ux110r2Kind names the two failures this lane bars: the deadlock the
// database detects (40P01) and the lock wait that ran out of time (55P03).
// Anything else is "other", and the legs report it rather than hide it.
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
	}
	return "other"
}

// ux110r2Tally counts the three kinds a leg's errors can be.
type ux110r2Tally struct {
	deadlocks int
	timeouts  int
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
	case "other":
		t.others++
		if len(t.samples) < 5 {
			t.samples = append(t.samples, err.Error())
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
// The seat takes the channel's lock and then writes the member row. The
// remove writes the member row and then takes the channel's lock. On one
// member row of one room those two orders are inverses, and the cycle closes:
// the remove's DELETE holds the member row, the seat's channel lock blocks
// the remove's feed bump, and the seat's INSERT waits for the member row the
// remove holds.
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

	t.Logf("seat and remove of one member of one group room: %d rounds, 40P01 %d, 55P03 %d, other %d",
		rounds, tally.deadlocks, tally.timeouts, tally.others)
	for i, s := range tally.samples {
		t.Logf("other error %d: %s", i+1, s)
	}
	if tally.deadlocks != 0 {
		t.Errorf("a seat and a remove of one member of one room must not deadlock: %d of %d rounds answered 40P01",
			tally.deadlocks, rounds)
	}
	if tally.timeouts != 0 {
		t.Errorf("a seat and a remove of one member of one room must not wait out a lock timeout: %d of %d rounds answered 55P03",
			tally.timeouts, rounds)
	}
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
				if out.err != nil && ux110r2Kind(out.err) == "other" {
					// The remove can find no row when the seat has not
					// landed yet: that is the churn's own outcome.
					if out.name == "remove" && errors.Is(out.err, errNotFound) {
						missing++
						continue
					}
					t.Errorf("round %d: the %s must land: %v", round, out.name, out.err)
				}
			case <-time.After(ux110r2Settle):
				t.Fatalf("round %d: an operation never answered", round)
			}
		}
	}

	t.Logf("mixed burst on one group room: %d rounds, 40P01 %d, 55P03 %d, other %d, removes that found no row %d",
		rounds, tally.deadlocks, tally.timeouts, tally.others, missing)
	for i, s := range tally.samples {
		t.Logf("other error %d: %s", i+1, s)
	}
	if tally.deadlocks != 0 {
		t.Errorf("the mixed burst on one room must not deadlock: %d deadlocks across %d rounds",
			tally.deadlocks, rounds)
	}
	if tally.timeouts != 0 {
		t.Errorf("the mixed burst on one room must not wait out a lock timeout: %d lock timeouts across %d rounds",
			tally.timeouts, rounds)
	}
}
