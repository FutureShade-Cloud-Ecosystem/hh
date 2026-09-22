package dashboard

import (
	"context"
	"errors"
	"log/slog"
)

// quickaction_service.go, DIB-434. The service half of the quick-action registry: resolve what a
// caller may see, and let an admin change it.
//
// It is a SEPARATE service from *Service rather than four more methods on it, for one reason: the
// cockpit's Service exists to gather measurements behind a provider seam, and a quick action has no
// measurement. Bolting the config onto it would have meant widening the Store interface every
// module's fake in this package already implements. This one owns its own store and its own routes,
// and shares only the thing the two genuinely share, the role → job-shape mapping.

// ErrUnknownAction, a caller named an action id outside the registry.
var ErrUnknownAction = errors.New("unknown quick action")

// ProfileResolver is the ONE thing the quick-action service borrows from the cockpit: which of the
// five job shapes a role code is. Declared as an interface (satisfied by *Service) so this service
// can be tested with a two-line fake instead of a layout store.
type ProfileResolver interface {
	ProfileFor(ctx context.Context, roleCode string) string
}

// QuickActionStore is the persistence seam for 0151. Satisfied by *QuickActionRepository (pgx) and
// by a fake in the tests, so every rule above is exercised without a database while the integration
// test still proves the SQL.
type QuickActionStore interface {
	// Configs returns the stored configuration for every action that has one. An action with no
	// row must be ABSENT from the map rather than present-and-zero: "nobody has configured this"
	// and "an admin turned this off" are different facts and the resolver treats them differently.
	Configs(ctx context.Context) (map[string]ActionConfig, error)
	SaveConfig(ctx context.Context, cfg ActionConfig, by string) error
}

// QuickActionService resolves visibility and stores an admin's edits.
type QuickActionService struct {
	store    QuickActionStore
	profiles ProfileResolver
	log      *slog.Logger
}

func NewQuickActionService(store QuickActionStore, profiles ProfileResolver, log *slog.Logger) *QuickActionService {
	return &QuickActionService{store: store, profiles: profiles, log: log}
}

// configs reads the stored configuration, degrading to "nothing stored" on a store failure.
//
// DEGRADE TO THE DEFAULT, NOT TO HIDDEN. If the config table cannot be read, the counter clerk gets
// the shipped default, the button they had yesterday, rather than a silently missing button and
// no explanation. The failure is logged; it is not made into a UI change nobody asked for.
func (s *QuickActionService) configs(ctx context.Context) map[string]ActionConfig {
	if s.store == nil {
		return nil
	}
	cfgs, err := s.store.Configs(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Warn("quick actions: config lookup failed; falling back to shipped defaults", "err", err)
		}
		return nil
	}
	return cfgs
}

// ProfileFor resolves a role code to a job shape, defaulting to counter exactly as the cockpit does
// when no resolver is wired.
func (s *QuickActionService) ProfileFor(ctx context.Context, roleCode string) string {
	if s.profiles == nil {
		return ProfileCounter
	}
	return s.profiles.ProfileFor(ctx, roleCode)
}

// VisibleFor returns the actions a caller with this role code may see, in catalog order.
func (s *QuickActionService) VisibleFor(ctx context.Context, roleCode string) (string, []ActionMeta) {
	profile := s.ProfileFor(ctx, roleCode)
	return profile, ResolveVisible(ActionCatalog, s.configs(ctx), profile)
}

// AdminView returns every registered action with its resolved switch + per-profile matrix.
func (s *QuickActionService) AdminView(ctx context.Context) []EffectiveConfig {
	cfgs := s.configs(ctx)
	out := make([]EffectiveConfig, 0, len(ActionCatalog))
	for _, meta := range ActionCatalog {
		var cfg *ActionConfig
		if c, ok := cfgs[meta.ID]; ok {
			c := c
			cfg = &c
		}
		out = append(out, EffectiveFor(meta, cfg))
	}
	return out
}

// SaveConfig stores one action's configuration (admin) and answers with what it now resolves to.
//
// The submitted matrix is normalised against the closed set of job shapes first: an unknown profile
// name is dropped rather than stored, so a typo cannot leave behind a setting that is invisible in
// every UI and still present in the table.
func (s *QuickActionService) SaveConfig(ctx context.Context, actionID string, enabled bool, profiles map[string]bool, by string) (EffectiveConfig, error) {
	meta, ok := ActionByID[actionID]
	if !ok {
		return EffectiveConfig{}, ErrUnknownAction
	}
	cfg := ActionConfig{ActionID: actionID, Enabled: enabled, Profiles: NormalizeProfiles(profiles)}
	if s.store != nil {
		if err := s.store.SaveConfig(ctx, cfg, by); err != nil {
			return EffectiveConfig{}, err
		}
	}
	return EffectiveFor(meta, &cfg), nil
}
