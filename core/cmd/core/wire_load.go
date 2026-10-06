// wire_load.go is the load doors' wiring (HH-LOAD-1 LD-1, ADR-0024): the
// registration site the load module's routes land through, and the audit
// seam the frame's one-audit-row-per-batch ruling writes through.
//
// THE SWITCH. One switch exists, the composition root's: CORE_LOAD_DOORS
// (values on and off, off by default), read once in loadServeConfig
// beside the machine credential it requires (CORE_MACHINE_AUDIENCE and
// CORE_MACHINE_JWKS_URL with it). This file registers nothing of the
// load module until the configuration switched the doors on; the round 1
// HH_LOAD_DOORS_ENABLED name is retired.
//
// THE GATE. The routes are machine surface: the census words machine, and
// the registrar this wiring registers through carries the machine gate
// exactly where the configuration switched the doors on, so each
// registration lands behind its gate (the census's operation id against
// the machine principal's own door list). The registrar refuses the boot
// on a machine worded registration when no gate rides it, so the doors
// cannot serve ungated anywhere: no wrapper, no pending state, nothing
// left from the round 1 interim.
package main

import (
	"context"
	"log/slog"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// wireLoad registers the load doors when and only when the composition
// root switched them on: the routes land through the gate carrying
// registrar, the machine gate wraps each one, and the handler's org and
// actor come from the scope the gate stamps. The frame's two read
// operations register beside the four submit doors of LD-4 (the branch
// map, the user map, the categories and the suppliers): every door over
// the same store, each owning module's writer or read taken by
// constructor, all of them off until the switch is on.
func wireLoad(r httpx.Registrar, pool *pgxpool.Pool, log *slog.Logger, doors bool, auditSvc *audit.Service) {
	if !doors {
		return
	}
	store := load.NewStore(pool)
	h := load.NewHandler(store, log)
	h.RegisterRoutes(r)
	submits := load.NewDoorsHandler(log, map[string]*load.Door{
		"PUT /api/v1/load/branches": newLoadMapDoor(store, load.KindBranch,
			&load.BranchMapWriter{Branches: branches.BranchCodeReader{}}, auditSvc),
		"PUT /api/v1/load/users": newLoadMapDoor(store, load.KindUser,
			&load.UserMapWriter{Staff: identity.StaffEmailReader{}}, auditSvc),
		"PUT /api/v1/load/categories": newLoadDoor(store, load.KindCategory,
			product.CategoryLoadWriter{}, auditSvc),
		"PUT /api/v1/load/suppliers": newLoadDoor(store, load.KindSupplier,
			purchasing.VendorLoadWriter{}, auditSvc),
	})
	submits.RegisterRoutes(r)
	log.Info("load: the load doors serve behind the machine gate")
}

// newLoadDoor builds one door of the load frame with everything the
// wiring owes it: the owning module's writer and the audit seam, so a
// batch cannot land unaudited. The door wiring sites from LD-4 onward
// call this and nothing else of the frame's construction.
func newLoadDoor(store *load.Store, kind string, w load.Writer, auditSvc *audit.Service) *load.Door {
	return load.NewDoor(store, kind, w, loadAuditSeam{svc: auditSvc})
}

// newLoadMapDoor is newLoadDoor for the map doors: the branch map and
// the user map answer the create arm with the attached word, the
// outcome of a name binding to a row that already exists.
func newLoadMapDoor(store *load.Store, kind string, w load.Writer, auditSvc *audit.Service) *load.Door {
	return load.NewAttachDoor(store, kind, w, loadAuditSeam{svc: auditSvc})
}

// loadAuditSeam satisfies load.BatchAuditSink over the carried audit
// service: one audit_log row per batch, the machine principal as actor,
// the counts by outcome, written on the batch record's own transaction
// through the audit module's deny-by-default boundary. Keys and fields
// only: no record value reaches the row.
type loadAuditSeam struct {
	svc *audit.Service
}

// RecordBatchTx writes the batch's audit row on the caller's transaction
// through audit.Service.RecordTx, so the row commits or rolls back with
// the batch record it describes.
func (s loadAuditSeam) RecordBatchTx(ctx context.Context, tx pgx.Tx, row load.BatchAuditRow) error {
	return s.svc.RecordTx(ctx, tx, audit.SensitiveAccess{
		Action:     load.AuditAction,
		EntityType: load.AuditEntityType,
		EntityID:   row.BatchUUID,
		ActorID:    row.ActorID,
		Purpose:    load.AuditPurpose,
		Extra: map[string]any{
			"load_kind":      row.Kind,
			"load_batch_id":  row.BatchID,
			"load_source":    row.SourceSystem,
			"load_created":   row.Counts[load.OutcomeCreated],
			"load_attached":  row.Counts[load.OutcomeAttached],
			"load_updated":   row.Counts[load.OutcomeUpdated],
			"load_unchanged": row.Counts[load.OutcomeUnchanged],
			"load_refused":   row.Counts[load.OutcomeRefused],
		},
	})
}
