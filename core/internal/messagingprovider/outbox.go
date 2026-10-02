package messagingprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// outbox.go, the durable queue behind out-of-band delivery: sms_outbox (0097), claimed with exactly
// the semantics notify.PgOutboxStore uses on events_outbox (one short transaction per row, FOR UPDATE
// SKIP LOCKED), so the shipped notify.Dispatcher drives it unchanged.
//
// Why a second table rather than a second handler on the shared one is argued in 0097's header: the
// shared dispatcher claims by status alone and freezes its handler set before it starts running, so a
// new topic there would be claimed by a dispatcher with no handler for it and dead-letter. Keeping SMS
// disjoint also means a carrier outage cannot retry, block or dead-letter a GL journal.

// dueClause is the claim predicate: which sms_outbox rows the drain may take right now.
//
// IT IS A CONSTANT RATHER THAN AN INLINE STRING SO A TEST CAN ASSERT THE SAME PREDICATE THE DRAIN USES.
// The alternative, a test that hand-copies the WHERE clause, passes happily after somebody deletes
// the not_before condition from the real query, which is exactly the regression the send window exists
// to prevent. (A test cannot simply call ProcessBatch here: it claims the oldest due row in the WHOLE
// table, so on a shared integration database it would reach into another package's fixture.)
//
// not_before NULL is every row the Wave 2 producers wrote, and means "as soon as possible".
const dueClause = `status = 'PENDING' AND (not_before IS NULL OR not_before <= now())`

// The dispatcher's disposition values, as wire strings (see the ProcessBatch note). They are also the
// events_outbox / sms_outbox status CHECK values, so they are pinned by the schema.
const (
	dispositionPublished = "PUBLISHED"
	dispositionDead      = "DEAD"
)

// Compile-time proof that this store can drive the shipped dispatcher.
var _ notify.OutboxStore = (*Outbox)(nil)

// Outbox is the sms_outbox reader/writer. A nil pool is tolerated (scaffold/offline mode): enqueues
// are dropped with an error and ProcessBatch is a no-op, so the server still boots.
type Outbox struct {
	pool *pgxpool.Pool
	// policy is the send-window + rate gate (Wave 3, DIB-58/76 bundle). It is consulted at the top of
	// ProcessBatch, so a closed window leaves rows PENDING and untouched rather than claiming them and
	// putting them back, a claimed-and-returned row burns an attempt toward dead-lettering, and "it
	// is 10pm" is not a delivery failure. A nil policy means no gate, which is what every Wave 2 test
	// and every non-drain caller gets.
	policy *SendPolicy
	log    *slog.Logger
}

// NewOutbox builds the store; pass a nil pool in scaffold mode.
func NewOutbox(pool *pgxpool.Pool) *Outbox { return &Outbox{pool: pool} }

// WithSendPolicy attaches the quiet-hours + throttle gate to the drain path.
func (o *Outbox) WithSendPolicy(p *SendPolicy) *Outbox {
	o.policy = p
	return o
}

// WithLogger attaches a logger (used only to say, once per closed tick, why nothing drained).
func (o *Outbox) WithLogger(l *slog.Logger) *Outbox {
	o.log = l
	return o
}

// errNoDB, no database is configured, so nothing can be queued durably.
var errNoDB = errors.New("messagingprovider: database not configured")

// EnqueueTx queues one notice INSIDE a caller-supplied transaction. This is the seam the yard's stage
// write uses (tasks.StageOutbox): the notice and the business fact commit together, so a customer is
// never told their order is ready by a transaction that rolled back.
func (o *Outbox) EnqueueTx(ctx context.Context, tx pgx.Tx, topic string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("messagingprovider: marshal %s: %w", topic, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sms_outbox (topic, payload) VALUES ($1, $2)`, topic, b); err != nil {
		return fmt.Errorf("messagingprovider: enqueue %s: %w", topic, err)
	}
	return nil
}

// Enqueue queues one notice on its own. Used by producers that have no transaction to join (a job
// sweep, a manual send).
func (o *Outbox) Enqueue(ctx context.Context, topic string, payload any) error {
	if o.pool == nil {
		return errNoDB
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("messagingprovider: marshal %s: %w", topic, err)
	}
	if _, err := o.pool.Exec(ctx, `INSERT INTO sms_outbox (topic, payload) VALUES ($1, $2)`, topic, b); err != nil {
		return fmt.Errorf("messagingprovider: enqueue %s: %w", topic, err)
	}
	return nil
}

// EnqueueAtTx queues one notice inside a caller's transaction, held until notBefore (0109). It returns
// the new row's event_id so a cadence ledger can point at the notice it produced, the drill-through
// an operator needs to get from "we say we told them" to "here is the queue row and what happened".
//
// A zero notBefore means "as soon as the window allows", i.e. the same behaviour EnqueueTx has always
// had. A notBefore in the past is stored as given rather than clamped: the row is simply due, and
// rewriting a producer's stated intent would hide a scheduling bug instead of showing it.
func (o *Outbox) EnqueueAtTx(ctx context.Context, tx pgx.Tx, topic string, payload any, notBefore time.Time) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("messagingprovider: marshal %s: %w", topic, err)
	}
	var at any
	if !notBefore.IsZero() {
		at = notBefore.UTC()
	}
	var eventID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO sms_outbox (topic, payload, not_before) VALUES ($1, $2, $3) RETURNING event_id::text`,
		topic, b, at).Scan(&eventID); err != nil {
		return "", fmt.Errorf("messagingprovider: enqueue %s: %w", topic, err)
	}
	return eventID, nil
}

// EnqueueAt is EnqueueAtTx without a caller transaction.
func (o *Outbox) EnqueueAt(ctx context.Context, topic string, payload any, notBefore time.Time) (string, error) {
	if o.pool == nil {
		return "", errNoDB
	}
	tx, err := o.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, err := o.EnqueueAtTx(ctx, tx, topic, payload, notBefore)
	if err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

// ProcessBatch satisfies notify.OutboxStore: claim and settle up to n PENDING rows, one short
// transaction each, FOR UPDATE SKIP LOCKED so concurrent dispatchers divide the work instead of
// double-sending. The settle callback runs INSIDE the claiming transaction, so a row is only marked
// PUBLISHED once its handler actually succeeded.
//
// NOTE ON THE DISPOSITION SWITCH. notify.Settlement.Status is an exported field of an UNEXPORTED
// named string type, so a store outside package notify cannot name its constants. Converting the
// value to a string (legal, conversions do not require naming the type) and switching on the wire
// values it was defined with is the only way to implement this interface from outside, and it is
// stable: those strings are also the values of the events_outbox status CHECK, so they cannot change
// without a migration. The compile-time proof that this type satisfies the interface is the
// var _ notify.OutboxStore assertion below.
//
// THE SEND WINDOW IS ENFORCED HERE, BEFORE THE FIRST CLAIM (Wave 3). Asking the policy once per batch
// rather than once per row is what makes "it is 21:00, send nothing" cost one function call instead of
// one claim transaction per queued notice; and gating the CLAIM rather than the SEND is what keeps a
// held row's attempts counter at zero, so a night's worth of waiting cannot dead-letter anything.
func (o *Outbox) ProcessBatch(ctx context.Context, n int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	if o.pool == nil {
		return 0, nil
	}
	if o.policy != nil {
		allowed, why := o.policy.Allowance(n)
		if allowed <= 0 {
			if o.log != nil {
				o.log.Debug("sms: drain held", "why", why)
			}
			return 0, nil
		}
		n = allowed
	}
	processed := 0
	for i := 0; i < n; i++ {
		ok, err := o.processOne(ctx, settle)
		if err != nil {
			return processed, err
		}
		if !ok {
			break
		}
		processed++
	}
	if o.policy != nil {
		o.policy.Consume(processed)
	}
	return processed, nil
}

func (o *Outbox) processOne(ctx context.Context, settle func(context.Context, notify.Event) notify.Settlement) (bool, error) {
	tx, err := o.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var e notify.Event
	err = tx.QueryRow(ctx, `
		SELECT event_id::text, topic, payload, attempts
		FROM sms_outbox
		WHERE `+dueClause+`
		ORDER BY occurred_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED`).Scan(&e.EventID, &e.Topic, &e.Payload, &e.Attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("messagingprovider: claim: %w", err)
	}

	st := settle(ctx, e)

	switch string(st.Status) {
	case dispositionPublished:
		// A delivered notice's payload carries the recipient's phone number. It has done its job, so
		// it is redacted at rest, the same treatment the invite/reset topics get for their tokens.
		_, err = tx.Exec(ctx, `
			UPDATE sms_outbox
			SET status = 'PUBLISHED', published_at = now(), attempts = attempts + 1, last_error = '',
			    payload = '{}'::jsonb
			WHERE event_id = $1`, e.EventID)
	case dispositionDead:
		// A DEAD notice is as terminal as a published one, and the same sensitive keys leave with
		// it: the share link (a bearer credential for the public quote view and accept routes), the
		// recipient's number and address. The redaction is SELECTIVE, unlike the published row's
		// full clear: a dead row is the operator's only trace of what failed, and several topics'
		// enqueue returns no event id, so clearing the whole payload would leave an anonymous dead
		// letter nobody can trace to an order or a quote (the round 1 review's P2). The keys are
		// removed blindly of topic: every payload type in this module carries its reference under
		// its own name and its secrets under these three, and a topic no handler was ever
		// registered for has no secret marking to consult, yet that unwired path is exactly the
		// one that must never park a token at rest. The topic and last_error stay.
		_, err = tx.Exec(ctx, `
			UPDATE sms_outbox
			SET status = 'DEAD', attempts = attempts + 1, last_error = $2,
			    payload = payload - 'to' - 'share_url' - 'email'
			WHERE event_id = $1`, e.EventID, st.LastError)
	default:
		_, err = tx.Exec(ctx, `
			UPDATE sms_outbox SET attempts = attempts + 1, last_error = $2
			WHERE event_id = $1`, e.EventID, st.LastError)
	}
	if err != nil {
		return false, fmt.Errorf("messagingprovider: settle: %w", err)
	}
	return true, tx.Commit(ctx)
}
