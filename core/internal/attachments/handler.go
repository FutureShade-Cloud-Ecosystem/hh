package attachments

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RoomMembershipChecker verifies whether a user is a member of a room and whether the room exists.
type RoomMembershipChecker interface {
	IsRoomMember(ctx context.Context, roomID, userID string) (bool, error)
	RoomExists(ctx context.Context, roomID string) (bool, error)
}

// RoomAttachmentMirror mirrors a channel attachment link to the messaging module.
type RoomAttachmentMirror interface {
	MirrorAttachment(ctx context.Context, channelID string, att Attachment, role string) error
}

// attachmentStore is the repository interface required by Handler.
type attachmentStore interface {
	CreateAttachment(ctx context.Context, orgID, createdBy string, in AttachmentCreateInput) (Attachment, error)
	GetAttachment(ctx context.Context, orgID, id string) (Attachment, error)
	GetLinks(ctx context.Context, attachmentID string) ([]Link, error)
	CreateLink(ctx context.Context, link Link) (Link, error)
}

// Handler serves D10 attachment HTTP operations.
type Handler struct {
	repo        attachmentStore
	roomChecker RoomMembershipChecker
	roomMirror  RoomAttachmentMirror
	userLink    identity.UserIDLinker
	log         *slog.Logger
}

// NewHandler constructs an attachment HTTP handler.
func NewHandler(repo attachmentStore, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{repo: repo, log: log}
}

// WithRoomChecker attaches the room membership checker.
func (h *Handler) WithRoomChecker(c RoomMembershipChecker) *Handler {
	h.roomChecker = c
	return h
}

// WithRoomMirror attaches the room attachment mirror callback.
func (h *Handler) WithRoomMirror(m RoomAttachmentMirror) *Handler {
	h.roomMirror = m
	return h
}

// WithUserLinker binds the user ID linker for mapping platform users to user UUIDs.
func (h *Handler) WithUserLinker(link identity.UserIDLinker) *Handler {
	h.userLink = link
	return h
}

func (h *Handler) staff(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		next(w, identity.StampRequest(r, caller, h.userLink), caller)
	}
}

// RegisterRoutes registers the D10 attachment operations onto the registrar.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/attachments", g.RequireStaff(h.staff(h.createAttachment)))
	r.Handle("GET /api/v1/attachments/{id}", g.RequireStaff(h.staff(h.getAttachment)))
	r.Handle("POST /api/v1/attachments/{id}/links", g.RequireStaff(h.staff(h.createAttachmentLink)))
}

func (h *Handler) orgID(r *http.Request, caller guards.StaffCaller) string {
	if caller.StaffScope.OrgID != "" {
		return caller.StaffScope.OrgID
	}
	if sc, ok := authz.StaffScopeFrom(r.Context()); ok && sc.OrgID != "" {
		return sc.OrgID
	}
	if v := r.Header.Get("X-Org"); v != "" {
		return v
	}
	return ""
}

func (h *Handler) userID(r *http.Request, caller guards.StaffCaller) string {
	if sc, ok := identity.StaffContextFromContext(r.Context()); ok && sc.UserID != "" {
		return sc.UserID
	}
	if h.userLink != nil && caller.PlatformUserID != "" {
		if uid, ok := h.userLink(r.Context(), caller.PlatformUserID); ok && uid != "" {
			return uid
		}
	}
	if caller.PlatformUserID != "" {
		return caller.PlatformUserID
	}
	return ""
}

type attachmentCreateRequest struct {
	Kind           string `json:"kind"`
	AppwriteFileID string `json:"appwrite_file_id"`
	Bucket         string `json:"bucket"`
	Mime           string `json:"mime"`
	SizeBytes      *int64 `json:"size_bytes"`
	Checksum       string `json:"checksum"`
}

func (h *Handler) createAttachment(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	orgID := h.orgID(r, caller)
	if orgID == "" {
		httpx.Error(w, http.StatusBadRequest, "org context is required")
		return
	}
	var req attachmentCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validKind(req.Kind) {
		httpx.Error(w, http.StatusBadRequest, "invalid attachment kind")
		return
	}
	if strings.TrimSpace(req.AppwriteFileID) == "" {
		httpx.Error(w, http.StatusBadRequest, "appwrite_file_id is required")
		return
	}
	if strings.TrimSpace(req.Bucket) == "" {
		httpx.Error(w, http.StatusBadRequest, "bucket is required")
		return
	}

	userID := h.userID(r, caller)
	in := AttachmentCreateInput{
		Kind:           req.Kind,
		AppwriteFileID: req.AppwriteFileID,
		Bucket:         req.Bucket,
		Mime:           req.Mime,
		SizeBytes:      req.SizeBytes,
		Checksum:       req.Checksum,
	}

	att, err := h.repo.CreateAttachment(r.Context(), orgID, userID, in)
	if err != nil {
		h.log.ErrorContext(r.Context(), "create attachment failed", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "failed to create attachment")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, att)
}

type linkCreateRequest struct {
	AttachmentID string `json:"attachment_id"`
	OwnerKind    string `json:"owner_kind"`
	OwnerID      string `json:"owner_id"`
	Role         string `json:"role"`
}

func (h *Handler) createAttachmentLink(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "attachment not found")
		return
	}
	orgID := h.orgID(r, caller)
	if orgID == "" {
		httpx.Error(w, http.StatusBadRequest, "org context is required")
		return
	}

	att, err := h.repo.GetAttachment(r.Context(), orgID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "attachment not found")
			return
		}
		h.log.ErrorContext(r.Context(), "get attachment for link failed", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "failed to get attachment")
		return
	}

	var req linkCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AttachmentID != "" && req.AttachmentID != id {
		httpx.Error(w, http.StatusBadRequest, "attachment_id mismatch")
		return
	}
	if !validOwnerKind(req.OwnerKind) {
		httpx.Error(w, http.StatusBadRequest, "invalid owner_kind")
		return
	}
	if !looksLikeUUID(req.OwnerID) {
		httpx.Error(w, http.StatusBadRequest, "owner_id must be a uuid")
		return
	}
	if strings.TrimSpace(req.Role) == "" {
		httpx.Error(w, http.StatusBadRequest, "role is required")
		return
	}

	userID := h.userID(r, caller)
	if req.OwnerKind == OwnerChannel {
		if h.roomChecker != nil {
			exists, err := h.roomChecker.RoomExists(r.Context(), req.OwnerID)
			if err != nil {
				h.log.ErrorContext(r.Context(), "room exists check failed", "err", err)
				httpx.Error(w, http.StatusInternalServerError, "failed to check room")
				return
			}
			if !exists {
				httpx.Error(w, http.StatusNotFound, "room not found")
				return
			}
			isMember, err := h.roomChecker.IsRoomMember(r.Context(), req.OwnerID, userID)
			if err != nil {
				h.log.ErrorContext(r.Context(), "room membership check failed", "err", err)
				httpx.Error(w, http.StatusInternalServerError, "failed to check room membership")
				return
			}
			if !isMember {
				httpx.Error(w, http.StatusForbidden, "not a channel member")
				return
			}
		}
	}

	link := Link{
		AttachmentID: id,
		OwnerKind:    req.OwnerKind,
		OwnerID:      req.OwnerID,
		Role:         req.Role,
	}

	created, err := h.repo.CreateLink(r.Context(), link)
	if err != nil {
		h.log.ErrorContext(r.Context(), "create link failed", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "failed to create attachment link")
		return
	}

	if req.OwnerKind == OwnerChannel && h.roomMirror != nil {
		if err := h.roomMirror.MirrorAttachment(r.Context(), req.OwnerID, att, req.Role); err != nil {
			h.log.ErrorContext(r.Context(), "mirror attachment to channel failed", "err", err)
		}
	}

	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *Handler) getAttachment(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "attachment not found")
		return
	}
	orgID := h.orgID(r, caller)
	if orgID == "" {
		httpx.Error(w, http.StatusBadRequest, "org context is required")
		return
	}

	att, err := h.repo.GetAttachment(r.Context(), orgID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "attachment not found")
			return
		}
		h.log.ErrorContext(r.Context(), "get attachment failed", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "failed to get attachment")
		return
	}

	links, err := h.repo.GetLinks(r.Context(), id)
	if err != nil {
		h.log.ErrorContext(r.Context(), "get attachment links failed", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "failed to get attachment links")
		return
	}
	att.Links = links

	// Ruling 1 read fence: if any link is owner_kind channel, caller must be a member of at least one linked room.
	var channelIDs []string
	for _, l := range links {
		if l.OwnerKind == OwnerChannel {
			channelIDs = append(channelIDs, l.OwnerID)
		}
	}
	if len(channelIDs) > 0 && h.roomChecker != nil {
		userID := h.userID(r, caller)
		allowed := false
		for _, chID := range channelIDs {
			isMember, err := h.roomChecker.IsRoomMember(r.Context(), chID, userID)
			if err == nil && isMember {
				allowed = true
				break
			}
		}
		if !allowed {
			httpx.Error(w, http.StatusForbidden, "not a member of linked room")
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, att)
}
