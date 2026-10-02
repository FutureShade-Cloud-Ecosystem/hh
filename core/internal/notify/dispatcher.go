package notify

import (
	"context"
	"log/slog"
	"time"
)

// Default dispatcher tuning. The interval is short so an invite/reset link reaches its
// (dev) inbox promptly; the batch bounds work per tick; maxAttempts bounds retries before
// a row is dead-lettered so a permanently-failing handler can never loop forever.
const (
	defaultInterval    = 5 * time.Second
	defaultBatch       = 50
	defaultMaxAttempts = 5
)

// Event is one outbox row handed to a handler. Attempts is the count BEFORE this delivery,
// so the dispatcher can decide whether the next failure should dead-letter. Payload is the
// raw JSONB persisted at emit time.
type Event struct {
	EventID  string
	Topic    string
	Payload  []byte
	Attempts int
}

// status is the terminal/continuing disposition of an outbox row after a delivery attempt.
type status string

const (
	statusPublished status = "PUBLISHED" // delivered; row is done
	statusPending   status = "PENDING"   // transient failure; leave PENDING to retry next tick
	statusDead      status = "DEAD"      // exhausted retries (or unknown topic past the window)
)

// Settlement tells the store how to finalize a row inside the same transaction that claimed
// it. Redact replaces the stored payload with '{}' (used for secret-bearing topics so a
// one-time token never lingers at rest past delivery). LastError annotates a failure.
type Settlement struct {
	Status    status
	Redact    bool
	LastError string
}

// OutboxStore is the DB boundary. ProcessBatch claims up to n PENDING rows, one short
// transaction per row, FOR UPDATE SKIP LOCKED so concurrent dispatchers never double-send ,
// and for each claimed row calls settle (which performs the delivery and returns its
// disposition), then applies that Settlement in the SAME transaction before committing.
// It returns the number of rows processed. A fake implementation drives the unit tests with
// no database.
type OutboxStore interface {
	ProcessBatch(ctx context.Context, n int, settle func(context.Context, Event) Settlement) (int, error)
}

// Handler maps a claimed event to a delivery. Returning nil publishes the row; returning an
// error retries it (and eventually dead-letters). A handler must be safe to retry.
type Handler func(ctx context.Context, e Event) error

// Dispatcher drains the events outbox on a ticker and routes each event to its registered
// handler. Routing + retry/dead-letter policy live here; the per-row transaction and the
// SKIP LOCKED claim live in the OutboxStore. Domain handlers (and the Notifier they close
// over) are registered by the owning module at wire time, so the dispatcher stays domain-free.
type Dispatcher struct {
	store       OutboxStore
	log         *slog.Logger
	handlers    map[string]Handler
	observers   map[string][]Handler // secondary, non-owning side-effects per topic (see RegisterObserver)
	secret      map[string]bool      // topics whose payload is redacted on publish (carry a token)
	interval    time.Duration
	batch       int
	maxAttempts int
}

// NewDispatcher builds a dispatcher over the given store.
func NewDispatcher(store OutboxStore, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		store:       store,
		log:         log,
		handlers:    map[string]Handler{},
		observers:   map[string][]Handler{},
		secret:      map[string]bool{},
		interval:    defaultInterval,
		batch:       defaultBatch,
		maxAttempts: defaultMaxAttempts,
	}
}

// WithInterval overrides the poll interval (tests use a tiny value; <=0 is ignored).
func (d *Dispatcher) WithInterval(v time.Duration) *Dispatcher {
	if v > 0 {
		d.interval = v
	}
	return d
}

// Register wires a handler for a topic whose payload is non-secret (preserved on publish for
// audit, e.g. crm.NewPortalContact). Single-handler-per-topic is now an ENFORCED invariant: a
// second registration for the same topic PANICS at boot (before W6 doubles the number of posting
// registrants, a silent last-writer-wins clobber would route a topic's events to the wrong module
// , that must fail loudly at wire time, never in production).
func (d *Dispatcher) Register(topic string, h Handler) { d.set(topic, h) }

// RegisterSecret wires a handler for a topic whose payload carries a one-time token (invite,
// password reset). On successful delivery the stored payload is redacted to '{}' so the token
// does not persist past the brief PENDING window in which it had to travel to the sender.
func (d *Dispatcher) RegisterSecret(topic string, h Handler) {
	d.set(topic, h)
	d.secret[topic] = true
}

// set installs a handler, panicking if the topic is already registered (the boot-safe
// single-handler-per-topic invariant shared by Register + RegisterSecret).
func (d *Dispatcher) set(topic string, h Handler) {
	if _, dup := d.handlers[topic]; dup {
		panic("notify: duplicate handler registration for topic " + topic)
	}
	d.handlers[topic] = h
}

// RegisterObserver attaches a SECONDARY, non-owning handler to a topic that is already owned by a
// primary Register handler. Observers exist so a module can react to another module's events WITHOUT
// contending for topic ownership (the single-handler-per-topic invariant is about which module
// SETTLES the row; a topic may have many observers). It is the sanctioned "thin relay" seam: the
// messaging module observes the raw domain topics (payment.received, order.fulfilled, …) to post a
// system EVENT message, while the GL posting consumer remains the sole owner that decides the row's
// PUBLISHED/PENDING/DEAD disposition.
//
// Observer semantics (deliberately different from a primary handler):
//   - Observers run AFTER the primary handler succeeds, never for a row that failed its owner.
//   - An observer error is logged and swallowed: it does NOT fail the row, does NOT retry, and can
//     NEVER dead-letter a financial event. (A messaging-post blip must not block GL posting or
//     re-deliver a payment.) At-least-once redelivery of the OWNER on a later transient owner-failure
//     is made safe by the observer's own idempotency (messaging keys on event_key).
//   - Observing a topic that has no primary owner is a boot-time panic: an observer with nothing to
//     observe is a wiring bug (the row would never be claimed, so the observer would never fire).
//
// Many observers per topic are allowed; they run in registration order.
func (d *Dispatcher) RegisterObserver(topic string, h Handler) {
	d.observers[topic] = append(d.observers[topic], h)
}

// validateObservers is called once at Run start: every observed topic MUST have a primary owner,
// otherwise the observer can never fire (the owner is what claims + settles the row).
func (d *Dispatcher) validateObservers() {
	for topic := range d.observers {
		if _, owned := d.handlers[topic]; !owned {
			panic("notify: observer registered for topic with no primary handler: " + topic)
		}
	}
}

// Run drains immediately, then every interval, until ctx is cancelled. Safe to call in a
// goroutine; it owns no state beyond the ticker.
func (d *Dispatcher) Run(ctx context.Context) {
	d.validateObservers() // fail fast: an observer with no primary owner would never fire
	if d.log != nil {
		d.log.Info("notify: outbox dispatcher started", "interval", d.interval.String())
	}
	d.DrainOnce(ctx)
	t := time.NewTicker(d.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if d.log != nil {
				d.log.Info("notify: outbox dispatcher stopped")
			}
			return
		case <-t.C:
			d.DrainOnce(ctx)
		}
	}
}

// DrainOnce processes one batch and returns the number of rows handled. Exposed for tests
// and for a one-shot drain. Errors from the store are logged, not propagated, the next tick
// retries.
func (d *Dispatcher) DrainOnce(ctx context.Context) int {
	n, err := d.store.ProcessBatch(ctx, d.batch, d.settle)
	if err != nil && d.log != nil {
		d.log.Error("notify: outbox drain failed", "err", err)
	}
	return n
}

// settle delivers one event and decides its disposition. Unknown topics are treated as a
// (retryable) failure rather than silently dropped, so a handler added in a later deploy can
// still pick the event up, and only dead-letter once retries are exhausted, like any failure.
func (d *Dispatcher) settle(ctx context.Context, e Event) Settlement {
	h, ok := d.handlers[e.Topic]
	if !ok {
		return d.fail(e, "no handler registered for topic")
	}
	if err := h(ctx, e); err != nil {
		return d.fail(e, err.Error())
	}
	// Secondary observers run only after the owner succeeded; their errors are swallowed so a
	// non-owning side-effect (e.g. a messaging EVENT post) can never fail/retry/dead-letter the row.
	// A panicking observer is recovered PER OBSERVER: the owner has already succeeded, so an observer
	// crash must neither change the returned Settlement nor unwind through settle→ProcessBatch and kill
	// the dispatcher goroutine (which would halt ALL outbox draining, payments, GL posting). One bad
	// observer must not stop the others, so the recover is scoped to a single observer invocation.
	for _, obs := range d.observers[e.Topic] {
		d.runObserver(ctx, e, obs)
	}
	if d.log != nil {
		d.log.Debug("notify: published", "topic", e.Topic, "event_id", e.EventID)
	}
	return Settlement{Status: statusPublished, Redact: d.secret[e.Topic]}
}

// runObserver invokes one observer with its own panic barrier. The owner has already settled the
// row PUBLISHED before any observer runs, so both an error (returned) and a panic (recovered here)
// are swallowed: neither can fail/retry/dead-letter the financial row, and a panic is contained to
// this one observer rather than unwinding through settle→ProcessBatch and killing the dispatcher
// goroutine. The recovered value is logged at error level (a panic is louder than a plain observer
// error) with the same topic/event_id fields as the rest of this file.
func (d *Dispatcher) runObserver(ctx context.Context, e Event, obs Handler) {
	defer func() {
		if r := recover(); r != nil && d.log != nil {
			d.log.Error("notify: observer panicked (recovered; row still published)",
				"topic", e.Topic, "event_id", e.EventID, "panic", r)
		}
	}()
	if err := obs(ctx, e); err != nil && d.log != nil {
		d.log.Warn("notify: observer failed (row still published)",
			"topic", e.Topic, "event_id", e.EventID, "err", err)
	}
}

// fail bumps toward the dead-letter threshold: a transient failure stays PENDING for the next
// tick; once this attempt would reach maxAttempts the row is dead-lettered with the last error.
func (d *Dispatcher) fail(e Event, msg string) Settlement {
	if e.Attempts+1 >= d.maxAttempts {
		if d.log != nil {
			d.log.Warn("notify: dead-lettered after max attempts",
				"topic", e.Topic, "event_id", e.EventID, "attempts", e.Attempts+1, "err", msg)
		}
		// A dead-lettered SECRET topic redacts like a published one (HH-188): the row is terminal,
		// and a one-time token must not outlive its delivery in a dead letter any more than in a
		// delivered row.
		return Settlement{Status: statusDead, LastError: msg, Redact: d.secret[e.Topic]}
	}
	if d.log != nil {
		d.log.Warn("notify: delivery failed, will retry",
			"topic", e.Topic, "event_id", e.EventID, "attempts", e.Attempts+1, "err", msg)
	}
	return Settlement{Status: statusPending, LastError: msg}
}
