// followup_on_share.go, DIB-482 (M3-G3, half A): minting a share link starts the cadence.
//
// WHAT WAS WRONG
// --------------
// A quote's follow-up cadence was started in exactly ONE place: AutomationService.ShareBySMS
// (followup_service.go:163). That is the path a salesperson takes when they text the quote. It is not
// the path they take most of the time.
//
// The common path is the "Create link" button on the staff quote panel, POST
// /api/v1/quotes/{id}/share-links, which mints the token, moves DRAFT -> SENT, and hands the operator
// a URL to paste into an email or read down the phone. That path started NO cadence. So the ordinary
// way of sending a quote produced a quote that was never followed up, and quote_followup_plans stayed
// empty while the Follow-ups panel at the foot of the very same screen went on saying that sharing a
// quote starts its follow-ups. The panel was not wrong about the intent. Nothing implemented it.
//
// The two paths agree now, and they agree on the SAME terms: ChannelBoth, DefaultOffsets, started at
// the moment of the share. EnsurePlan is an upsert-that-does-nothing, so a quote shared twice, once
// by link, once by SMS, keeps the first cadence and its original clock (followup_repository.go).
//
// WHY THIS NEEDS NO COMPOSITION-ROOT CHANGE
// -----------------------------------------
// The seam is discovered by type assertion on the store the StaffService was already built with,
// exactly as customer.Service discovers scopedAccountSearcher. In production main.go passes
// *quote.Repository, which satisfies followUpPlanner through the one-line delegation below, the SQL
// stays where it lives, on FollowUpRepository, and both repositories are this package's own types
// over the same pool. A fake store in a unit test simply does not implement it, and gets the old
// behaviour rather than a nil dereference.
package quote

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// followUpPlanner is the cadence-starting seam MintShareLink reaches for. It is a SEPARATE, optional
// interface rather than a widened staffStore on purpose: staffStore is implemented outside this
// package by test fakes, and widening it would break every one of them for a capability they do not
// need. The signature is FollowUpRepository.EnsurePlan's, verbatim, so the real store satisfies it
// without an adapter.
type followUpPlanner interface {
	EnsurePlan(ctx context.Context, quoteID, channel string, offsets []int, startedAt time.Time) (FollowUpPlan, error)
}

// EnsurePlan lets the quote Repository start a cadence, by delegating to the repository that owns
// quote_followup_plans. Both types live in this package and hold the same *pgxpool.Pool, so this is a
// forward, not a second implementation: there is still exactly one EnsurePlan statement in the
// codebase, and a change to the cadence's insert semantics cannot half-apply.
func (r *Repository) EnsurePlan(ctx context.Context, quoteID, channel string, offsets []int, startedAt time.Time) (FollowUpPlan, error) {
	return (&FollowUpRepository{pool: r.followUpPool()}).EnsurePlan(ctx, quoteID, channel, offsets, startedAt)
}

// followUpPool hands over this repository's pool. A nil pool is passed through unchanged: the
// FollowUpRepository's own nil-pool posture (every write an error, ErrFollowUpNotWired) is the right
// answer in scaffold mode, and inventing a different one here would give the same code two behaviours
// depending on which door it came through.
func (r *Repository) followUpPool() *pgxpool.Pool { return r.pool }

// startCadenceOnShare is called after a share link is minted. It is BEST-EFFORT by deliberate choice,
// and mirrors ShareBySMS's handling exactly: a cadence that could not be started is logged, never
// returned. The share link is the customer-facing artifact, the operator is on the phone, about to
// read out a URL, and failing that mint because a follow-up row could not be written would deny the
// customer their quote to protect a reminder. The Follow-ups panel reports the absent plan honestly.
func (s *StaffService) startCadenceOnShare(ctx context.Context, quoteID string, now time.Time) {
	planner, ok := s.store.(followUpPlanner)
	if !ok {
		// No cadence store behind this service (a unit-test fake, or a scaffold build). Silent by
		// design: this is a capability that is absent, not an operation that failed.
		return
	}
	if _, err := planner.EnsurePlan(ctx, quoteID, ChannelBoth, DefaultOffsets, now.UTC()); err != nil {
		s.logger().Warn("quote: share link minted but the follow-up plan could not be created",
			"quote_id", quoteID, "err", err)
	}
}

// logger is the StaffService's warning voice.
//
// slog.Default() rather than an injected logger, deliberately. StaffService carries no logger field
// today, and adding one would mean a line in main.go, outside this lane's fence, for the sake of a
// single best-effort warning. The alternative was to swallow the error entirely, and a silently
// dropped cadence is precisely the failure this ticket exists to end. audit.NewService makes the same
// substitution for the same reason.
func (s *StaffService) logger() *slog.Logger { return slog.Default() }
