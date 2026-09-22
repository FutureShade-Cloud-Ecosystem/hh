package tour

// service.go, the one gate, the four writes, and the posture that holds them apart.
//
// -- THE READ CANNOT FAIL, AND THAT IS A SIGNATURE AND NOT A PROMISE -----------------------------
//
// Service.Tour returns a Tour and NO ERROR. Every other read in this file's neighbourhood returns
// (T, error) and a handler maps the error onto a status; this one cannot, because there is no error
// for it to map. That is deliberate and it is the house rule "every failure DEGRADES DOWNWARD and
// never errors" written where it cannot be forgotten: an unreadable policy answers OFF, an
// unreadable chapter list answers OFF, an unreadable bookmark answers "start at the beginning".
//
// THE ALTERNATIVE, AND WHY IT LOSES. `(Tour, error)` with a handler that maps every error to `{}`
// is the same behaviour today and one refactor from a 500 on the app's first screen, a demo that
// dies because a SELECT timed out is the one failure this feature cannot have, since the only
// people who ever see it are people being sold to. dispatch.PolicyFor takes the same shape for the
// same reason and its header says so.
//
// -- ONE METHOD ASKS THE POLICY -----------------------------------------------------------------
//
// Exactly one method in this package reads tour_policy for a rendering decision, and exactly one
// method builds a Tour. Those are the same method. That is what makes byte-absence structural
// rather than remembered: there is no second producer of the payload that could be written next
// year by somebody who did not read this file.
//
// -- THE WRITES DO NOT DEGRADE ------------------------------------------------------------------
//
// The same posture inverted: a copy edit that could not be saved says so. Degrading a WRITE
// downward means a platform administrator retypes a sentence, sees a green tick, and finds the old
// line still on the screen in front of the client. Reads answer quietly; writes answer honestly.

import (
	"context"
	"log/slog"
	"net/http"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// store is the module's persistence seam, satisfied by *Repository and faked in unit tests.
//
// It is UNEXPORTED because chapterRow is: the row shape is a column list and the wire shape is
// Chapter, and an exported seam carrying the row type would invite a caller outside this package to
// treat one as the other. Nothing outside internal/tour has any business persisting a chapter.
type store interface {
	ReadPolicy(ctx context.Context, branchID string) (Policy, error)
	SetDemoMode(ctx context.Context, branchID string, demoMode bool, updatedBy string) error
	ReadVisibleChapters(ctx context.Context) ([]chapterRow, error)
	ReadSteps(ctx context.Context) (map[string][]Step, error)
	UpdateStepCopy(ctx context.Context, in StepEdit, updatedBy string) (bool, error)
	ReadProgress(ctx context.Context, userID string) (map[string]Mark, error)
	UpsertProgress(ctx context.Context, userID string, in ProgressEdit) error
	DeleteProgress(ctx context.Context, userID, chapterKey string) error
}

// Service is the walkthrough's logic.
type Service struct {
	store      store
	log        *slog.Logger
	callerFrom func(context.Context) (identity.StaffContext, bool)
}

// NewService wires the repository.
//
// A NIL REPOSITORY IS A LEGAL DEPLOYMENT, not a programming error, it is what a boot with no
// database looks like, and it must leave `s.store` a nil INTERFACE rather than an interface
// holding a nil pointer. The difference is not pedantry: the second is non-nil to `== nil` and
// panics on the first field access, which would turn "no database" into a crash on the app's
// first screen, which is the one outcome this whole file is arranged to prevent.
func NewService(repo *Repository) *Service {
	s := &Service{log: slog.Default(), callerFrom: identity.StaffContextFromContext}
	if repo != nil {
		s.store = repo
	}
	return s
}

// WithLogger swaps the logger.
func (s *Service) WithLogger(l *slog.Logger) *Service {
	if l != nil {
		s.log = l
	}
	return s
}

// withStore swaps the persistence seam (tests only).
func (s *Service) withStore(st store) *Service { s.store = st; return s }

// withCaller overrides caller resolution (tests only).
func (s *Service) withCaller(f func(context.Context) (identity.StaffContext, bool)) *Service {
	s.callerFrom = f
	return s
}

func (s *Service) caller(ctx context.Context) (identity.StaffContext, error) {
	c, ok := s.callerFrom(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, Errf(http.StatusUnauthorized, "tour: no authenticated staff caller")
	}
	return c, nil
}

// -- the read ------------------------------------------------------------------------------------

// Tour is the whole walkthrough for the calling person, or the zero value.
//
// THE ZERO VALUE IS THE REFUSAL AND IT MARSHALS AS `{}`. Every early return below is `Tour{}`
// written literally, not a partially built value with a flag cleared, so no path can leave a field
// set on a refused answer. Read the returns of this function as a list: they are the complete set
// of ways the walkthrough is absent, and every one of them is spelled the same way.
func (s *Service) Tour(ctx context.Context, branchID string) Tour {
	if s.store == nil {
		return Tour{}
	}
	// FAIL CLOSED ON THE CALLER. The route is staff-gated at the door, so an anonymous context here
	// means something upstream changed; answering OFF is the reading that stays safe when it does.
	caller, ok := s.callerFrom(ctx)
	if !ok || caller.UserID == "" {
		return Tour{}
	}

	policy, err := s.store.ReadPolicy(ctx, branchID)
	if err != nil {
		s.log.Warn("tour: policy unreadable, answering off", "branch_id", branchID, "err", err)
		return Tour{}
	}
	if !policy.On() {
		return Tour{}
	}

	rows, err := s.store.ReadVisibleChapters(ctx)
	if err != nil {
		s.log.Warn("tour: chapters unreadable, answering off", "err", err)
		return Tour{}
	}
	if len(rows) == 0 {
		return Tour{}
	}

	// A WALKTHROUGH WITH NO STEPS IS WORSE THAN NO WALKTHROUGH, so an unreadable step list answers
	// OFF rather than serving nine chapters that open onto nothing. This is the one read whose
	// failure is NOT survivable by degrading a level: a chapter is a container, and an empty
	// container offered to a client is a promise the product then breaks in front of them.
	steps, err := s.store.ReadSteps(ctx)
	if err != nil {
		s.log.Warn("tour: steps unreadable, answering off", "err", err)
		return Tour{}
	}

	// THE BOOKMARK IS THE ONE READ THAT MAY FAIL SOFT. Losing it degrades to "start at the
	// beginning", which is a state the product already has a name for and a person already knows how
	// to be in. Refusing the whole tour because a bookmark table hiccuped would trade a small,
	// recoverable annoyance for the total outage the two reads above are protecting against.
	progress, err := s.store.ReadProgress(ctx, caller.UserID)
	if err != nil {
		s.log.Warn("tour: progress unreadable, answering as unstarted", "err", err)
		progress = nil
	}

	// ONE LOOP, ONE ORDER. The marks are emitted in the order the chapters are, so the payload has a
	// single ordering rather than two that have to be kept in step, and a mark is emitted ONLY for a
	// chapter this body is also sending, which is what keeps `progress` and `chapters` joinable
	// without the client having to decide what an orphan row means.
	out := Tour{Chapters: make([]Chapter, 0, len(rows)), CanEdit: caller.IsSuperAdmin()}
	for _, r := range rows {
		out.Chapters = append(out.Chapters, Chapter{
			ChapterKey: r.Key,
			Position:   r.Position,
			Title:      r.Title,
			Promise:    r.Promise,
			Role:       r.Role,
			Route:      r.Route,
			Steps:      steps[r.Key],
		})
		if mark, seen := progress[r.Key]; seen {
			out.Progress = append(out.Progress, mark)
		}
	}
	return out
}

// -- the platform writes -------------------------------------------------------------------------

// PutStepCopy edits ONE step's line, route and anchor, live, with no deploy.
//
// GATED ON THE PLATFORM OPERATOR, NOT ON "AN ADMINISTRATOR", and gated FIRST. IsAdmin() admits four
// roles including the dealer's own General Manager, so it cannot gate a tier whose entire purpose is
// to sit above the dealer, 0191:44-70 argues that at length and 0192 adopts it. IsSuperAdmin() is
// the only predicate in the identity model that separates the two.
//
// THE ORDER OF THE FIRST TWO CHECKS IS PART OF THE GATE. Authority is resolved and refused BEFORE
// the body is validated, so a refused caller learns nothing about which of their fields was also
// wrong, and, the part that matters, no path exists where a validation branch could reach the
// store ahead of the role check.
func (s *Service) PutStepCopy(ctx context.Context, in StepEdit) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if !caller.IsSuperAdmin() {
		return &NotPlatformAdminError{RoleCode: caller.RoleCode}
	}
	if err := ValidateStepEdit(in); err != nil {
		return err
	}
	if s.store == nil {
		return Errf(http.StatusServiceUnavailable, "tour: the walkthrough is not available on this deployment")
	}
	found, err := s.store.UpdateStepCopy(ctx, in, caller.UserID)
	if err != nil {
		return err
	}
	if !found {
		// NAMED, NOT GUESSED. 0192's ids are add-only and never renamed, so a step_key that matches
		// nothing is a typo or a retired chapter, and both are answered better by saying which id
		// was not found than by inserting a step nobody wrote.
		return Errf(http.StatusNotFound, "tour: %q has no step called %q", in.ChapterKey, in.StepKey)
	}
	return nil
}

// PutPolicy flips the demo knob. Same gate, and for the same reason.
func (s *Service) PutPolicy(ctx context.Context, in PolicyEdit) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if !caller.IsSuperAdmin() {
		return &NotPlatformAdminError{RoleCode: caller.RoleCode}
	}
	if s.store == nil {
		return Errf(http.StatusServiceUnavailable, "tour: the walkthrough is not available on this deployment")
	}
	return s.store.SetDemoMode(ctx, in.BranchID, in.DemoMode, caller.UserID)
}

// -- the caller's own bookmark -------------------------------------------------------------------

// PutProgress records where THIS caller has got to.
//
// THE USER ID COMES FROM THE CONTEXT AND NEVER FROM THE BODY. ProgressEdit has no user_id field at
// all, the strongest version of this rule is the one where the wrong value has nowhere to be typed
// , and this is the only line in the package that decides whose row is written.
//
// IT IS NOT GATED ON THE POLICY, AND THAT IS DELIBERATE. Byte-absence gates the DISPLAY; the
// platform routes above gate the ACT, because editing every deployment's copy is a power. A
// bookmark is neither: it grants nothing, moves no data, and is readable only by the person who
// wrote it (0193 gives a dealer's manager no read of it). Refusing it while a knob is off would
// mean a person walking a chapter loses their place the moment an administrator toggles something
// in another tab, protecting nothing, at the cost of the one thing 0193 exists to provide.
func (s *Service) PutProgress(ctx context.Context, in ProgressEdit) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if err := ValidateProgressEdit(in); err != nil {
		return err
	}
	if s.store == nil {
		return Errf(http.StatusServiceUnavailable, "tour: the walkthrough is not available on this deployment")
	}
	return s.store.UpsertProgress(ctx, caller.UserID, in)
}

// RestartChapter is restart, and restart is a DELETE of this caller's own row.
//
// NEVER A WRITE OF STEP 0. 0193's header is the argument: "never started" and "started and went back
// to the beginning" are the same state to the person, so they are the same state in the table.
// Writing step 0 instead would make them two states every client then has to tell apart, and would
// quietly overwrite a DONE chapter's record with an OPEN one on a restart somebody abandoned.
func (s *Service) RestartChapter(ctx context.Context, chapterKey string) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if !keyRe.MatchString(chapterKey) {
		return Errf(http.StatusUnprocessableEntity, "tour: chapter_key names the chapter to start again")
	}
	if s.store == nil {
		return Errf(http.StatusServiceUnavailable, "tour: the walkthrough is not available on this deployment")
	}
	// A DELETE THAT MATCHED NOTHING IS A SUCCESS. Restarting a chapter you never started leaves you
	// at the beginning of it, which is exactly where the caller asked to be, answering 404 would
	// make the client handle "already where you wanted to be" as a failure.
	return s.store.DeleteProgress(ctx, caller.UserID, chapterKey)
}
