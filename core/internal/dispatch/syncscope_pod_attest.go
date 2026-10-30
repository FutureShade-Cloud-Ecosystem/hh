// syncscope_pod_attest.go is the typed name attestation door of the pod sync scope (Field CP-2): the
// sync twin of POST /api/v1/dispatch/stops/{id}/pod. A driver who took the recipient's typed name and
// affirmation offline replays it as one pod create, {id?, stop_id, received_by, affirmed}, and the
// door gives the SAME effect and the SAME guards as the direct door by running the one body both
// share, Service.CapturePOD: the transition rule from the stop's status, the name and affirmation
// refusals, the signature provider first (so a failed capture leaves the stop en route), the one
// statement that writes the proof and flips the stop to DELIVERED, the audit row, the photo attach
// and scan trail tie, and the order hand off. It runs under the caller's resolved staff context, so
// the branch fence (guardStop) and the audit actor are the REST door's.
//
// Where it is stored: on delivery_stops, where the direct door stores it. The attestation is not a
// delivery_pod_photos row (a photo row needs bytes), so the pod feed stays photo metadata and the
// stops feed carries DELIVERED, pod_received_by and pod_at to the device. The stop is the row the
// create answers with: its id and its stops seq.
//
// Idempotency without a migration: SetPOD stamps the stop's idempotency_key in the same statement
// that delivers it, and the pod FindApplied resolves a key against delivered stops as well as photo
// rows, so a replay answers applied and nothing runs twice.
//
// The driver fence is the scope's own: a driver attests only a stop on a run assigned to them, and a
// colleague's stop is the same as absent.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// PODAttestor is the direct door's service body, the narrow seam the attestation door runs: the
// dispatch Service satisfies it.
type PODAttestor interface {
	CapturePOD(ctx context.Context, stopID string, in PODInput) (StopDetail, error)
}

// WithAttestor wires the attestation door's service body.
func (s *PODSyncStore) WithAttestor(a PODAttestor) *PODSyncStore {
	s.attestor = a
	return s
}

// WithStaffAuthority wires the attestation door's authority seam: the caller's staff context in the
// org, with the linked ERP user on it.
func (s *PODSyncStore) WithStaffAuthority(r identity.SyncStaffResolver) *PODSyncStore {
	s.staff = r
	return s
}

// AttestDoorWired reports whether both seams of the attestation door are wired, the leg the
// composition test and the boot fail fast pin. An unwired door refuses; it never attests unfenced.
func (s *PODSyncStore) AttestDoorWired() bool { return s.attestor != nil && s.staff != nil }

// isAttestation reports whether a create is the typed name form: it declares received_by or
// affirmed. The photo form never does, and the two forms do not mix.
func isAttestation(fields map[string]any) bool {
	_, name := fields["received_by"]
	_, aff := fields["affirmed"]
	return name || aff
}

// attestationFields checks the form's declared set: the stop it proves, the typed name and the
// affirmation, and the outbox's local row id. The time is the server's, never the payload's.
func attestationFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "stop_id", "received_by", "affirmed":
		default:
			return fmt.Errorf("dispatch: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// classifyAttestErr sorts the shared body's errors with the module's own taxonomy: an unknown stop
// is the not found, the module's client faults are the op's refusal in its words, and anything else
// (no signature provider, a database fault) is an operational fault the push fails on.
func classifyAttestErr(err error) error {
	status, msg, ok := ErrorHTTPStatus(err)
	if !ok {
		return err
	}
	switch status {
	case http.StatusNotFound:
		return sync.ErrRowNotFound
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusGone:
		return fmt.Errorf("dispatch: %s: %w", msg, sync.ErrOpRefused)
	}
	return err
}

// attestationCreateShape is the body-only half of the attestation create's
// checks: the form's field set, the stop uuid, the typed name and
// affirmation shapes and the payload id shape. It reads nothing and honours
// no fence (the stop read and the authority read are the door's own), so
// the bound key gate can run it before the hidden refusal (UX-116): an
// invalid body then meets these refusals under a spent key and a free one.
// The refusals are exactly the ones createAttestation answers.
func attestationCreateShape(fields map[string]any) error {
	if err := attestationFields(fields); err != nil {
		return err
	}
	stopID, _ := fields["stop_id"].(string)
	if !looksLikeUUID(stopID) {
		return fmt.Errorf("dispatch: stop_id must be a uuid: %w", sync.ErrOpRefused)
	}
	if _, err := syncCreateID(fields); err != nil {
		return err
	}
	if raw, has := fields["received_by"]; has {
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("dispatch: received_by must be text: %w", sync.ErrOpRefused)
		}
	}
	if raw, has := fields["affirmed"]; has {
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("dispatch: affirmed must be true or false: %w", sync.ErrOpRefused)
		}
	}
	return nil
}

// createAttestation applies one attestation create.
func (s *PODSyncStore) createAttestation(ctx context.Context, f sync.ScopeFilter, d driverFence, key string, fields map[string]any) (sync.RowState, error) {
	if err := attestationFields(fields); err != nil {
		return sync.RowState{}, err
	}
	if !s.AttestDoorWired() {
		return sync.RowState{}, fmt.Errorf("dispatch: the proof of delivery attestation door is not configured: %w", sync.ErrOpRefused)
	}
	stopID, _ := fields["stop_id"].(string)
	if !looksLikeUUID(stopID) {
		return sync.RowState{}, fmt.Errorf("dispatch: stop_id must be a uuid: %w", sync.ErrOpRefused)
	}
	if _, err := syncCreateID(fields); err != nil {
		return sync.RowState{}, err
	}
	var name string
	if raw, has := fields["received_by"]; has {
		v, ok := raw.(string)
		if !ok {
			return sync.RowState{}, fmt.Errorf("dispatch: received_by must be text: %w", sync.ErrOpRefused)
		}
		name = v
	}
	var affirmed bool
	if raw, has := fields["affirmed"]; has {
		v, ok := raw.(bool)
		if !ok {
			return sync.RowState{}, fmt.Errorf("dispatch: affirmed must be true or false: %w", sync.ErrOpRefused)
		}
		affirmed = v
	}
	// The stop must sit in the caller's org and, for a driver, on their own run: absent otherwise.
	arm, args := driverArm(d, []any{stopID, f.OrgID})
	var present int
	err := s.pool.QueryRow(ctx,
		`SELECT 1 FROM delivery_stops WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL`+arm,
		args...).Scan(&present)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: attestation stop read: %w", err)
	}
	sc, has, err := s.staff(ctx, f.Actor, f.OrgID)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: attestation door authority: %w", err)
	}
	if !has || (sc.Status != "" && sc.Status != identity.StatusActive) {
		return sync.RowState{}, fmt.Errorf("dispatch: no active staff authority in this org: %w", sync.ErrOpRefused)
	}
	if _, err := s.attestor.CapturePOD(identity.ContextWithStaff(ctx, sc), stopID,
		PODInput{ReceivedBy: name, Affirmed: affirmed, IdempotencyKey: key}); err != nil {
		return sync.RowState{}, classifyAttestErr(err)
	}
	return s.attestationState(ctx, s.pool, stopID)
}

// driverArm renders the driver arm for a stop query whose own placeholders are $1 and $2 and appends
// the arm's argument only when the arm binds one: the desk binds NULL (the arm short circuits true), a
// linked driver binds their record, and a driver with none gets the arm that matches no row and no
// placeholder at all.
func driverArm(d driverFence, args []any) (string, []any) {
	arm, arg := d.leg(stopDriverCol, len(args)+1)
	if d.driver && d.driverID == "" {
		return arm, args
	}
	return arm, append(args, arg)
}

// attestationQuerier is the read surface a pool and a transaction share.
type attestationQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// attestationState answers the create (and the replay) with the stop the proof sits on.
func (s *PODSyncStore) attestationState(ctx context.Context, db attestationQuerier, stopID string) (sync.RowState, error) {
	var seq int64
	var name string
	if err := db.QueryRow(ctx,
		`SELECT seq, pod_received_by FROM delivery_stops WHERE id = $1::uuid`, stopID).Scan(&seq, &name); err != nil {
		return sync.RowState{}, fmt.Errorf("dispatch: attestation read back: %w", err)
	}
	return sync.RowState{ID: stopID, Seq: seq, Data: map[string]any{
		"stop_id": stopID, "received_by": name, "affirmed": true,
	}}, nil
}

// loadAttestation answers Load for the typed name attestation's row. The
// create answers the stop it proved (its id and its stops seq), so the
// stop is the row Load reads, under the same fence the create's own stop
// read runs (org, not deleted, the driver arm): a colleague's stop is
// the same as absent, and the desk reads its org's stops. The stop must
// carry the attestation (a received name): the proof Load answers is the
// typed name's, so a stop delivered with a photo stays the absent row,
// exactly as the create would refuse it (entry 489). Without this arm
// Load would miss the row for every caller, and the engine's bound key
// gate would take that blindness for a fence.
func (s *PODSyncStore) loadAttestation(ctx context.Context, db attestationQuerier, f sync.ScopeFilter, d driverFence, id string) (sync.RowState, bool, error) {
	if f.Tenant || f.OrgID == "" || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	arm, args := driverArm(d, []any{id, f.OrgID})
	var present int
	err := db.QueryRow(ctx,
		`SELECT 1 FROM delivery_stops WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL
		  AND pod_received_by <> ''`+arm,
		args...).Scan(&present)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("dispatch: pod sync load: %w", err)
	}
	rs, err := s.attestationState(ctx, db, id)
	if err != nil {
		return sync.RowState{}, false, err
	}
	return rs, true, nil
}

// findAttestation resolves a key against delivered stops: the stop whose proof the key applied.
func (s *PODSyncStore) findAttestation(ctx context.Context, f sync.ScopeFilter, d driverFence, key string) (sync.RowState, bool, error) {
	if f.Tenant || f.OrgID == "" {
		return sync.RowState{}, false, nil
	}
	arm, args := driverArm(d, []any{key, f.OrgID})
	var stopID string
	err := s.pool.QueryRow(ctx,
		`SELECT id::text FROM delivery_stops
		  WHERE idempotency_key = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL
		    AND pod_signature_ref <> ''`+arm+` LIMIT 1`,
		args...).Scan(&stopID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("dispatch: attestation find applied: %w", err)
	}
	rs, err := s.attestationState(ctx, s.pool, stopID)
	return rs, err == nil, err
}
