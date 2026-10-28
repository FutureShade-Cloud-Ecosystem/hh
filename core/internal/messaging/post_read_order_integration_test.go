//go:build integration

// post_read_order_integration_test.go: a post and a read of one channel are
// ordered by the channel's lock (UX-110).
//
// The defect this file pins: a post that was still in flight when a read
// landed read as already read, so the badge never lit for it. A cursor
// computed from the stamps it can see cannot repair that, because a post that
// has not committed is invisible to it. The order has to come from a lock. The
// post takes the channel's lock first and stamps itself with the clock it
// holds that lock at; the read takes the same lock (FOR UPDATE) before it
// writes the cursor. A post that commits before the cursor is stamped before
// it, so it reads as unread. A post that comes after waits for the lock and
// is stamped after the cursor.
//
// Every leg here runs against the migrated and seeded database (the same
// DATABASE_URL the integration suite uses). The names are avoided on purpose:
// the legs talk about the reader, the author and the channel.
package messaging

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ux110Wait is how long a leg waits before it calls a path that did not wait
// a failure. It is long enough that a path that is going to block has blocked
// by then, and short enough that the suite stays quick.
const ux110Wait = 250 * time.Millisecond

// ux110Settle is the ceiling on how long a path may take after the lock it
// waits for is free. It is a ceiling, not a measurement.
const ux110Settle = 30 * time.Second

// ux110Posted is one post path's answer.
type ux110Posted struct {
	id  string
	err error
}

// ux110Body is a body unique to the leg that writes it, so a count can find
// it again.
func ux110Body(tag string) string {
	return fmt.Sprintf("ux110-%s-%d", tag, time.Now().UnixNano())
}

// ux110Ready opens a room of the leg's own, seats the reader and the author in
// it, and puts the reader's cursor at the database's clock, so the badge starts
// dark and only the leg's own posts can light it.
//
// The room is the leg's own and is dropped when the leg ends (the messages
// cascade with it), so the legs leave the seeded rooms and the other legs'
// counts exactly as they found them. A leg that posted into a seeded room
// would push the rows the sync feeds serve past their page, and a room of its
// own keeps this file's rows out of everyone else's feed.
func ux110Ready(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var channelID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, name, topic, tier)
		VALUES ('TEAM', $1, '', 'STANDARD')
		RETURNING id::text`, ux110Body("room")).Scan(&channelID); err != nil {
		t.Fatalf("open the leg's room: %v", err)
	}
	for _, u := range []string{uSamRivera, uAlexMorgan} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO msg_members (channel_id, user_id, role, source)
			VALUES ($1, $2, 'MEMBER', 'EXPLICIT')`, channelID, u); err != nil {
			t.Fatalf("seat the leg's member: %v", err)
		}
	}
	if _, err := pool.Exec(ctx,
		`UPDATE msg_members SET last_read_at = now() WHERE channel_id = $1 AND user_id = $2`,
		channelID, uSamRivera); err != nil {
		t.Fatalf("reset the read cursor: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM msg_channels WHERE id = $1::uuid`, channelID); err != nil {
			t.Errorf("drop the leg's room: %v", err)
		}
	})
	return channelID
}

// ux110HoldLock takes the channel's lock in the probe's mode, in its own
// transaction, and hands back the release. The release is safe to call twice:
// the second call does nothing.
//
// The mode is FOR NO KEY UPDATE, and it is the only mode that isolates what
// these legs are about. Every insert into msg_messages checks the channel's
// foreign key, and that check takes a FOR KEY SHARE lock on the channel row
// whatever the path does. FOR NO KEY UPDATE is COMPATIBLE with that key share
// and INCOMPATIBLE with both FOR SHARE (the mode a post takes) and FOR NO KEY
// UPDATE itself (the mode a path that writes msg_channels takes). So a path
// that takes the channel's lock waits here, and a path that does not takes
// only the foreign key's key share and sails through. A FOR UPDATE probe
// would not tell the two apart: the foreign key's key share conflicts with
// it too, so a path with no lock of its own would wait and the leg would
// pass for the wrong reason.
func ux110HoldLock(ctx context.Context, t *testing.T, pool *pgxpool.Pool, channelID string) (pgx.Tx, func()) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the holding transaction: %v", err)
	}
	var one int
	if err := tx.QueryRow(ctx,
		`SELECT 1 FROM msg_channels WHERE id = $1::uuid FOR NO KEY UPDATE`, channelID).Scan(&one); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("take the channel's lock: %v", err)
	}
	released := false
	return tx, func() {
		if released {
			return
		}
		released = true
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("release the channel's lock: %v", err)
		}
	}
}

// UX-110: the held post, the read's side.
//
// A post that holds the channel's lock is still in flight. The read must wait
// for it, because the lock is what orders the two. And the post's write, which
// landed before the read, reads as read: the read came after it.
//
// Red on the head before the fix: the read waited for nothing, so it stamped
// its cursor while the post was still open.
func TestIntegration_HeldPost_TheReadWaitsForThePostInFlight(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	ch := ux110Ready(t, pool)

	// The post, held open across the read. It takes the channel's lock the
	// way every post path takes it, and stamps itself with the clock it holds
	// that lock at.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the post's transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var one int
	if err := tx.QueryRow(ctx,
		`SELECT 1 FROM msg_channels WHERE id = $1::uuid FOR SHARE`, ch).Scan(&one); err != nil {
		t.Fatalf("take the channel's lock: %v", err)
	}
	body := ux110Body("held")
	if _, err := tx.Exec(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, created_at)
		VALUES ($1, $2, 'TEXT', $3, clock_timestamp())`,
		ch, uAlexMorgan, body); err != nil {
		t.Fatalf("write the held post: %v", err)
	}

	// The read, in flight while the post is still open.
	answered := make(chan error, 1)
	go func() { answered <- repo.MarkRead(ctx, ch, uSamRivera) }()
	select {
	case err := <-answered:
		t.Fatalf("the read must wait for the post in flight, it answered %v", err)
	case <-time.After(ux110Wait):
	}

	// The post commits.
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the held post: %v", err)
	}
	select {
	case err := <-answered:
		if err != nil {
			t.Fatalf("mark read: %v", err)
		}
	case <-time.After(ux110Settle):
		t.Fatal("the read never answered")
	}

	// The post's write landed before the read, so the read covers it: the
	// badge stays dark for it. A post that lands AFTER the read is the other
	// leg.
	if got := unreadFor(ctx, t, repo, uSamRivera, ch); got != 0 {
		t.Errorf("a post whose write landed before the read is read, got %d unread", got)
	}
	var landed int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM msg_messages WHERE channel_id = $1 AND body = $2`,
		ch, body).Scan(&landed); err != nil {
		t.Fatalf("count the held post: %v", err)
	}
	if landed != 1 {
		t.Errorf("the held post must land, counted %d", landed)
	}
}

// UX-110: the held post, the badge.
//
// The post's transaction is held open across the read, so the post's WRITE
// lands after the read's cursor. The badge must light for it: the user read
// the room before the post was there.
//
// This leg drives the REAL post path through the repository's test seam, which
// holds the post's own transaction open right after it opens. A raw
// transaction that mirrors the path cannot stand in here: a mirror stamps
// itself the way the mirror is written, so it would carry the fix's own
// stamping and never fail.
//
// Red on the head before the fix: the post stamped itself with the clock its
// transaction BEGAN at, which is before the read's cursor, so a post that
// landed after the read read as already read and the badge stayed dark.
func TestIntegration_HeldPost_TheBadgeLightsForAPostThatLandsAfterTheRead(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	ch := ux110Ready(t, pool)

	// The post's transaction, held open across the read.
	held := make(chan struct{})
	release := make(chan struct{})
	landed := make(chan ux110Posted, 1)
	repo.afterPostBegin = func(context.Context) {
		close(held)
		<-release
	}
	go func() {
		m, err := repo.InsertMessage(ctx, ch, uAlexMorgan, ux110Body("landed"), "")
		landed <- ux110Posted{id: m.ID, err: err}
	}()
	select {
	case <-held:
	case <-time.After(ux110Settle):
		t.Fatal("the post never opened its transaction")
	}

	// The read, while the post's transaction is open.
	if err := repo.MarkRead(ctx, ch, uSamRivera); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	// The post lands, after the read's cursor.
	close(release)
	select {
	case p := <-landed:
		if p.err != nil {
			t.Fatalf("insert message: %v", p.err)
		}
	case <-time.After(ux110Settle):
		t.Fatal("the post never landed")
	}

	// The badge lights: the post landed after the cursor the read wrote.
	if got := unreadFor(ctx, t, repo, uSamRivera, ch); got < 1 {
		t.Errorf("a post that lands after the read must light the badge, got %d unread", got)
	}
}

// UX-110: the post path itself.
//
// The post path must take the channel's lock, or a read that lands while the
// post is in flight cannot be ordered against it. And the post must stamp
// itself with the clock it holds the lock at, not the clock its transaction
// began at: the transaction begins before the lock is free.
//
// Red on the head before the fix: the path never took the lock, so it landed
// while the lock was held, and its stamp was the clock its transaction began
// at (before the lock was free).
func TestIntegration_InsertMessage_WaitsForTheChannelLockAndStampsAfterIt(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	ch := ux110Ready(t, pool)

	// The channel's lock, held the way a read holds it.
	_, release := ux110HoldLock(ctx, t, pool, ch)
	defer release()

	posted := make(chan ux110Posted, 1)
	go func() {
		m, err := repo.InsertMessage(ctx, ch, uAlexMorgan, ux110Body("locked"), "")
		posted <- ux110Posted{id: m.ID, err: err}
	}()

	// The post must wait: the channel's lock is what orders a post and a read.
	select {
	case p := <-posted:
		t.Fatalf("the post must wait for the channel's lock, it answered %v", p.err)
	case <-time.After(ux110Wait):
	}

	// The database's clock, read while the post waits.
	var heldAt time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&heldAt); err != nil {
		t.Fatalf("read the database's clock: %v", err)
	}

	release()

	var p ux110Posted
	select {
	case p = <-posted:
	case <-time.After(ux110Settle):
		t.Fatal("the post never landed")
	}
	if p.err != nil {
		t.Fatalf("insert message: %v", p.err)
	}
	var stamped time.Time
	if err := pool.QueryRow(ctx,
		`SELECT created_at FROM msg_messages WHERE id = $1::uuid`, p.id).Scan(&stamped); err != nil {
		t.Fatalf("read the post's stamp: %v", err)
	}
	// The stamp is the clock the post holds the lock at, not the clock its
	// transaction began at (the transaction began before the lock was free).
	if stamped.Before(heldAt) {
		t.Errorf("the post must stamp itself with the clock it holds the channel's lock at: stamped %s, the clock while it waited %s",
			stamped.UTC(), heldAt.UTC())
	}
}

// UX-110: two posts, one channel.
//
// The channel's lock must not make two posts to one channel deadlock. The
// posts take the lock in the mode that lets them share it, so both land. The
// leg runs in a loop, because a deadlock shows up as a race, not as a rule.
//
// The second scenario is the group paths, the two that write msg_channels
// themselves. A lock taken weakly first and upgraded by a later write
// deadlocks two of them against each other, so the leg asks for two group
// seats at once and expects both to land.
//
// This leg is green on the head before the fix as well: it is the guard on the
// fix, not a witness against it. The lock that can deadlock is the one the
// fix adds.
func TestIntegration_TwoPosts_OneChannel_BothLandAndNoDeadlock(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	ch := ux110Ready(t, pool)

	const rounds = 20
	run := fmt.Sprintf("ux110-two-posts-%d", time.Now().UnixNano())
	for round := 0; round < rounds; round++ {
		start := make(chan struct{})
		results := make(chan ux110Posted, 2)
		for i := 0; i < 2; i++ {
			go func(n int) {
				<-start
				m, err := repo.InsertMessage(ctx, ch, uAlexMorgan,
					fmt.Sprintf("%s-%d-%d", run, round, n), "")
				results <- ux110Posted{id: m.ID, err: err}
			}(i)
		}
		close(start)
		for i := 0; i < 2; i++ {
			select {
			case p := <-results:
				if p.err != nil {
					if strings.Contains(p.err.Error(), "deadlock") {
						t.Fatalf("two posts to one channel must not deadlock: %v", p.err)
					}
					t.Fatalf("two posts to one channel must both land, one failed: %v", p.err)
				}
				if p.id == "" {
					t.Fatalf("two posts to one channel must both land, one answered no message")
				}
			case <-time.After(ux110Settle):
				t.Fatal("a post never landed")
			}
		}
	}

	var landed int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM msg_messages WHERE channel_id = $1 AND body LIKE $2`,
		ch, run+"-%").Scan(&landed); err != nil {
		t.Fatalf("count the posts: %v", err)
	}
	if landed != rounds*2 {
		t.Errorf("every post must land, counted %d, expected %d", landed, rounds*2)
	}
}

// UX-110: two group seats, one room.
//
// The two paths that write msg_channels take the channel's lock in the mode a
// write takes, so they do not upgrade it later. Two of them at once must
// both land: a lock taken weakly first and upgraded by a later write would
// deadlock them against each other.
func TestIntegration_TwoGroupSeats_OneRoom_BothLandAndNoDeadlock(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	group, err := repo.CreateGroup(ctx, uAlexMorgan, ux110Body("group"), nil)
	if err != nil {
		t.Fatalf("create the group room: %v", err)
	}
	// The room goes when the leg ends, so its lines stay out of the other
	// legs' feeds.
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM msg_channels WHERE id = $1::uuid`, group.ID); err != nil {
			t.Errorf("drop the leg's group room: %v", err)
		}
	})

	const rounds = 10
	for round := 0; round < rounds; round++ {
		start := make(chan struct{})
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func(n int) {
				<-start
				_, _, err := repo.SeatGroupMember(ctx, group.ID, uSamRivera, "the owner",
					fmt.Sprintf("the guest %d", n))
				results <- err
			}(i)
		}
		close(start)
		for i := 0; i < 2; i++ {
			select {
			case err := <-results:
				if err != nil {
					if strings.Contains(err.Error(), "deadlock") {
						t.Fatalf("two group seats to one room must not deadlock: %v", err)
					}
					t.Fatalf("two group seats to one room must both land, one failed: %v", err)
				}
			case <-time.After(ux110Settle):
				t.Fatal("a group seat never landed")
			}
		}
	}
}

// UX-110: the wait behind a burst.
//
// A measurement, not a pass bar: how long a read waits behind a burst of
// concurrent posts to one channel. The read takes the channel's lock, so it
// waits for the posts that are in flight.
func TestIntegration_MarkRead_WaitsBehindABurstOfPosts(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	ch := ux110Ready(t, pool)

	const burst = 50
	const rounds = 5
	var waits []time.Duration
	for round := 0; round < rounds; round++ {
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < burst; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				<-start
				if _, err := repo.InsertMessage(ctx, ch, uAlexMorgan,
					fmt.Sprintf("ux110-burst-%d-%d-%d", round, n, time.Now().UnixNano()), ""); err != nil {
					t.Errorf("burst post: %v", err)
				}
			}(i)
		}
		close(start)

		began := time.Now()
		if err := repo.MarkRead(ctx, ch, uSamRivera); err != nil {
			t.Fatalf("mark read: %v", err)
		}
		waited := time.Since(began)
		waits = append(waits, waited)
		wg.Wait()
		t.Logf("round %d: a read waited %s behind %d concurrent posts to one channel",
			round, waited.Round(time.Microsecond), burst)
	}

	var longest, shortest time.Duration
	for i, w := range waits {
		if i == 0 || w > longest {
			longest = w
		}
		if i == 0 || w < shortest {
			shortest = w
		}
	}
	t.Logf("a read behind %d concurrent posts: shortest %s, longest %s",
		burst, shortest.Round(time.Microsecond), longest.Round(time.Microsecond))
}

// UX-110: every post path takes the channel's lock.
//
// The order only holds if EVERY path that writes a message takes the channel's
// lock first. A path that does not is a path whose posts a read cannot be
// ordered against. Each leg here holds the channel's lock the way a read
// holds it and asks one path to write into the channel: the path must wait,
// and it must land once the lock is free.
//
// Red on the head before the fix: no path waited for anything.
func TestIntegration_EveryPostPath_TakesTheChannelLock(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	ch := ux110Ready(t, pool)

	// A group room: the two group paths answer only for a room of kind GROUP.
	group, err := repo.CreateGroup(ctx, uAlexMorgan, ux110Body("group"), nil)
	if err != nil {
		t.Fatalf("create the group room: %v", err)
	}
	// The room goes when the leg ends, so its lines stay out of the other
	// legs' feeds.
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM msg_channels WHERE id = $1::uuid`, group.ID); err != nil {
			t.Errorf("drop the leg's group room: %v", err)
		}
	})

	// A root for the reply path.
	root, err := repo.InsertMessage(ctx, ch, uAlexMorgan, ux110Body("root"), "")
	if err != nil {
		t.Fatalf("write the reply's root: %v", err)
	}

	// One key per path: the event paths are idempotent on their key.
	keys := make([]string, 8)
	for i := range keys {
		if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&keys[i]); err != nil {
			t.Fatalf("mint a key: %v", err)
		}
	}

	paths := []struct {
		name    string
		channel string
		run     func(key string) (string, error)
	}{
		{
			name:    "InsertMessage",
			channel: ch,
			run: func(string) (string, error) {
				m, err := repo.InsertMessage(ctx, ch, uAlexMorgan, ux110Body("path"), "")
				return m.ID, err
			},
		},
		{
			name:    "InsertEventMessage",
			channel: ch,
			run: func(key string) (string, error) {
				m, _, err := repo.InsertEventMessage(ctx, ch, key, ux110Body("path"),
					EventRef{Topic: "ux110"}, false)
				return m.ID, err
			},
		},
		{
			name:    "InsertEventMessageOpts",
			channel: ch,
			run: func(key string) (string, error) {
				m, _, err := repo.InsertEventMessageOpts(ctx, ch, key, ux110Body("path"),
					EventRef{Topic: "ux110"}, EventOpts{})
				return m.ID, err
			},
		},
		{
			name:    "PostApprovalMessage",
			channel: ch,
			run: func(key string) (string, error) {
				m, _, err := repo.PostApprovalMessage(ctx, ch, key, ux110Body("path"),
					ApprovalRef{ApprovalID: key, PolicyType: "ux110", Status: "PENDING"})
				return m.ID, err
			},
		},
		{
			name:    "InsertEventReply",
			channel: ch,
			run: func(key string) (string, error) {
				m, _, err := repo.InsertEventReply(ctx, ch, root.ID, key, ux110Body("path"),
					EventRef{Topic: "ux110"})
				return m.ID, err
			},
		},
		{
			name:    "syncMessageCreate",
			channel: ch,
			run: func(key string) (string, error) {
				id, _, err := repo.syncMessageCreate(ctx, uAlexMorgan, ch, ux110Body("path"), "", key)
				return id, err
			},
		},
		{
			name:    "SeatGroupMember",
			channel: group.ID,
			run: func(string) (string, error) {
				if _, _, err := repo.SeatGroupMember(ctx, group.ID, uSamRivera, "the owner", "the guest"); err != nil {
					return "", err
				}
				// The path writes its own system line, so the line is found by
				// its body.
				return ux110NewestMessageByBody(ctx, pool, group.ID, "the owner added the guest to the group.")
			},
		},
		{
			name:    "RenameGroup",
			channel: group.ID,
			run: func(string) (string, error) {
				name := ux110Body("renamed")
				if _, err := repo.RenameGroup(ctx, group.ID, name, "the owner"); err != nil {
					return "", err
				}
				return ux110NewestMessageByBody(ctx, pool, group.ID, "the owner renamed this group to "+name+".")
			},
		},
	}

	for i, p := range paths {
		p := p
		t.Run(p.name, func(t *testing.T) {
			_, release := ux110HoldLock(ctx, t, pool, p.channel)
			defer release()

			answered := make(chan ux110Posted, 1)
			go func() {
				id, err := p.run(keys[i])
				answered <- ux110Posted{id: id, err: err}
			}()

			// The path must wait for the channel's lock.
			select {
			case answer := <-answered:
				t.Fatalf("the post path must wait for the channel's lock, it answered %v", answer.err)
			case <-time.After(ux110Wait):
			}

			// The database's clock, read while the path waits.
			var heldAt time.Time
			if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&heldAt); err != nil {
				t.Fatalf("read the database's clock: %v", err)
			}

			release()

			var answer ux110Posted
			select {
			case answer = <-answered:
			case <-time.After(ux110Settle):
				t.Fatal("the post path never landed")
			}
			if answer.err != nil {
				t.Fatalf("the post path must land once the lock is free: %v", answer.err)
			}
			if answer.id == "" {
				t.Fatal("the post path answered no message")
			}

			// The stamp is the clock the path holds the lock at, not the clock
			// its transaction began at (the transaction began before the lock
			// was free). A path that stamps its message with the clock its
			// transaction began at is a path whose message can read as read.
			var stamped time.Time
			if err := pool.QueryRow(ctx,
				`SELECT created_at FROM msg_messages WHERE id = $1::uuid`, answer.id).Scan(&stamped); err != nil {
				t.Fatalf("read the message's stamp: %v", err)
			}
			if stamped.Before(heldAt) {
				t.Errorf("the post path must stamp its message with the clock it holds the channel's lock at: stamped %s, the clock while it waited %s",
					stamped.UTC(), heldAt.UTC())
			}
		})
	}
}

// ux110NewestMessageByBody answers the id of the newest message in a channel
// with that body. It is how the two group paths' own messages are found: they
// do not hand the message back.
func ux110NewestMessageByBody(ctx context.Context, pool *pgxpool.Pool, channelID, body string) (string, error) {
	var id string
	err := pool.QueryRow(ctx, `
		SELECT id::text FROM msg_messages
		WHERE channel_id = $1::uuid AND body = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, channelID, body).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}
