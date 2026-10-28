package purchasing

// journal_clock.go, purchasing's read seam over the GL's own as-of filing rule
// (W2-9a round 2b, S2's P2 1): a document's as-of instant is its posting journal's
// effective instant, the same late filing rule the GL's account reads keep. The
// adapter over the gl service lives in the composition root and the tests; no
// purchasing SQL touches a GL table.

import (
	"context"
	"time"
)

// GLRefJournalInstant mirrors one GL journal's as-of filing by value, so purchasing
// does not import gl's internals: the source document ref the journal was filed
// against, the instant the journal carries (its document date), and the instant an
// as of read counts it at (its posted_at, except a late filed journal, which counts
// from the end of the period it was filed in).
type GLRefJournalInstant struct {
	RefID       string
	PostedAt    time.Time
	EffectiveAt time.Time
}

// GLJournalClock is the read seam. The clock is optional and degrades downward when
// absent: the document's own dates keep the inclusion rule the seam replaced (the
// bill's bill_date, the payment's paid_at), the behavior this seam corrects.
type GLJournalClock interface {
	// JournalEffectiveInstants answers each named POSTED journal's effective instant;
	// a journal with no POSTED row is absent from the map.
	JournalEffectiveInstants(ctx context.Context, journalIDs []string) (map[string]time.Time, error)
	// RefJournalInstants answers, for every POSTED journal filed against source with
	// one of refIDs as its source ref, the ref, the instant the journal carries and
	// the instant an as of read counts it at.
	RefJournalInstants(ctx context.Context, source string, refIDs []string) ([]GLRefJournalInstant, error)
}

// WithGLJournalClock binds the GL journal clock seam.
func (s *Service) WithGLJournalClock(c GLJournalClock) *Service {
	s.journalClock = c
	return s
}
