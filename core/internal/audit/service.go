package audit

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// Authenticator resolves the acting user from a request (mirrors gable audit.go). identity.Handler
// implements this via its Actor adapter, claims-only, no DB hit.
type Authenticator interface {
	Actor(r *http.Request) (uid, role string)
}

// Recorder is the seam RUN-PAY / A-03 / A-19 / FN / SignatureProvider call. It is the ONLY way a
// sibling writes to audit_log (zero-trust: audit owns the table). Synchronous + masked-enforced.
//
// SCAFFOLD-OWNED SHARED SEAM.
type Recorder interface {
	Record(ctx context.Context, a SensitiveAccess) error
}

// store is the persistence seam the service depends on (satisfied by *Repo). Defining it here lets the
// unit tests inject an in-memory fake, no DB, while production passes the real pgx-backed *Repo.
type store interface {
	Insert(ctx context.Context, e Entry) error
	InsertTx(ctx context.Context, q Execer, e Entry) error
	List(ctx context.Context, f Filter) ([]Entry, error)
	ListByEntity(ctx context.Context, entityType, entityID string) ([]Entry, error)
	ListByEntityFenced(ctx context.Context, entityType, entityID string, branches []string) ([]Entry, error)
	GetByID(ctx context.Context, id string) (*Entry, error)
}

// Service holds the audit business rules + the recording middleware.
type Service struct {
	repo      store
	log       *slog.Logger
	resolvers map[string]EntityBranchResolver
}

// NewService wires a repo.
func NewService(repo *Repo, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{repo: repo, log: log}
}

// EntityBranchResolver resolves one entity's owning branch: the narrow seam the reopen fence needs
// from the entity's owner (the order and quote repositories expose the read; cmd/core adapts them
// here). found is false when no such entity exists, so the reopen read can answer an empty trail
// without an error and without an existence signal.
type EntityBranchResolver interface {
	BranchOf(ctx context.Context, entityID string) (branchID string, found bool, err error)
}

// WithEntityBranchResolvers attaches the owner resolvers the reopen fence resolves through
// (HH-180). Setter injection because the integration is genuinely optional: with no resolver, or
// none for a given entity type, the reopen read fails closed for every scoped caller, it never
// opens.
func (s *Service) WithEntityBranchResolvers(resolvers map[string]EntityBranchResolver) *Service {
	s.resolvers = resolvers
	return s
}

// -- request-id correlation (US-A5) -----------------------------------------
type ctxKey int

const requestIDKey ctxKey = iota

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Record validates the masked-only contract (D-A21-6) then inserts. This is the single privacy
// chokepoint the FN posture (spec §15) depends on: deny-by-default, only allowlisted fields persist,
// and every persisted string value is run through the cleartext detector so a raw identifier/PAN/SIN
// can never reach audit_log even if a caller passes one. Every sensitive row inherits the request id
// off the context so it correlates with the coarse middleware row (US-A5).
func (s *Service) Record(ctx context.Context, a SensitiveAccess) error {
	return s.repo.Insert(ctx, s.buildEntry(ctx, a))
}

// RecordTx is Record written on a caller-supplied transaction: same redaction/allowlist boundary
// (buildEntry), but the row lands on the caller's pgx.Tx so it commits or ROLLS BACK atomically with
// whatever mutation the caller is performing (DIB-175 B2-1, the order-edit trail must be all-or-nothing
// with the line write). Unlike Record it never swallows a nil-pool: a tx is a real handle and a write
// failure MUST propagate so the caller's transaction aborts.
func (s *Service) RecordTx(ctx context.Context, q Execer, a SensitiveAccess) error {
	return s.repo.InsertTx(ctx, q, s.buildEntry(ctx, a))
}

// buildEntry applies the masked-only contract (D-A21-6) and returns the row to persist. This is the
// single privacy chokepoint the FN posture (spec §15) depends on: deny-by-default, only allowlisted
// fields persist, and every persisted string value is run through the cleartext detector so a raw
// identifier/PAN/SIN can never reach audit_log even if a caller passes one. Every sensitive row inherits
// the request id off the context so it correlates with the coarse middleware row (US-A5). Shared by
// Record (pool) and RecordTx (caller tx) so the boundary is identical on both write paths.
func (s *Service) buildEntry(ctx context.Context, a SensitiveAccess) Entry {
	changes := map[string]any{}
	put := func(k, v string) {
		if v == "" {
			return
		}
		clean, redacted := redactCleartext(v)
		if redacted {
			s.log.Warn("audit: redacted cleartext-looking value at record boundary",
				"action", a.Action, "entity_type", a.EntityType, "field", k)
		}
		changes[k] = clean
	}
	// Base allowlisted fields. masked_ref is re-enforced here even though the caller is expected to
	// have masked it ("already masked by the caller; the boundary re-enforces").
	put("purpose", a.Purpose)
	put("masked_ref", a.MaskedRef)
	put("reason", a.Reason)
	// Extra is deny-by-default: only allowlisted keys survive, and each value is still redacted.
	for k, v := range a.Extra {
		if !allowedExtraKeys[k] {
			s.log.Warn("audit: dropped non-allowlisted sensitive field",
				"action", a.Action, "entity_type", a.EntityType, "field", k)
			continue
		}
		switch val := v.(type) {
		case string:
			put(k, val)
		case nil:
			// skip
		default:
			// Non-string allowlisted values (ints, bools, e.g. a status code or amount band) pass
			// through unchanged, they cannot carry a cleartext identifier string.
			changes[k] = v
		}
	}
	return Entry{
		Action:     a.Action,
		EntityType: a.EntityType,
		EntityID:   a.EntityID,
		ActorID:    a.ActorID,
		BranchID:   a.BranchID,
		Changes:    changes,
		RequestID:  requestIDFromContext(ctx),
	}
}

func (s *Service) List(ctx context.Context, f Filter) ([]Entry, error) { return s.repo.List(ctx, f) }

// Get fetches one row for the admin deep viewer; returns (nil, nil) when absent.
func (s *Service) Get(ctx context.Context, id string) (*Entry, error) { return s.repo.GetByID(ctx, id) }

func (s *Service) ReopenTransaction(ctx context.Context, entityType, entityID string) ([]Entry, error) {
	return s.repo.ListByEntity(ctx, entityType, entityID)
}

// ReopenTransactionFor is the operator reopen read fenced to the caller's branch reach (HH-180): a
// global caller keeps the full trail. Every other authority is held to the ENTITY first and the rows
// second. The entity gate resolves the entity's owning branch through its owner's resolver: the
// rows' own branch_id is not enough, because most semantic writers (order and quote edits, order
// status and creation, the till payment allocation) write branch-less rows, so a row-only fence
// would hand every scoped caller every branch's price-edit trail. An entity whose type carries no
// resolver, or that does not exist, or whose branch the caller cannot reach answers an EMPTY trail,
// never a refusal: this surface has no not-found shape of its own (an unknown entity id already
// answers 200 with no rows), so an unreachable one answers the same and the read is no existence
// oracle. Only when the entity gate passes does the read run, and it still carries the row fence
// (branches the grants reach plus branch-less rows; the coarse middleware rows carry no branch), so
// the gate and the predicate defend together. The in-process seam ReopenTransaction above stays
// unfenced on purpose: its callers (order history, edit history and activity, quote edit history,
// till payment history, dispatch, billing and POS policy history, the POS return approval, the
// customer-quote acceptance load) gate on the owning entity before they ask, each in its owner's
// way; only the route reached the rows with nobody asking.
func (s *Service) ReopenTransactionFor(ctx context.Context, caller identity.StaffAuthority, entityType, entityID string) ([]Entry, error) {
	if caller.Scope == identity.ScopeGlobal {
		return s.repo.ListByEntity(ctx, entityType, entityID)
	}
	if resolve, ok := s.resolvers[entityType]; ok {
		branchID, found, err := resolve.BranchOf(ctx, entityID)
		if err != nil {
			return nil, err
		}
		if !found || !caller.CanAccessBranch(branchID) {
			return []Entry{}, nil
		}
		return s.repo.ListByEntityFenced(ctx, entityType, entityID, caller.Branches)
	}
	// No resolver for this entity type: fail closed, a scoped caller is answered an empty trail.
	return []Entry{}, nil
}

// -- middleware --------------------------------------------------------------

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the wrapped ResponseWriter so http.NewResponseController can walk the chain to the
// real writer's Flusher/Deadline methods. Without this, a handler that needs to flush (the SSE stream,
// main.go:471) sees only statusRecorder, which implements neither http.Flusher nor Unwrap, and its
// flush silently fails on EVERY request through the audit middleware (prod included). This one method is
// a strict correctness fix to shipped code: it is kept under any feature rollback.
func (sr *statusRecorder) Unwrap() http.ResponseWriter { return sr.ResponseWriter }

var mutating = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// selfRecordedMutations names the mutating routes that write their own
// trail rows through the Recorder seam and take no coarse row. The coarse
// row is per call whatever the status, so on a pre-auth route it would let
// a stranger's refused burst write a row per call: exactly the defect the
// route's own bounded record exists to prevent. A route joins this table
// when its module owns a finer record than the coarse row, by review, and
// the route's own row is then the whole record for it.
var selfRecordedMutations = map[string]bool{
	// The demo session mint writes DEMO_SESSION_MINTED through the
	// Recorder, before the token, and the per-address limit bounds it.
	"/api/v1/demo/session": true,
}

// Middleware records every mutating /api/... request (except the routes
// carved out below) AFTER it completes. Recording is
// best-effort + async so it never adds latency to, or fails, the request (US-A2). It also reads/echoes/
// threads/persists X-Request-Id so the coarse row correlates with the request's Recorder rows (US-A5).
//
// Two carve outs, each a table: /api/v1/auth/login, whose attempts carry
// credentials no trail needs, and selfRecordedMutations, whose routes
// write their own finer rows and must not also land a coarse row per call.
//
// The persisted path never carries a public route's credential: elidePublicToken (below) scrubs the
// token segment of the public token route families to the fixed "{token}" placeholder before the row
// is built, so the operator trail keeps the route shape (family plus action leg) without the
// credential whose secrecy is those routes' whole authorization model.
func (s *Service) Middleware(authn Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := orNewUUID(r.Header.Get("X-Request-Id"))
			w.Header().Set("X-Request-Id", reqID)
			r = r.WithContext(withRequestID(r.Context(), reqID))
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			if mutating[r.Method] && strings.HasPrefix(r.URL.Path, "/api/") &&
				r.URL.Path != "/api/v1/auth/login" && !selfRecordedMutations[r.URL.Path] {
				uid, role := authn.Actor(r)
				e := Entry{
					Action:     httpAction(r.Method),
					EntityType: deriveEntityType(r.URL.Path),
					EntityID:   trailingUUID(r.URL.Path),
					ActorID:    uid,
					RequestID:  reqID,
					Changes: map[string]any{
						"method":     r.Method,
						"path":       elidePublicToken(r.URL.Path),
						"status":     rec.status,
						"actor_role": role,
					},
				}
				go func() { _ = s.repo.Insert(context.Background(), e) }()
			}
		})
	}
}

// -- pure helpers ------------------------------------------------------------

// publicTokenPrefixes names the served route families whose path segment right after the prefix is
// the credential itself (the share or will-call token; ADR-0022 pins the customer-quote one's
// hash-only-at-rest rule). The coarse middleware row must never persist that segment, so
// elidePublicToken replaces it with the route template's own placeholder spelling. The table is
// exhaustive over the tree's mutating /api/public surface by construction: a new public token route
// joins the table or its credential persists, and the serve-shaped regression over the audit wrap
// (core/cmd/core/cq_auditwrap_test.go) fails on the leak.
var publicTokenPrefixes = []string{
	"/api/public/v1/customer-quotes/", // the customer-quote public accept pair (ADR-0022)
	"/api/public/v1/quotes/",          // the dealer share-link accept pair (the carried R4 surface)
	"/api/public/v1/will-call/",       // the dispatch will-call confirm pair (DIB-79)
}

// publicActionSuffixes names the action legs a public token route carries after the token segment.
// Anything else between the family prefix and the end of the path is elided together with the token:
// an unknown suffix collapses into the placeholder rather than persisting any part of a credential,
// so a token carrying an encoded slash cannot smuggle a fragment past the elision.
var publicActionSuffixes = []string{"/accept", "/confirm"}

// elidePublicToken returns path with the credential segment of a known public token route family
// replaced by the fixed "{token}" placeholder, keeping the family and the action leg visible for
// operators. Paths outside the families return unchanged (an ordinary /api path carries ids, not
// credentials, and its raw shape is the trail's value).
func elidePublicToken(path string) string {
	for _, prefix := range publicTokenPrefixes {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := path[len(prefix):]
		for _, suffix := range publicActionSuffixes {
			if strings.HasSuffix(rest, suffix) {
				return prefix + "{token}" + suffix
			}
		}
		return prefix + "{token}"
	}
	return path
}

func httpAction(method string) string {
	switch method {
	case http.MethodPost:
		return ActHTTPPost
	case http.MethodPut:
		return ActHTTPPut
	case http.MethodPatch:
		return ActHTTPPatch
	case http.MethodDelete:
		return ActHTTPDelete
	default:
		return "HTTP." + method
	}
}

// deriveEntityType maps "/api/v1/pos/sales" -> "pos.sale" (drops the version + id segments, singularizes).
func deriveEntityType(path string) string {
	p := strings.TrimPrefix(path, "/api/")
	p = strings.TrimPrefix(p, "v1/")
	segs := strings.Split(strings.Trim(p, "/"), "/")
	var parts []string
	for _, s := range segs {
		if s == "" || looksLikeUUID(s) {
			continue
		}
		parts = append(parts, singular(s))
		if len(parts) == 2 {
			break
		}
	}
	if len(parts) == 0 {
		// Safe fallback: entity_type is NOT NULL in 0013, so an unmappable path must never yield "".
		return "unknown"
	}
	return strings.Join(parts, ".")
}

// singular drops a trailing plural "s" ("sales"->"sale", "widgets"->"widget") while leaving short
// abbreviations ("pos"), "ss"-words ("address"), and non-s words ("staff","role") intact.
func singular(s string) string {
	if len(s) > 3 && strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") {
		return s[:len(s)-1]
	}
	return s
}

// trailingUUID returns the last path segment when it is a UUID, else "".
func trailingUUID(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) == 0 {
		return ""
	}
	last := segs[len(segs)-1]
	if looksLikeUUID(last) {
		return last
	}
	return ""
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// orNewUUID returns the given id or a freshly minted v4 UUID when empty.
func orNewUUID(id string) string {
	if id != "" {
		return id
	}
	return newUUID()
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// -- masking boundary (D-A21-6) ----------------------------------------------

// allowedExtraKeys is the deny-by-default allowlist for SensitiveAccess.Extra. Anything not named
// here is dropped before persistence, a caller cannot smuggle a cleartext field into changes by
// inventing a new key. These are all coarse-grained, already-masked, or code-shaped fields.
var allowedExtraKeys = map[string]bool{
	"tax_reason_code": true, // e.g. ON_FN_POS_8 (US-C1), a code, not an identifier
	"ref_type":        true, // signature/evidence ref kind, not the bytes (US-C2)
	"variance_grain":  true, // masked till-variance magnitude band (US-C3)
	"policy":          true, // override policy name (US-C3)
	"subject_ref":     true, // the masked subject the override applied to (US-C3)
	"approver_id":     true, // four-eyes approver staff id
	"outcome":         true, // assessment outcome (0%/5%/13%/review)
	"change_no":       true, // UNION-W5 (A park): '#CO-nnn' change-order number, well under the redaction threshold
	"delta_cents":     true, // UNION-W5 (A park): change-order money delta, same class as amount_cents
	"method":          true, // payment/HTTP method
	"card_type":       true, // VISA/MC brand (never a PAN)
	"amount_band":     true, // coarse amount band, never the cents
	"status":          true, // result/status code
	"amount_cents":    true, // integer money fact on a payment record (Q-044; an int, never an identifier)
	"invoice_id":      true, // the invoice a payment slice was applied to (uuid; Q-044)
	"payment_no":      true, // human payment number "PMT-000042" (6-digit run, under the redaction threshold)
	// The pos RETURN_COMPLETED completion record (returns.go): the committed
	// return's own number and total, the two facts a counter trail answers
	// "which return, how much" with.
	"return_no":   true, // the return's number, '#RET-' plus a return_no_seq value (repository_w7.go), a sequence count whose digit run stays under the redaction threshold like payment_no
	"total_cents": true, // the committed total in cents, an int money fact like amount_cents: non-string Extra values pass the chokepoint unchanged, so the digits never reach the string redactor
	// The pos SALE_COMPLETED completion record (service.go): the order facts as IDS AND REFERENCES
	// only. A person's name or an address never rides the trail (salesperson_name and job_address
	// stay dropped here; a name is read from its id when shown). po_job_ref and job_reference are
	// free text references: a value with a run of seven or more digits redacts to [redacted] like
	// any string. All three stay admin only, NOT in operatorSafeKeys.
	"po_job_ref":     true, // the PO or job reference typed at the counter (free text, at most 64 characters)
	"salesperson_id": true, // the credited salesperson's staff id (uuid)
	"job_reference":  true, // the job reference typed at the counter (free text, at most 64 characters)
	// The pos TAX_RELIEF_ASSESS record: the declared use and fulfilment vocabulary codes (personal,
	// commercial, pickup, dibbits_truck and the like), codes, never identifiers. Admin only.
	"declared_use":    true, // the purchaser's declared use code
	"fulfilment":      true, // the fulfilment code
	"source_order_id": true, // CP-04 reorder provenance (uuid; admin-only, NOT in operatorSafeKeys)
	"lines":           true, // CP-04 reorder line count (an int, never an identifier)
	// DIB-159 order-status history: from→to status codes on an ORDER_STATUS_CHANGED row.
	// These are CHECK-constrained status codes (DRAFT..CANCELLED), codes, never identifiers,
	// and free of any 7-digit run so they pass redactCleartext cleanly. The transition's optional
	// reason rides in the top-level SensitiveAccess.Reason field (persisted as changes["reason"]),
	// NOT a separate Extra key, so there is no "reason_code" entry here.
	"old_status": true, // the status the order transitioned FROM ("" at first CONFIRMED)
	"new_status": true, // the status the order transitioned TO
	// DIB-175 staff order-edit change history: ONE ORDER_CHANGED row per changed column, shaped
	// {field, line_no, before, after}. `field` is drawn from a fixed vocabulary of column names
	// (order/edit.go) and `line_no` is an int, neither can carry an identifier.
	//
	// before/after carry MONEY as int64: a non-string allowlisted value passes through unchanged
	// (see Record above), so cents are never at risk of redaction. Quantities ride as DECIMAL
	// strings, which order/edit.go bounds below 1,000,000 for exactly this reason, a 7-digit
	// quantity would trip redactCleartext's 7-consecutive-digit rule and silently land in the
	// trail as "[redacted]", destroying the before→after reconstruction the history read depends on.
	// The ROLE that made a change, as a role code. It is coarse by construction, one of fourteen
	// enum values, and it is already deemed safe enough to show a FRONTLINE operator on the reopen
	// path (operatorSafeKeys below), so persisting it cannot leak anything the read side would not
	// hand over anyway. It was missing here only because until now nothing wrote it through Extra:
	// the coarse HTTP trail sets it on its own path (see the middleware), and no service had ever
	// needed to attribute a specific change. The dispatch settings history does, "a dispatch
	// manager changed this" answers the question a settings screen is asked, without putting a user
	// identifier on a screen.
	"actor_role": true,
	"field":      true, // the order/order_lines column that changed
	"line_no":    true, // which line it changed on (an int; absent/0 for an order-header field)
	"before":     true, // the pre-image value  (int64 for cents, string for qty/date)
	"after":      true, // the post-image value (same shape as before)
	// The customer-quote acceptance artifact (HH-3.6, ADR 0022): the signed
	// acceptance's audit row joins the allowlist openly at the integration
	// re-point, per the seam contract's own pin (core/internal/customerquote/
	// audit.go: the first six Extra keys join the deny-by-default allowlist
	// openly). ref_type and amount_cents were already allowlisted. The three
	// hex-valued keys ride separator-encoded (groups of six hex characters
	// joined by colons) so no stored value can carry the seven-digit run the
	// chokepoint redacts on; the encoding is the seam's contract, the
	// decoding the module's. alg and key_id are code-shaped values bounded
	// by the module's own key-id shape rule (no seven-digit run).
	"artifact_hash": true, // separator-encoded sha256 of the acceptance artifact
	"artifact_sig":  true, // separator-encoded HMAC of the acceptance artifact
	"alg":           true, // the artifact signature algorithm constant
	"key_id":        true, // the acceptance signing key id (shape-bounded)
	"token_hash":    true, // separator-encoded sha256 of the share token
	// The offline sync per-operation rows (context 4.5): scope and op are
	// enum vocabulary (the SyncScope enum and the create/update/delete
	// set), conflict_policy the SyncConflict.policy enum, and client_id the
	// client-generated idempotency key, a uuid, code-shaped by the
	// contract's own format pin. None can carry a cleartext identifier.
	"scope":           true, // the sync scope name (SyncScope enum)
	"op":              true, // create, update or delete
	"client_id":       true, // the operation's idempotency key (uuid)
	"conflict_policy": true, // the conflict policy that resolved a stale write
	// The sync caller's platform subject, recorded when no linked users.id exists (a tenant
	// caller, or a staff caller with no linked users row): audit_log.actor_id is a uuid column a text subject cannot ride. It is the
	// platform JWT subject (an identity id, not PII) and stays out of operatorSafeKeys.
	"actor_subject": true,
}

// redactCleartext returns the value with cleartext-looking identifiers stripped. A value is treated as
// a raw identifier if it contains a run of 7+ consecutive digits (PAN 13-19, SIN 9, registry numbers
// ~10), a masked ref like "•••• 4321" (4 trailing digits) and a reason code like "ON_FN_POS_8" both
// pass. The second return reports whether anything was redacted.
func redactCleartext(s string) (string, bool) {
	// A UUID is a safe internal identifier (staff/order/approver id), never PII, allow it through even
	// though its all-numeric groups can look like a digit run.
	if looksLikeUUID(s) {
		return s, false
	}
	if !looksLikeCleartextID(s) {
		return s, false
	}
	return "[redacted]", true
}

func looksLikeCleartextID(s string) bool {
	const threshold = 7
	run := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			run++
			if run >= threshold {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

// IsSensitiveAction reports whether an action follows the masked-only contract and is badged in the
// viewer (exported for the frontend/badging + tests).
func IsSensitiveAction(action string) bool { return sensitiveActions[action] }

// -- operator reopen masking projection (US-B1/US-B2) ------------------------

// operatorSafeKeys are the only changes fields a frontline operator may see on the reopen path.
// Admin-only deep fields, masked_ref (FN identifier), reason (override reason), raw payloads, are
// projected OUT here, server-side, so the client never receives them.
var operatorSafeKeys = map[string]bool{
	"method":          true,
	"path":            true,
	"status":          true,
	"actor_role":      true,
	"purpose":         true,
	"tax_reason_code": true, // the relief reason code IS shown (US-B2); the identifier is not
	"outcome":         true,
	"ref_type":        true,
	"amount_cents":    true, // Q-044 payment trail: staff may see the applied amount ...
	"invoice_id":      true, // ... the invoice it went to ...
	"payment_no":      true, // ... and the payment number (no PII in any of these)
	// DIB-159 order-status history is a counter-level timeline, the operator sees the
	// from→to codes + reason (no PII in a status code). The reason itself is carried by the
	// top-level "reason" changes key (admin-only, projected OUT here), so no "reason_code" entry.
	"old_status": true,
	"new_status": true,
	// DIB-175 order-edit history is a counter-level trail, exactly like the status timeline above:
	// the operator sees WHICH column moved, on which line, and from what to what. No PII is
	// reachable here, `field` is a column name from a fixed vocabulary, `line_no` is an int, and
	// before/after are quantities, dates, or integer cents. Line DESCRIPTION is deliberately not an
	// editable field, so free text never enters this trail.
	"field":   true,
	"line_no": true,
	"before":  true,
	"after":   true,
}

// projectForOperator strips a row down to the operator-safe view: a masked actor label and only the
// operator-safe changes fields. It never mutates the stored row, it returns a copy for the response.
func projectForOperator(e Entry) Entry {
	out := e
	out.ActorID = maskActor(e.ActorID)
	safe := map[string]any{}
	for k, v := range e.Changes {
		if operatorSafeKeys[k] {
			safe[k] = v
		}
	}
	out.Changes = safe
	return out
}

// maskActor renders a non-attributing actor label for the operator view (last 4 of the uuid).
func maskActor(id string) string {
	if id == "" {
		return ""
	}
	if len(id) <= 4 {
		return "•••• " + id
	}
	return "•••• " + id[len(id)-4:]
}
