package order

// change_events.go, the worker's side of the change-applied outbox topic.
//
// The events outbox dispatcher fails any row whose topic has no registered handler (notify/dispatcher.go:
// "no handler registered for topic", retried and then dead lettered). An applied change writes a row on
// ChangeAppliedTopic in its own transaction, so the topic needs an owner here: a handler that settles the
// row. Modules that react to an applied change (the rep's notice, Yard Pick, dispatch) attach as observers
// with RegisterObserver, after this owner, and never contend for the topic.

import (
	"context"
	"log/slog"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// changeEventOwner is the subset of *notify.Dispatcher the owner registers on.
type changeEventOwner interface {
	Register(topic string, h notify.Handler)
}

// RegisterChangeEventOwner makes the order module the owner of ChangeAppliedTopic. The handler settles the
// row after checking the payload decodes; a malformed payload is logged and settled too, because nothing a
// retry could do would repair it.
func RegisterChangeEventOwner(d changeEventOwner, log *slog.Logger) {
	d.Register(ChangeAppliedTopic, func(_ context.Context, e notify.Event) error {
		if _, err := ChangeAppliedPayload(e.Payload); err != nil && log != nil {
			log.Warn("order: change applied event with a payload that does not decode", "event_id", e.EventID, "err", err)
		}
		return nil
	})
}
