package main

// order_events.go, the composition root's half of the change order outbox event (order/change_signapply.go).
//
// The order module announces an applied change on the events outbox through a consumer declared seam;
// this file is the adapter that writes the row, and the owner registration the worker runs so the
// dispatcher never dead letters the topic.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
)

// orderChangeEvents satisfies order.ChangeEventPublisher over the events outbox: one row on the apply's own
// transaction, so the event commits with the change or not at all.
type orderChangeEvents struct{}

func (orderChangeEvents) EnqueueTx(ctx context.Context, tx pgx.Tx, topic string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("order change event: marshal: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO events_outbox (topic, payload) VALUES ($1, $2)`, topic, b); err != nil {
		return fmt.Errorf("order change event: enqueue: %w", err)
	}
	return nil
}

// registerOrderEventOwners registers the order module as the owner of the topics it writes to the events
// outbox. The worker calls it before any observer registers on those topics.
func registerOrderEventOwners(d *notify.Dispatcher, log *slog.Logger) {
	order.RegisterChangeEventOwner(d, log)
}
