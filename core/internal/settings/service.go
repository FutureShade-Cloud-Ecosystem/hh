package settings

// service.go, the kit's resolution + write logic over the 0172 substrate.
//
//   SurfaceConfig  the one read a surface opens on: the caller's resolved profile, the dealer's
//                  role items, the caller's user items (empty when no override), the resolved
//                  workspace row, and the module's policy knobs via the surface hook (the module
//                  gates its own security-bearing fields there, this service never sees them).
//   PutRoleItems   dealer curation. Tier-1 GLOBAL admins only, gated IN THE SERVICE
//                  (caller.IsAdmin(), the quote acceptcontrols precedent: the route guard is the
//                  coarse first line; the caller is actually resolved here).
//   PutUserItems / ResetUserItems      the caller's whole-or-absent override; reset = DELETE.
//   Workspace / SaveWorkspace / ResetWorkspace  the per-user ergonomics row: whole-or-absent,
//                  write_key idempotent replay (the 0166 contract), reset = DELETE.

import (
	"context"
	"fmt"
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// Store is the kit's persistence seam (satisfied by *Store; faked in tests).
type StoreIface interface {
	ProfileFor(ctx context.Context, roleCode string) (string, error)
	ReadRoleItems(ctx context.Context, surfaceID, profile string) ([]Item, error)
	ReplaceRoleItems(ctx context.Context, surfaceID, profile string, items []Item, updatedBy string) error
	ReadUserItems(ctx context.Context, userID, surfaceID string) ([]Item, error)
	ReplaceUserItems(ctx context.Context, userID, surfaceID string, items []Item) error
	DeleteUserItems(ctx context.Context, userID, surfaceID string) error
	ReadUserWorkspace(ctx context.Context, userID, surfaceID string) (WorkspaceRow, bool, error)
	UpsertUserWorkspace(ctx context.Context, row WorkspaceRow) error
	DeleteUserWorkspace(ctx context.Context, userID, surfaceID string) error
}

// WorkspaceRow is one surface_user_workspace row.
type WorkspaceRow struct {
	UserID             string
	SurfaceID          string
	SplitPct           int
	Arrangement        string
	PrimaryCollapsed   bool
	SecondaryCollapsed bool
	CostHidden         bool
	// OpenRef is the surface-neutral "object last open here" reference (0174), opaque to the
	// kit, interpreted by the surface's own frontend. Display state, never authority.
	OpenRef  string
	WriteKey string
}

// WorkspaceView is the resolved workspace answer (the wire shape).
type WorkspaceView struct {
	SplitPct           int    `json:"split_pct"`
	Arrangement        string `json:"arrangement"`
	PrimaryCollapsed   bool   `json:"primary_collapsed"`
	SecondaryCollapsed bool   `json:"secondary_collapsed"`
	CostHidden         bool   `json:"cost_hidden"`
	OpenRef            string `json:"open_ref,omitempty"`
	Source             string `json:"source"` // "user" | "dealer"
	Replayed           bool   `json:"replayed,omitempty"`
}

// WorkspaceSaveInput is the my-workspace PUT body.
type WorkspaceSaveInput struct {
	SplitPct           int    `json:"split_pct"`
	Arrangement        string `json:"arrangement"`
	PrimaryCollapsed   bool   `json:"primary_collapsed"`
	SecondaryCollapsed bool   `json:"secondary_collapsed"`
	CostHidden         bool   `json:"cost_hidden"`
	OpenRef            string `json:"open_ref"`
	IdempotencyKey     string `json:"idempotency_key"`
}

// SurfaceHooks is what a module contributes for its registered surface. Both hooks are optional
// and fail soft: a nil Policy leaves the config's policy field absent; nil WorkspaceDefaults
// resolves to the kit constants. THE POLICY HOOK GATES ITS OWN FIELDS, cost/margin knobs a
// caller may not see must already be byte-absent from what it returns.
type SurfaceHooks struct {
	Policy            func(ctx context.Context, branchID string) (any, error)
	WorkspaceDefaults func(ctx context.Context, branchID string) (splitPct int, arrangement string, err error)
}

// Service is the kit's logic. One instance serves every registered surface.
type Service struct {
	store      StoreIface
	hooks      map[string]SurfaceHooks
	callerFrom func(context.Context) (identity.StaffContext, bool)
}

// NewService builds the kit service over a store.
func NewService(store StoreIface) *Service {
	return &Service{
		store:      store,
		hooks:      map[string]SurfaceHooks{},
		callerFrom: identity.StaffContextFromContext,
	}
}

// WithSurface registers a module's hooks for one surface id. Chainable at wiring time.
func (s *Service) WithSurface(surfaceID string, h SurfaceHooks) *Service {
	s.hooks[surfaceID] = h
	return s
}

// withCaller overrides caller resolution (tests only).
func (s *Service) withCaller(f func(context.Context) (identity.StaffContext, bool)) *Service {
	s.callerFrom = f
	return s
}

func (s *Service) caller(ctx context.Context) (identity.StaffContext, error) {
	c, ok := s.callerFrom(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, Errf(http.StatusUnauthorized, "settings: no authenticated staff caller")
	}
	return c, nil
}

// NotAdminError is the refusal a non-admin gets at a dealer-curation write. Typed so the handler
// can answer 403 with the sentence (the quote NotSettingsAdminError posture).
type NotAdminError struct{ RoleCode string }

func (e *NotAdminError) Error() string {
	if e.RoleCode == "" {
		return "settings: changing dealer screen settings is limited to administrators"
	}
	return fmt.Sprintf("settings: changing dealer screen settings is limited to administrators, %s cannot do it; ask a tech admin", e.RoleCode)
}

// SurfaceConfig is the GET /api/v1/surfaces/{surface}/config answer.
type SurfaceConfig struct {
	SurfaceID string `json:"surface_id"`
	// Profile is THE CALLER'S OWN resolved job shape, and it means that and nothing else.
	//
	// THIS FIELD IS SECURITY-BEARING, which is why the profile widening below does not touch it.
	// quote-workarea.ts gates its settings button on `config.profile === 'admin'`; echoing back a
	// REQUESTED profile here would let any staff member ask for `?profile=admin` and be handed the
	// admin control. The requested profile gets its own field.
	Profile string `json:"profile"`
	// ItemsProfile names whose curation RoleItems actually is. It equals Profile except when an
	// admin asked for another job shape's rows, which is the whole point of the widening.
	ItemsProfile string        `json:"items_profile,omitempty"`
	RoleItems    []Item        `json:"role_items"`
	UserItems    []Item        `json:"user_items"`
	Workspace    WorkspaceView `json:"workspace"`
	Policy       any           `json:"policy,omitempty"`
}

// ConfigOpt narrows a config read. Options rather than a fourth positional argument so the six
// existing call sites stay byte-identical, a widening that forces every caller to change is a
// widening that gets worked around.
type ConfigOpt func(*configOpts)

type configOpts struct{ profile string }

// ForProfile asks for ANOTHER job shape's curation instead of the caller's own.
//
// THE WART THIS CLOSES. An admin editing the yard profile's columns writes with
// PUT /items {profile: "yard"} and then re-read... their OWN profile's rows, because the read had no
// way to name a profile at all. So an editor wrote one thing and displayed another, and the only way
// to see what you had just saved was to be the role you had saved it for.
//
// IT IS ADMIN-GATED, because reading another shape's curation is the read half of the same admin act
// as writing it (PutRoleItems is gated identically). A frontline caller asking for `manager` is
// refused before the store is touched.
func ForProfile(p string) ConfigOpt { return func(o *configOpts) { o.profile = p } }

// Config resolves the whole surface view for the calling user (one call per surface open).
func (s *Service) Config(ctx context.Context, surfaceID, branchID string, opts ...ConfigOpt) (SurfaceConfig, error) {
	if err := ValidateSurfaceID(surfaceID); err != nil {
		return SurfaceConfig{}, err
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return SurfaceConfig{}, err
	}
	profile, err := s.store.ProfileFor(ctx, caller.RoleCode)
	if err != nil || profile == "" {
		// Least-privileged fallback, never an error: a missing mapping is the 0140 posture.
		profile = ProfileFallback
	}

	var o configOpts
	for _, fn := range opts {
		fn(&o)
	}
	// The profile whose ROWS are read. Defaults to the caller's, which is what every existing caller
	// gets and why none of them changed.
	itemsProfile := profile
	if o.profile != "" && o.profile != profile {
		if err := ValidateProfile(o.profile); err != nil {
			return SurfaceConfig{}, err
		}
		if !caller.IsAdmin() {
			return SurfaceConfig{}, &NotAdminError{RoleCode: caller.RoleCode}
		}
		itemsProfile = o.profile
	}

	roleItems, err := s.store.ReadRoleItems(ctx, surfaceID, itemsProfile)
	if err != nil {
		return SurfaceConfig{}, err
	}
	userItems, err := s.store.ReadUserItems(ctx, caller.UserID, surfaceID)
	if err != nil {
		return SurfaceConfig{}, err
	}
	ws, err := s.resolveWorkspace(ctx, caller.UserID, surfaceID, branchID)
	if err != nil {
		return SurfaceConfig{}, err
	}
	cfg := SurfaceConfig{
		SurfaceID: surfaceID, Profile: profile, ItemsProfile: itemsProfile,
		RoleItems: roleItems, UserItems: userItems, Workspace: ws,
	}
	if h, ok := s.hooks[surfaceID]; ok && h.Policy != nil {
		p, perr := h.Policy(ctx, branchID)
		if perr != nil {
			return SurfaceConfig{}, perr
		}
		cfg.Policy = p
	}
	return cfg, nil
}

// PutRoleItems replaces the dealer's item set for one (surface, profile, kinds present in the
// set). Admin-gated in the service (see the file header).
func (s *Service) PutRoleItems(ctx context.Context, surfaceID, profile string, items []Item) error {
	if err := ValidateSurfaceID(surfaceID); err != nil {
		return err
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if !caller.IsAdmin() {
		return &NotAdminError{RoleCode: caller.RoleCode}
	}
	if err := ValidateProfile(profile); err != nil {
		return err
	}
	if err := ValidateItems(items); err != nil {
		return err
	}
	return s.store.ReplaceRoleItems(ctx, surfaceID, profile, items, caller.UserID)
}

// ResetRoleItems clears the dealer's curation of ONE KIND on one surface+profile, so the level
// BENEATH the dealer answers again.
//
// D-111 made this necessary. Before it, the only level under a dealer's curation was a constant in
// Go, so "the dealer curated the defaults into their own level" was untidy rather than wrong. Now
// there is a platform roster underneath, and a dealer who has once pressed reset-and-save would be
// permanently deaf to it. Reset therefore has to be a DELETE, which is already what reset means
// for a person's own override (ResetUserItems) and for a workspace row.
//
// Admin-gated exactly like PutRoleItems: clearing the dealer's curation is a dealer-curation write.
// The kind scope is the store's contract; see Store.DeleteRoleItems.
func (s *Service) ResetRoleItems(ctx context.Context, surfaceID, profile, kind string) error {
	if err := ValidateSurfaceID(surfaceID); err != nil {
		return err
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if !caller.IsAdmin() {
		return &NotAdminError{RoleCode: caller.RoleCode}
	}
	if err := ValidateProfile(profile); err != nil {
		return err
	}
	switch kind {
	case KindPanel, KindColumn, KindChip, KindAction:
	default:
		return Errf(http.StatusUnprocessableEntity,
			"settings: item kind must be panel, column, chip or action")
	}
	deleter, ok := s.store.(RoleItemDeleter)
	if !ok {
		return Errf(http.StatusServiceUnavailable, "settings: this deployment cannot clear dealer curation")
	}
	return deleter.DeleteRoleItems(ctx, surfaceID, profile, kind)
}

// RoleItemDeleter is the reset seam, asserted at the call site rather than widened into StoreIface
// so a store that predates D-111 keeps compiling and simply cannot reset, which is a refusal in
// words, not a panic.
type RoleItemDeleter interface {
	DeleteRoleItems(ctx context.Context, surfaceID, profile, kind string) error
}

// PutUserItems replaces the CALLER's own override for one surface (whole-or-absent; the stored
// set IS the override). Self-service on purpose: hiding a column is not an admin act.
func (s *Service) PutUserItems(ctx context.Context, surfaceID string, items []Item) error {
	if err := ValidateSurfaceID(surfaceID); err != nil {
		return err
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if err := ValidateItems(items); err != nil {
		return err
	}
	return s.store.ReplaceUserItems(ctx, caller.UserID, surfaceID, items)
}

// ResetUserItems deletes the caller's override, reset IS a DELETE (the 0140/0149 ruling).
func (s *Service) ResetUserItems(ctx context.Context, surfaceID string) error {
	if err := ValidateSurfaceID(surfaceID); err != nil {
		return err
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	return s.store.DeleteUserItems(ctx, caller.UserID, surfaceID)
}

// resolveWorkspace answers the caller's workspace: their stored row when present (source "user"),
// else the surface's dealer defaults via the module hook (source "dealer"). Collapse flags default
// open on the dealer path, nobody configures "everyone starts collapsed" (the 0166 ruling).
func (s *Service) resolveWorkspace(ctx context.Context, userID, surfaceID, branchID string) (WorkspaceView, error) {
	row, ok, err := s.store.ReadUserWorkspace(ctx, userID, surfaceID)
	if err != nil {
		return WorkspaceView{}, err
	}
	if ok {
		split, arr, verr := ValidateGeometry("settings", row.SplitPct, row.Arrangement, SplitDefault)
		if verr != nil {
			return WorkspaceView{}, verr
		}
		return WorkspaceView{
			SplitPct: split, Arrangement: arr,
			PrimaryCollapsed: row.PrimaryCollapsed, SecondaryCollapsed: row.SecondaryCollapsed,
			CostHidden: row.CostHidden, OpenRef: row.OpenRef, Source: "user",
		}, nil
	}
	defSplit, defArr := SplitDefault, ArrangementPrimaryLeft
	if h, hok := s.hooks[surfaceID]; hok && h.WorkspaceDefaults != nil {
		if ds, da, derr := h.WorkspaceDefaults(ctx, branchID); derr == nil {
			if s2, a2, verr := ValidateGeometry("settings", ds, da, SplitDefault); verr == nil {
				defSplit, defArr = s2, a2
			}
		}
		// A failed defaults read degrades to the kit constants: prefs never block a screen
		// (empty-never-error).
	}
	return WorkspaceView{SplitPct: defSplit, Arrangement: defArr, Source: "dealer"}, nil
}

// Workspace reads the caller's resolved workspace for one surface.
func (s *Service) Workspace(ctx context.Context, surfaceID, branchID string) (WorkspaceView, error) {
	if err := ValidateSurfaceID(surfaceID); err != nil {
		return WorkspaceView{}, err
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return WorkspaceView{}, err
	}
	return s.resolveWorkspace(ctx, caller.UserID, surfaceID, branchID)
}

// SaveWorkspace upserts the CALLER's own row for one surface. Requires the settings-write
// idempotency key; a verbatim replay of the last accepted key returns the STORED row without a
// second write (the 0166 contract, the replay answers the store, never the input).
func (s *Service) SaveWorkspace(ctx context.Context, surfaceID string, in WorkspaceSaveInput) (WorkspaceView, error) {
	if err := ValidateSurfaceID(surfaceID); err != nil {
		return WorkspaceView{}, err
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return WorkspaceView{}, err
	}
	if in.IdempotencyKey == "" {
		return WorkspaceView{}, Errf(http.StatusUnprocessableEntity,
			"settings: idempotency_key is required on a workspace write")
	}
	split, arr, err := ValidateGeometry("settings", in.SplitPct, in.Arrangement, SplitDefault)
	if err != nil {
		return WorkspaceView{}, err
	}
	if err := ValidateOpenRef("settings", in.OpenRef); err != nil {
		return WorkspaceView{}, err
	}
	if prev, ok, rerr := s.store.ReadUserWorkspace(ctx, caller.UserID, surfaceID); rerr != nil {
		return WorkspaceView{}, rerr
	} else if ok && prev.WriteKey == in.IdempotencyKey {
		return WorkspaceView{
			SplitPct: prev.SplitPct, Arrangement: prev.Arrangement,
			PrimaryCollapsed: prev.PrimaryCollapsed, SecondaryCollapsed: prev.SecondaryCollapsed,
			CostHidden: prev.CostHidden, OpenRef: prev.OpenRef, Source: "user", Replayed: true,
		}, nil
	}
	row := WorkspaceRow{
		UserID: caller.UserID, SurfaceID: surfaceID, SplitPct: split, Arrangement: arr,
		PrimaryCollapsed: in.PrimaryCollapsed, SecondaryCollapsed: in.SecondaryCollapsed,
		CostHidden: in.CostHidden, OpenRef: in.OpenRef, WriteKey: in.IdempotencyKey,
	}
	if err := s.store.UpsertUserWorkspace(ctx, row); err != nil {
		return WorkspaceView{}, err
	}
	return WorkspaceView{
		SplitPct: split, Arrangement: arr,
		PrimaryCollapsed: in.PrimaryCollapsed, SecondaryCollapsed: in.SecondaryCollapsed,
		CostHidden: in.CostHidden, OpenRef: in.OpenRef, Source: "user",
	}, nil
}

// ResetWorkspace deletes the caller's row for one surface, reset IS a delete.
func (s *Service) ResetWorkspace(ctx context.Context, surfaceID string) error {
	if err := ValidateSurfaceID(surfaceID); err != nil {
		return err
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	return s.store.DeleteUserWorkspace(ctx, caller.UserID, surfaceID)
}
