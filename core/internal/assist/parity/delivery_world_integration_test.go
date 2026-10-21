//go:build integration

package parity

// The delivery world the slice 7b tests plant for themselves: two branches of their own, a customer, trucks and
// drivers, runs of every status, and stops of every state, the sensitive columns filled on purpose (coordinates, an
// instruction carrying a gate code, the gate code, a signature reference) so a test can prove none of them reaches
// a view, a catalogue or a log.

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

const (
	gateCodeSecret    = "GATE-7731-SECRET"
	signatureSecret   = "sig-ref-secret-0042"
	instructionSecret = "Back lane, gate code GATE-7731-SECRET, dog loose"
)

type deliveryWorld struct {
	a, b     string
	customer string
	tag      string
	trucks   map[string]string
	drivers  map[string]string
	orders   map[string]string
	runs     map[string]string
	stops    map[string]string
}

func plantDeliveryWorld(t *testing.T, env *testsupport.Env) *deliveryWorld {
	t.Helper()
	ctx := testsupport.Ctx(t)
	tag := randomHex(t, 3)
	w := &deliveryWorld{tag: tag, trucks: map[string]string{}, drivers: map[string]string{}, orders: map[string]string{}, runs: map[string]string{}, stops: map[string]string{}}
	w.a = plantBranch(t, env, tag+"DA", "S7b delivery A")
	w.b = plantBranch(t, env, tag+"DB", "S7b delivery B")
	if err := env.Admin.QueryRow(ctx, `INSERT INTO customers (account_no, name) VALUES ($1, $2) RETURNING id::text`, "S7B-"+tag+"D", "Planted Delivery Customer "+tag).Scan(&w.customer); err != nil {
		t.Fatal(err)
	}
	for name, br := range map[string]string{"a1": w.a, "b1": w.b} {
		var id string
		if err := env.Admin.QueryRow(ctx, `INSERT INTO dispatch_trucks (branch_id, code, name) VALUES ($1::uuid, $2, 'planted') RETURNING id::text`, br, "TRK-"+tag+name).Scan(&id); err != nil {
			t.Fatalf("plant truck: %v", err)
		}
		w.trucks[name] = id
		if err := env.Admin.QueryRow(ctx, `INSERT INTO dispatch_drivers (branch_id, name, phone) VALUES ($1::uuid, $2, '555-0100') RETURNING id::text`, br, "Driver "+name+" "+tag).Scan(&id); err != nil {
			t.Fatalf("plant driver: %v", err)
		}
		w.drivers[name] = id
	}
	run := func(name, br, truck, driver, status string, dayOffset int) {
		var id string
		if err := env.Admin.QueryRow(ctx, `INSERT INTO delivery_runs (run_no, branch_id, scheduled_date, truck_id, driver_id, status, dispatched_at, completed_at, notes)
			VALUES ($1, $2::uuid, CURRENT_DATE + $3::int, NULLIF($4,'')::uuid, NULLIF($5,'')::uuid, $6,
			        CASE WHEN $6 IN ('DISPATCHED','COMPLETED') THEN now() END, CASE WHEN $6 = 'COMPLETED' THEN now() END, 'run note secret')
			RETURNING id::text`, fmt.Sprintf("#RUN-S7B-%s-%s", tag, name), br, dayOffset, truck, driver, status).Scan(&id); err != nil {
			t.Fatalf("plant run %s: %v", name, err)
		}
		w.runs[name] = id
	}
	run("a-out", w.a, w.trucks["a1"], w.drivers["a1"], "DISPATCHED", 0)
	run("a-plan", w.a, "", "", "PLANNED", 1)
	run("b-done", w.b, w.trucks["b1"], w.drivers["b1"], "COMPLETED", -1)

	stop := func(name, br, runName, status string, stopNo int, extra string) {
		var oid string
		if err := env.Admin.QueryRow(ctx, `INSERT INTO orders (order_no, customer_id, branch_id, status, total_cents) VALUES ($1, $2::uuid, $3::uuid, 'CONFIRMED', 1000) RETURNING id::text`,
			fmt.Sprintf("SO-S7B-%s-%s", tag, name), w.customer, br).Scan(&oid); err != nil {
			t.Fatalf("plant order: %v", err)
		}
		w.orders[name] = oid
		var runID any
		if runName != "" {
			runID = w.runs[runName]
		}
		var id string
		q := `INSERT INTO delivery_stops (branch_id, order_id, run_id, stop_no, scheduled_date, status, time_window, contact_name, contact_phone, address_line, city, postal_code,
			latitude, longitude, instructions, gate_code, pod_signature_ref, pod_received_by, pod_at, failure_reason, delivered_at)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, CURRENT_DATE, $5, $6, $7, $8, $9, 'Planted City', 'K0K 0K0', 44.1234, -77.5678, $10, $11,
			        CASE WHEN $5 = 'DELIVERED' THEN $12 ELSE '' END, 'Receiver '||$7, CASE WHEN $5 = 'DELIVERED' THEN now() END,
			        CASE WHEN $5 = 'FAILED' THEN 'nobody home, left a card' ELSE '' END, CASE WHEN $5 = 'DELIVERED' THEN now() END) RETURNING id::text`
		if err := env.Admin.QueryRow(ctx, q, br, oid, runID, stopNo, status, "AM "+extra, "Contact "+name+" "+tag, "555-01"+fmt.Sprintf("%02d", stopNo),
			"1 Planted Street "+name, instructionSecret, gateCodeSecret, signatureSecret).Scan(&id); err != nil {
			t.Fatalf("plant stop %s: %v", name, err)
		}
		w.stops[name] = id
	}
	stop("a1", w.a, "a-out", "DELIVERED", 1, "early")
	stop("a2", w.a, "a-out", "FAILED", 2, "late")
	stop("a3", w.a, "a-out", "EN_ROUTE", 3, "")
	stop("a-un", w.a, "", "READY", 0, "unassigned")
	stop("b1", w.b, "b-done", "DELIVERED", 1, "")
	stop("b-un", w.b, "", "READY", 0, "")

	dsn := env.AdminURL
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `DELETE FROM delivery_stops WHERE order_id IN (SELECT id FROM orders WHERE order_no LIKE $1)`, "SO-S7B-"+tag+"-%")
		_, _ = c.Exec(context.Background(), `DELETE FROM delivery_runs WHERE run_no LIKE $1`, "#RUN-S7B-"+tag+"-%")
		_, _ = c.Exec(context.Background(), `DELETE FROM orders WHERE order_no LIKE $1`, "SO-S7B-"+tag+"-%")
		_, _ = c.Exec(context.Background(), `DELETE FROM dispatch_trucks WHERE code LIKE $1`, "TRK-"+tag+"%")
		_, _ = c.Exec(context.Background(), `DELETE FROM dispatch_drivers WHERE name LIKE $1`, "Driver % "+tag)
		_, _ = c.Exec(context.Background(), `DELETE FROM customers WHERE account_no = $1`, "S7B-"+tag+"D")
	})
	return w
}

func (w *deliveryWorld) branches() []string { return []string{w.a, w.b} }
