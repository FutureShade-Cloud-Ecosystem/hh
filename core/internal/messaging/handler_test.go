package messaging

import (
	"context"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// chUUID / rootUUID are well-formed UUIDs used by the handler-level tests. The handler now validates
// {id}/{rootID} as UUIDs at the boundary (mirroring the cursor $4::uuid guard), so the handler tests
// must use real UUIDs where the request is expected to reach the service, the service-level chGen
// ("ch-general") stays a readable non-UUID because the service itself never casts it.
const (
	chUUID   = "a5000000-0000-4000-9000-000000000003"
	rootUUID = "b5000000-0000-4000-9000-000000000001"
)

// cursorFakeStore is a store double whose ListMessages runs the REAL decodeCursor on the caller's
// cursor, so the handler test exercises the full chain: decode → errBadCursor wrap → service
// propagation → writeErr HTTP mapping. A valid/empty cursor paginates normally.
type cursorFakeStore struct {
	*fakeStore
}

func (f cursorFakeStore) ListMessages(_ context.Context, _, cursor string, _ int) (MessagePage, error) {
	if cursor != "" {
		if _, _, err := decodeCursor(cursor); err != nil {
			return MessagePage{}, err
		}
	}
	return MessagePage{Messages: []Message{}}, nil
}

// newCursorHandler wires a real Handler over the cursor-aware fake store, with the caller pre-stamped
// as a member of chGen so the request passes the channel-scoping gate and reaches ListMessages.
func newCursorHandler(t *testing.T) (*Handler, *http.Request) {
	t.Helper()
	fs := newFakeStore()
	fs.setMember(chUUID, uAlice)
	svc := &Service{repo: cursorFakeStore{fs}}
	h := NewHandler(svc, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/messaging/channels/"+chUUID+"/messages", nil)
	req.SetPathValue("id", chUUID)
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
	return h, req
}

// A malformed pagination cursor is a CLIENT fault: the handler must answer HTTP 400, not 500.
// Pre-fix (no ErrBadCursor sentinel / no writeErr case) this fell through to the 500 default.
func TestListMessages_BadCursor_Returns400(t *testing.T) {
	for _, bad := range []string{"garbage", "not-a-timestamp|x", "%27%20OR%201=1", "2026-13-99T00:00:00Z|x"} {
		h, req := newCursorHandler(t)
		q := req.URL.Query()
		q.Set("cursor", bad)
		req.URL.RawQuery = q.Encode()

		rr := httptest.NewRecorder()
		h.listMessages(rr, req, guards.StaffCaller{})

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("bad cursor %q: want 400, got %d (body %s)", bad, rr.Code, rr.Body.String())
		}
	}
}

// REGRESSION (MAJOR): a cursor with a VALID timestamp half but a non-UUID id half must return HTTP
// 400 through the full handler chain. Pre-fix the timestamp parsed fine, decodeCursor returned the
// junk id, and it reached the `$4::uuid` bind → Postgres cast error → wrapped "list messages" →
// HTTP 500. Only the timestamp-half cases in TestListMessages_BadCursor_Returns400 above caught the
// old sentinel gap; none exercised the id half. Without the uuid.Parse guard this returns 500.
func TestListMessages_BadUUIDCursorHalf_Returns400(t *testing.T) {
	validTS := time.Date(2026, 7, 13, 10, 30, 0, 123456789, time.UTC).Format(time.RFC3339Nano)
	for _, bad := range []string{
		validTS + "|not-a-uuid",
		validTS + "|8f14e45f-ceea-467d-9a1c",
		validTS + "|' OR 1=1 --",
	} {
		h, req := newCursorHandler(t)
		q := req.URL.Query()
		q.Set("cursor", bad)
		req.URL.RawQuery = q.Encode()

		rr := httptest.NewRecorder()
		h.listMessages(rr, req, guards.StaffCaller{})

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("valid-ts/bad-uuid cursor %q: want 400, got %d (body %s)", bad, rr.Code, rr.Body.String())
		}
	}
}

// - MAJOR: malformed (non-UUID) {id}/{rootID} path params -
// pgCastFakeStore reproduces the real Repository failure mode: every method binds channelID/rootID
// straight into a Postgres `uuid` column, so a non-UUID string fails the implicit text->uuid cast and
// the driver error wraps to the catch-all HTTP 500. This double raises that same generic (non-
// sentinel) error whenever it is handed a non-UUID id, so a request that reaches the store with a
// malformed path param maps to 500, exactly the pre-fix live behaviour. With the handler's boundary
// uuid.Parse guard the store is never reached and the caller gets a 404 instead.
type pgCastFakeStore struct {
	*fakeStore
}

var errPGCast = errors.New("messaging: ERROR: invalid input syntax for type uuid (SQLSTATE 22P02)")

func isUUIDish(s string) bool { _, err := uuid.Parse(s); return err == nil }

func (f pgCastFakeStore) IsMember(ctx context.Context, channelID, userID string) (bool, error) {
	if !isUUIDish(channelID) {
		return false, fmt.Errorf("is-member: %w", errPGCast)
	}
	return f.fakeStore.IsMember(ctx, channelID, userID)
}
func (f pgCastFakeStore) channelExists(ctx context.Context, channelID string) (bool, error) {
	if !isUUIDish(channelID) {
		return false, fmt.Errorf("channel-exists: %w", errPGCast)
	}
	return f.fakeStore.channelExists(ctx, channelID)
}
func (f pgCastFakeStore) ListThread(ctx context.Context, channelID, rootID string) ([]Message, error) {
	if !isUUIDish(channelID) || !isUUIDish(rootID) {
		return nil, fmt.Errorf("list thread: %w", errPGCast)
	}
	return f.fakeStore.ListThread(ctx, channelID, rootID)
}
func (f pgCastFakeStore) InsertMessage(ctx context.Context, channelID, userID, body, threadRootID string) (Message, error) {
	// A non-empty non-UUID threadRootID binds into the thread-root uuid column and fails the cast,
	// exactly like channelID, this reproduces the pre-fix 500 path so the boundary guard is covered.
	if !isUUIDish(channelID) || (threadRootID != "" && !isUUIDish(threadRootID)) {
		return Message{}, fmt.Errorf("insert message: %w", errPGCast)
	}
	return f.fakeStore.InsertMessage(ctx, channelID, userID, body, threadRootID)
}
func (f pgCastFakeStore) ListMessages(ctx context.Context, channelID, cursor string, limit int) (MessagePage, error) {
	if !isUUIDish(channelID) {
		return MessagePage{}, fmt.Errorf("list messages: %w", errPGCast)
	}
	return f.fakeStore.ListMessages(ctx, channelID, cursor, limit)
}
func (f pgCastFakeStore) MarkRead(ctx context.Context, channelID, userID string) error {
	if !isUUIDish(channelID) {
		return fmt.Errorf("mark read: %w", errPGCast)
	}
	return f.fakeStore.MarkRead(ctx, channelID, userID)
}

// newCastHandler wires a Handler over the pg-cast-simulating store and returns a request stamped with
// an authenticated staff caller. The caller is set as a member of chUUID/rootUUID so a WELL-FORMED id
// still reaches the store and reads 200 (proving the guard does not over-reject).
func newCastHandler(t *testing.T) *Handler {
	t.Helper()
	fs := newFakeStore()
	fs.setMember(chUUID, uAlice)
	fs.threads[chUUID+"|"+rootUUID] = []Message{{ID: "r1", Body: "one"}}
	svc := &Service{repo: pgCastFakeStore{fs}}
	return NewHandler(svc, nil)
}

func castReq(method, target, id, rootID string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	if id != "" {
		req.SetPathValue("id", id)
	}
	if rootID != "" {
		req.SetPathValue("rootID", rootID)
	}
	return req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
}

// A malformed (non-UUID) {id} on GET .../messages must be a 4xx client fault, not the catch-all 500.
// Pre-fix the junk id flowed to IsMember → Postgres text->uuid cast error → wrapped → HTTP 500.
func TestListMessages_NonUUIDPath_Returns4xx(t *testing.T) {
	h := newCastHandler(t)
	for _, bad := range []string{"not-a-uuid", "xx", "%27%20OR%201=1", "123"} {
		rr := httptest.NewRecorder()
		h.listMessages(rr, castReq(http.MethodGet, "/api/v1/messaging/channels/"+bad+"/messages", bad, ""), guards.StaffCaller{})
		if rr.Code < 400 || rr.Code >= 500 {
			t.Fatalf("listMessages non-uuid %q: want 4xx, got %d (body %s)", bad, rr.Code, rr.Body.String())
		}
	}
}

// A malformed {id} on POST .../messages must be 4xx, not 500 (pre-fix: InsertMessage cast → 500).
func TestPostMessage_NonUUIDPath_Returns4xx(t *testing.T) {
	h := newCastHandler(t)
	rr := httptest.NewRecorder()
	// A well-formed body so decode succeeds and control reaches the channel-scoping gate, that is
	// where the pre-fix junk id hit InsertMessage's assertMember → IsMember cast → 500. With the
	// boundary guard the malformed id is rejected 404 before any of that.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messaging/channels/not-a-uuid/messages",
		strings.NewReader(`{"body":"hello"}`))
	req.SetPathValue("id", "not-a-uuid")
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
	h.postMessage(rr, req, guards.StaffCaller{})
	if rr.Code < 400 || rr.Code >= 500 {
		t.Fatalf("postMessage non-uuid: want 4xx, got %d (body %s)", rr.Code, rr.Body.String())
	}
}

// REGRESSION (#1): a malformed (non-UUID) thread_root_id in the POST body, on a VALID channel the
// caller is a member of, with a non-empty body, must be a 4xx client fault, not a 500. Pre-fix the
// body field was never UUID-validated, so it flowed through assertMember (which passes) into
// InsertMessage's thread-root uuid bind → cast error → HTTP 500. The boundary guard in postMessage
// maps it to 404 (indistinguishable from an absent/wrong-channel root), before the store is reached.
func TestPostMessage_NonUUIDThreadRoot_Returns4xx(t *testing.T) {
	h := newCastHandler(t)
	for _, bad := range []string{"not-a-uuid", "xx", "' OR 1=1 --", "123"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/messaging/channels/"+chUUID+"/messages",
			strings.NewReader(`{"body":"hi","thread_root_id":"`+bad+`"}`))
		req.SetPathValue("id", chUUID)
		req = req.WithContext(identity.ContextWithStaff(req.Context(),
			identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
		h.postMessage(rr, req, guards.StaffCaller{})
		if rr.Code < 400 || rr.Code >= 500 {
			t.Fatalf("postMessage bad thread_root_id %q: want 4xx, got %d (body %s)", bad, rr.Code, rr.Body.String())
		}
	}
}

// A WELL-FORMED thread_root_id on a valid member channel must still reach the store and create (201),
// proving the body-field guard does not over-reject valid uuids.
func TestPostMessage_ValidThreadRoot_Reaches201(t *testing.T) {
	h := newCastHandler(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messaging/channels/"+chUUID+"/messages",
		strings.NewReader(`{"body":"hi","thread_root_id":"`+rootUUID+`"}`))
	req.SetPathValue("id", chUUID)
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
	h.postMessage(rr, req, guards.StaffCaller{})
	if rr.Code != http.StatusCreated {
		t.Fatalf("postMessage valid thread_root_id: want 201, got %d (body %s)", rr.Code, rr.Body.String())
	}
}

// A malformed {id} OR {rootID} on GET .../threads/... must be 4xx (pre-fix: ListThread cast → 500).
func TestListThread_NonUUIDPath_Returns4xx(t *testing.T) {
	h := newCastHandler(t)
	cases := []struct{ id, root string }{
		{"not-a-uuid", rootUUID}, // bad channel id
		{chUUID, "not-a-uuid"},   // valid channel, bad root id
		{"xx", "yy"},             // both bad
	}
	for _, c := range cases {
		rr := httptest.NewRecorder()
		h.listThread(rr, castReq(http.MethodGet, "/api/v1/messaging/channels/"+c.id+"/threads/"+c.root, c.id, c.root), guards.StaffCaller{})
		if rr.Code < 400 || rr.Code >= 500 {
			t.Fatalf("listThread id=%q root=%q: want 4xx, got %d (body %s)", c.id, c.root, rr.Code, rr.Body.String())
		}
	}
}

// A malformed {id} on POST .../read must be 4xx (pre-fix: MarkRead cast → 500).
func TestMarkRead_NonUUIDPath_Returns4xx(t *testing.T) {
	h := newCastHandler(t)
	rr := httptest.NewRecorder()
	h.markRead(rr, castReq(http.MethodPost, "/api/v1/messaging/channels/not-a-uuid/read", "not-a-uuid", ""), guards.StaffCaller{})
	if rr.Code < 400 || rr.Code >= 500 {
		t.Fatalf("markRead non-uuid: want 4xx, got %d (body %s)", rr.Code, rr.Body.String())
	}
}

// A WELL-FORMED but absent/present id must still behave: a valid id that the caller is a member of
// reads 200 through the same guard, proving the boundary check does not over-reject valid uuids.
func TestListThread_ValidUUIDPath_Reaches200(t *testing.T) {
	h := newCastHandler(t)
	rr := httptest.NewRecorder()
	h.listThread(rr, castReq(http.MethodGet, "/api/v1/messaging/channels/"+chUUID+"/threads/"+rootUUID, chUUID, rootUUID), guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("listThread valid uuid: want 200, got %d (body %s)", rr.Code, rr.Body.String())
	}
}

// A valid cursor must still paginate (HTTP 200), the 400 mapping must not regress the happy path.
func TestListMessages_ValidCursor_Returns200(t *testing.T) {
	h, req := newCursorHandler(t)
	valid := encodeCursor(Message{
		ID:        "8f14e45f-ceea-467d-9a1c-000000000001",
		CreatedAt: time.Date(2026, 7, 13, 10, 30, 0, 123456789, time.UTC),
	})
	q := req.URL.Query()
	q.Set("cursor", valid)
	req.URL.RawQuery = q.Encode()

	rr := httptest.NewRecorder()
	h.listMessages(rr, req, guards.StaffCaller{})

	if rr.Code != http.StatusOK {
		t.Fatalf("valid cursor: want 200, got %d (body %s)", rr.Code, rr.Body.String())
	}
}

// An empty cursor (first page) must also paginate normally.
func TestListMessages_NoCursor_Returns200(t *testing.T) {
	h, req := newCursorHandler(t)
	rr := httptest.NewRecorder()
	h.listMessages(rr, req, guards.StaffCaller{})
	if rr.Code != http.StatusOK {
		t.Fatalf("no cursor: want 200, got %d (body %s)", rr.Code, rr.Body.String())
	}
}
