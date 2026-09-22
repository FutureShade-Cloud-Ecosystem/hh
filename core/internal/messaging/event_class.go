package messaging

import (
	"context"
	"errors"
	"time"
)

// - DIB-449: EVENT-CLASS TIERS -
//
// A system event is not one thing. "Payment received" is a routine ledger fact; "credit hold" is an
// alert someone must acknowledge before the shift moves on. Presenting both as a conversation message
// is what made the hub loud enough to skim, and the hub is where the credit holds, SLA breaches and
// forced-ack escalations arrive, so a habit of skimming it is a safety defect, not a taste one.
//
// The class is CONFIG (msg_event_classes, 0152), tech-admin tunable, because "how loud is this event"
// is a visibility knob and the house rule is that admins own visibility knobs (the DIB-434 caveat).
//
//	ROUTINE , a ledger fact. NO per-event message; folded into the day's rollup (rollup.go).
//	NOTABLE , someone should look. One message per event in its home channel. UNCHANGED behaviour.
//	CRITICAL, the DIB-217 forced-ack / #escalations path. UNCHANGED behaviour.
const (
	ClassRoutine  = "ROUTINE"
	ClassNotable  = "NOTABLE"
	ClassCritical = "CRITICAL"
)

// - THE BOUNDARY: config decides PRESENTATION, never ACK -
//
// 0138 gated operator-configurable priority rules explicitly ("the priority set is a code-level
// allowlist … precisely so widening it stays a deliberate, reviewed change rather than a config toggle
// someone flips at 2am"). That gate still stands. So this table is authoritative for ONE question -
// does the event roll up, or post per-event, and requiresAck(topic) remains the sole authority for the
// forced-ack tier.
//
// effectiveClass is where the two meet, and the code wins:
//
//	a topic on the 0071 forced-ack allowlist is CRITICAL, whatever the row says.
//
// This is deliberately the same defence-in-depth shape 0137 used for the un-mutable escalation tier
// (refused at the write boundary AND forced in the read model). A legacy row, a direct-SQL write, or a
// future endpoint that forgets the check still cannot downgrade a live alert into silence.
func effectiveClass(topic string, configured string) string {
	if requiresAck(topic) {
		return ClassCritical
	}
	switch configured {
	case ClassRoutine, ClassNotable, ClassCritical:
		return configured
	default:
		// No row, or a value outside the CHECK. NOTABLE is the safe default: it is today's shipped
		// behaviour (one message per event), so an unclassified topic degrades to being SEEN. Defaulting
		// an unknown event to ROUTINE would silently fold a message nobody decided to fold.
		return ClassNotable
	}
}

// EventClass is one row of the classification config, as the admin surface reads and writes it.
type EventClass struct {
	Topic     string    `json:"event_topic"`
	Class     string    `json:"class"`
	Locked    bool      `json:"locked"`
	Label     string    `json:"label,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
}

// ErrEventClassLocked is an attempt to reclassify a LOCKED (CRITICAL) topic. It maps to HTTP 409 -
// the same shape as DIB-269's un-mutable refusal, and for the same reason: the caller is permitted to
// change classifications, but not THIS one, and a refusal that says so is the only honest answer. A
// silently-ignored write would leave an admin believing they had turned an alert down when they had not.
var ErrEventClassLocked = errors.New("messaging: this event class is locked (critical alerts cannot be reclassified)")

// ErrBadEventClass is a class outside the CHECK set (client fault → 400), caught at the service
// boundary so it never reaches Postgres as a constraint violation → 500.
var ErrBadEventClass = errors.New("messaging: unknown event class")

// normalizeEventClass validates a class against the CHECK set. ok is false for anything else.
func normalizeEventClass(class string) (string, bool) {
	switch class {
	case ClassRoutine, ClassNotable, ClassCritical:
		return class, true
	default:
		return "", false
	}
}

// classStore is the DIB-449 config persistence seam, discovered by type assertion off the base store
// exactly like priorityStore, so the shipped `store` interface, and every fake implementing it, stay
// unchanged.
type classStore interface {
	ListEventClasses(ctx context.Context) ([]EventClass, error)
	EventClassFor(ctx context.Context, topic string) (string, error)
	SetEventClass(ctx context.Context, topic, class, byUserID string) (EventClass, error)
}

func (s *Service) classes() (classStore, error) {
	if cs, ok := s.repo.(classStore); ok {
		return cs, nil
	}
	return nil, errNoDB
}

// ClassOf resolves the EFFECTIVE class of a topic: the configured row run through effectiveClass, so
// the forced-ack allowlist always wins. A store that cannot answer (no DB, in a unit test with the base
// fake) degrades to the code-only answer rather than failing the event, an event that cannot be
// classified must still be DELIVERED, and NOTABLE is the shipped per-event behaviour.
func (s *Service) ClassOf(ctx context.Context, topic string) string {
	cs, err := s.classes()
	if err != nil {
		return effectiveClass(topic, "")
	}
	configured, err := cs.EventClassFor(ctx, topic)
	if err != nil {
		return effectiveClass(topic, "")
	}
	return effectiveClass(topic, configured)
}

// ListEventClasses returns the classification config for the admin surface, with each row's class
// already run through effectiveClass, the surface shows what the SYSTEM will do, not what the table
// happens to say. A row that disagrees with the allowlist is displayed as CRITICAL and locked, which
// is the truth.
func (s *Service) ListEventClasses(ctx context.Context) ([]EventClass, error) {
	cs, err := s.classes()
	if err != nil {
		return nil, err
	}
	rows, err := cs.ListEventClasses(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Class = effectiveClass(rows[i].Topic, rows[i].Class)
		if requiresAck(rows[i].Topic) {
			rows[i].Locked = true
		}
	}
	return rows, nil
}

// SetEventClass reclassifies one topic. The route is admin-gated (the same guard as the HR/audit
// export); this is the RULE layer on top of that gate:
//
//   - a LOCKED row is refused (409). Locked means CRITICAL, and the escalation path is not a knob.
//   - a topic on the forced-ack allowlist is refused even if its row somehow says otherwise, so the
//     refusal cannot be routed around by first corrupting the table.
//   - an unknown class is a 400 at the boundary, never a CHECK violation downstream.
func (s *Service) SetEventClass(ctx context.Context, byUserID, topic, class string) (EventClass, error) {
	cs, err := s.classes()
	if err != nil {
		return EventClass{}, err
	}
	norm, ok := normalizeEventClass(class)
	if !ok {
		return EventClass{}, ErrBadEventClass
	}
	if requiresAck(topic) {
		return EventClass{}, ErrEventClassLocked
	}
	rows, err := cs.ListEventClasses(ctx)
	if err != nil {
		return EventClass{}, err
	}
	found := false
	for _, r := range rows {
		if r.Topic != topic {
			continue
		}
		found = true
		if r.Locked {
			return EventClass{}, ErrEventClassLocked
		}
	}
	if !found {
		return EventClass{}, errNotFound
	}
	out, err := cs.SetEventClass(ctx, topic, norm, byUserID)
	if err != nil {
		return EventClass{}, err
	}
	out.Class = effectiveClass(out.Topic, out.Class)
	return out, nil
}
