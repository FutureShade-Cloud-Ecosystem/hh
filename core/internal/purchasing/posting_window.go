package purchasing

// posting_window.go, purchasing's read seam over the GL's posting horizon. A module that takes
// a document date asks the GL, which owns the fiscal periods, whether a posting dated on that
// day would file; the answer comes back in the GL's own words. No purchasing SQL touches a GL
// table, and the rule lives in one place: the GL's posting path.

import (
	"context"
	"time"
)

// GLPostingWindow is the read seam over the GL's posting horizon. PostingDateRefusal answers
// the words a posting dated on the given instant would be refused with, or "" when it would
// post.
type GLPostingWindow interface {
	PostingDateRefusal(ctx context.Context, at time.Time) (string, error)
}

// WithGLPostingWindow binds the GL posting window seam. Nil leaves the horizon unread, the
// way the other optional seams degrade.
func (s *Service) WithGLPostingWindow(w GLPostingWindow) *Service {
	s.postingWindow = w
	return s
}
