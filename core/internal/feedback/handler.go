package feedback

import (
	"context"
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

// maxMultipartBytes bounds a multipart (with-image) submission: the total
// image bound plus headroom for the text fields and the multipart framing.
// The service bounds the images again (per image and total) after they are
// read. http.MaxBytesReader refuses oversized uploads before they are fully
// buffered.
const maxMultipartBytes = maxTotalImageBytes + (768 << 10)

// ActorNames resolves a linked ERP user id to the display name and role label
// the work item names the submitter by (the row 949 rule: the board carries
// one bridge user, so the item's text is the only place the sender is named,
// and the raw id never reaches it). Nil or a miss degrades to the role alone,
// never to the id, and never refuses the submission.
type ActorNames func(ctx context.Context, userID string) (name, role string, ok bool)

// Handler exposes POST /api/v1/feedback.
type Handler struct {
	userLink   identity.UserIDLinker
	actorNames ActorNames
	svc        *Service
	log        *slog.Logger
}

func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// WithActorNames sets the display name resolver the work item's text carries.
// Nil is legal: the item then names the role alone.
func (h *Handler) WithActorNames(resolve ActorNames) *Handler {
	h.actorNames = resolve
	return h
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
	// tester attaches screenshots, as multipart/form-data carrying the images.
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		// Rate-limit BEFORE buffering the (up to ~15 MB) multipart body, so a
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
	// The submitter's words come from the server's own resolver over the linked
	// row, never from anything the client sent, and never the raw id.
	if h.actorNames != nil && caller.UserID != "" {
		if name, role, ok := h.actorNames(r.Context(), caller.UserID); ok {
			sub.ActorName = name
			sub.ActorRoleName = role
		}
	}

	filed, err := h.svc.File(r.Context(), sub)
	var planeErr PlaneError
	switch {
	case err == nil:
		httpx.WriteJSON(w, http.StatusCreated, map[string]string{
			"issue_id":     filed.ID,
			"issue_number": filed.Number,
		})
	case errors.Is(err, ErrRateLimited):
		httpx.Error(w, http.StatusTooManyRequests, err.Error())
	case errors.As(err, &planeErr):
		h.log.Error("feedback: plane upstream failed", "err", err)
		httpx.Error(w, http.StatusBadGateway, "feedback could not be filed, the board is unreachable or its module is missing; try again shortly")
	default:
		httpx.Error(w, http.StatusBadRequest, err.Error())
	}
}

// readMultipart parses a multipart/form-data feedback submission: the text
// fields into sub, plus the "images" parts read into memory (the legacy single
// "image" part still lands there too, so an in flight client keeps working).
// The body is capped by http.MaxBytesReader so an oversized upload is refused
// before it is fully buffered. A missing image part is fine (images are
// optional). Image types and sizes are re-validated server-side in the
// service before any upload.
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
	// MaxBytesReader; multipart is otherwise bounded only by the image budget,
	// which would let a very large `message` through. Apply the SAME free-text
	// budget to the combined text fields here and reject with the same 400 the
	// oversize JSON path returns, before any image is read into memory.
	if len(sub.Message)+len(sub.Screen)+len(sub.URL)+len(sub.UserAgent) > maxBodyBytes {
		return errors.New("feedback_text_too_large")
	}

	headers := append(
		r.MultipartForm.File["images"],
		r.MultipartForm.File["image"]...,
	)
	for _, hdr := range headers {
		if len(sub.Images) >= maxImagesPerSubmission {
			return errors.New("too_many_images: at most 3 images per submission")
		}
		file, err := hdr.Open()
		if err != nil {
			return errors.New("invalid_image")
		}
		data, err := io.ReadAll(file)
		if err != nil {
			_ = file.Close()
			return errors.New("invalid_image")
		}
		if err := file.Close(); err != nil {
			return errors.New("invalid_image")
		}
		sub.Images = append(sub.Images, Image{Data: data, Name: hdr.Filename})
	}
	return nil
}
