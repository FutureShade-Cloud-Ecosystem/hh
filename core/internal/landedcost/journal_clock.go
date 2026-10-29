package landedcost

// journal_clock.go, landedcost's read seam over the GL's own as-of filing rule
// (W2-9a round 2b, S2's P3 1): a payable counts from its posting journal's
// effective instant, the same late filing rule the GL's account reads keep. The
// adapter over the gl service lives in the composition root and the tests; no
// landedcost SQL touches a GL table. The clock is optional and degrades downward
// when absent: the row's own created_at and updated_at keep the inclusion rule the
// seam replaced.

import (
	"context"
	"time"
)

// JournalClock is the read seam.
type JournalClock interface {
	// JournalEffectiveInstants answers each named POSTED journal's effective
	// instant; a journal with no POSTED row is absent from the map.
	JournalEffectiveInstants(ctx context.Context, journalIDs []string) (map[string]time.Time, error)
	// JournalEffectiveInstantByKey answers the effective instant of the POSTED
	// journal filed under an idempotency key, found false when none carries it.
	JournalEffectiveInstantByKey(ctx context.Context, key string) (time.Time, bool, error)
}

// WithJournalClock binds the GL journal clock seam.
func (s *Service) WithJournalClock(c JournalClock) *Service {
	s.journalClock = c
	return s
}
