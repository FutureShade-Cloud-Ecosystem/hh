package main

// sync_adapters.go carries the sync core's composition adapters: the one
// audit seam that maps the engine's per-operation entries onto the audit
// module's Recorder (HH-3.5 #165). The engine's own shapes stay in
// core/internal/sync; this file is the only place the two meet, the same
// discipline the customer-quote audit re-point established.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// syncAuditSeam satisfies sync.AuditRecorder over the carried audit
// service. The actor resolves through the staff linker exactly as the
// coarse middleware row's actor does (the linked ERP users.id, empty when
// no row links), and the entry's legs ride the allowlisted Extra keys the
// audit service's sync block pins (scope, op, client_id,
// conflict_policy): code-shaped values only, nothing a client typed
// free-hand.
type syncAuditSeam struct {
	svc  audit.Recorder
	link identity.UserIDLinker
}

// RecordSync maps one engine entry onto one audit row.
func (a syncAuditSeam) RecordSync(ctx context.Context, e sync.AuditEntry) error {
	actor := ""
	if a.link != nil && e.Actor != "" {
		if uid, ok := a.link(ctx, e.Actor); ok {
			actor = uid
		}
	}
	extra := map[string]any{
		"scope":     e.Scope,
		"op":        e.Op,
		"client_id": e.ClientID,
		"status":    e.Outcome,
	}
	if e.ConflictPolicy != "" {
		extra["conflict_policy"] = e.ConflictPolicy
	}
	return a.svc.Record(ctx, audit.SensitiveAccess{
		Action:     e.Action,
		EntityType: "sync." + e.Scope,
		EntityID:   e.EntityID,
		ActorID:    actor,
		Extra:      extra,
	})
}
