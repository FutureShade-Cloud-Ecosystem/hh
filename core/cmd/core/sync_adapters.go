package main

// sync_adapters.go carries the sync core's composition adapters: the one
// audit seam that maps the engine's per-operation entries onto the audit
// module's Recorder (HH-3.5 #165). The engine's own shapes stay in
// core/internal/sync; this file is the only place the two meet, the same
// discipline the customer-quote audit re-point established.

import (
	"context"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// syncAuditSeam satisfies sync.AuditRecorder over the carried audit
// service. The actor resolves through the staff linker exactly as the
// coarse middleware row's actor does (the linked ERP users.id); when no
// row links, the platform subject rides the allowlisted actor_subject key
// instead. The entry's other legs ride the Extra keys the audit service's
// sync block pins (scope, op, client_id, conflict_policy): code-shaped
// values only, nothing a client typed free-hand.
type syncAuditSeam struct {
	svc  audit.Recorder
	link identity.UserIDLinker
}

// subjectGroupLen is the group length of the subject encoding: six
// characters, so the longest digit run inside one group is six, one below
// the audit chokepoint's redaction threshold of seven (the house
// precedent on the same allowlist, customerquote's separator encoding,
// ADR 0022).
const subjectGroupLen = 6

// separatorEncodeSubject writes a platform subject as groups of six
// characters joined by colons, so no run of seven digits can occur and the
// chokepoint stores the subject whole instead of "[redacted]". It is
// deterministic and lossless for any character set a subject can carry
// (a colon is outside the id alphabet): stripping the colons returns the
// subject.
func separatorEncodeSubject(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); i += subjectGroupLen {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(string(rs[i:min(i+subjectGroupLen, len(rs))]))
	}
	return b.String()
}

// RecordSync maps one engine entry onto one audit row.
func (a syncAuditSeam) RecordSync(ctx context.Context, e sync.AuditEntry) error {
	actor, subject := "", ""
	if a.link != nil && e.Actor != "" {
		if uid, ok := a.link(ctx, e.Actor); ok {
			actor = uid
		}
	}
	if actor == "" {
		// No linked users.id (a tenant caller): the uuid actor column stays
		// empty and the platform subject rides the allowlisted actor_subject
		// key, so the trail still names who pushed.
		subject = separatorEncodeSubject(e.Actor)
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
	if subject != "" {
		extra["actor_subject"] = subject
	}
	return a.svc.Record(ctx, audit.SensitiveAccess{
		Action:     e.Action,
		EntityType: "sync." + e.Scope,
		EntityID:   e.EntityID,
		ActorID:    actor,
		Extra:      extra,
	})
}
