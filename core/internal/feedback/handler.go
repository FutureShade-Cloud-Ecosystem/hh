package feedback

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// maxBodyBytes bounds a JSON submission body (message + context).
const maxBodyBytes = 16 << 10

// maxMultipartBytes bounds a multipart (with-image) submission: the 5 MB image
// cap plus headroom for the text fields + multipart framing (~5.25 MB total).
// http.MaxBytesReader refuses oversized uploads before they are fully buffered.
const maxMultipartBytes = (5 << 20) + (256 << 10)

// Handler exposes POST /api/v1/feedback.
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
}

func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes wires the feedback route behind the staff-auth guard, matching
// the sibling-module pattern (guards injected, never re-implemented here).
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/feedback", g.RequireStaff(h.staff(h.create)))
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	caller, _ := identity.StaffContextFromContext(r.Context())

	var sub Submission
	// A submission arrives either as JSON (text only, back-compat) or, when the
	// tester attaches a screenshot, as multipart/form-data carrying the image.
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		// Rate-limit BEFORE buffering the (up to ~5 MB) multipart body, so a
		// flooding caller is turned away without streaming a large upload. This is
		// a read-only peek; svc.File's allow() stays the sole authority that records
		// a submission, so a permitted request is never double-counted.
		if h.svc.atLimit(caller.UserID) {
			httpx.Error(w, http.StatusTooManyRequests, ErrRateLimited.Error())
			return
		}
		if err := readMultipart(w, r, &sub); err != nil {
			httpx.Error(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err := dec.Decode(&sub); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_json")
			return
		}
	}
	sub.ActorID = caller.UserID
	sub.ActorRole = caller.RoleCode

	id, err := h.svc.File(r.Context(), sub)
	var planeErr PlaneError
	switch {
	case err == nil:
		httpx.WriteJSON(w, http.StatusCreated, map[string]string{"issue_id": id})
	case errors.Is(err, ErrRateLimited):
		httpx.Error(w, http.StatusTooManyRequests, err.Error())
	case errors.As(err, &planeErr):
		h.log.Error("feedback: plane upstream failed", "err", err)
		httpx.Error(w, http.StatusBadGateway, "feedback could not be filed, the board is unreachable; try again shortly")
	default:
		httpx.Error(w, http.StatusBadRequest, err.Error())
	}
}

// readMultipart parses a multipart/form-data feedback submission: the text
// fields into sub, plus the optional "image" part read into memory. The body is
// capped by http.MaxBytesReader so an oversized upload is refused before it is
// fully buffered. A missing image part is fine (image is optional). Image type
// and size are re-validated server-side in the service before any upload.
func readMultipart(w http.ResponseWriter, r *http.Request, sub *Submission) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxMultipartBytes)
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		return errors.New("invalid_multipart")
	}
	sub.Kind = Kind(r.FormValue("type"))
	sub.Message = r.FormValue("message")
	sub.Screen = r.FormValue("screen")
	sub.URL = r.FormValue("url")
	sub.UserAgent = r.FormValue("user_agent")

	// The JSON path caps the whole body (message + context) at maxBodyBytes via
	// MaxBytesReader; multipart is otherwise bounded only by the ~5.25 MB image
	// budget, which would let a ~5 MB `message` through. Apply the SAME free-text
	// budget to the combined text fields here and reject with the same 400 the
	// oversize JSON path returns, before the image is read into memory.
	if len(sub.Message)+len(sub.Screen)+len(sub.URL)+len(sub.UserAgent) > maxBodyBytes {
		return errors.New("feedback_text_too_large")
	}

	file, hdr, err := r.FormFile("image")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil // no screenshot attached, the feedback still files
		}
		return errors.New("invalid_image")
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return errors.New("invalid_image")
	}
	sub.Image = data
	sub.ImageName = hdr.Filename
	sub.ImageType = hdr.Header.Get("Content-Type")
	return nil
}
