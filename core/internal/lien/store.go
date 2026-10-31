package lien

import (
	"context"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// lienStore is the persistence seam (mirrors the internal/gl glStore + internal/till tillStore pattern): the
// service holds an interface so unit tests inject a DB-free fake and the integration suite injects the real
// pgx *Repository. Every method reads/writes ONLY lien-owned tables (lien_jobs, lien_participants,
// lien_documents, holdback_ledger).
type lienStore interface {
	InsertJob(ctx context.Context, in JobInput, createdBy string) (Job, error)
	GetJob(ctx context.Context, id string) (Job, error)
	ListJobs(ctx context.Context, branchID string) ([]Job, error)

	// RetentionInvoiceIDs returns the invoice ids already booked as RETENTION for a job (the idempotent-sync
	// guard, a second sync skips these).
	RetentionInvoiceIDs(ctx context.Context, jobID string) (map[string]bool, error)
	// HeldInvoiceIDs returns the invoice ids whose retention ANY lien job's ledger already holds, so a
	// sync never books another job's invoice a second time.
	HeldInvoiceIDs(ctx context.Context) (map[string]bool, error)
	InsertHoldbackEntry(ctx context.Context, e HoldbackEntry) (string, error)
	// InsertRetentionOnce books an invoice's positive RETENTION unless one is held, safe under concurrent
	// callers; it answers the rows written.
	InsertRetentionOnce(ctx context.Context, e HoldbackEntry) (int64, error)
	HoldbackEntriesForJob(ctx context.Context, jobID string) ([]HoldbackEntry, error)
	// HoldbackLedger returns every RETENTION/RELEASE row for a branch ("" = all) for the aging + triage roll-up.
	HoldbackLedger(ctx context.Context, branchID string) ([]HoldbackEntry, error)

	InsertDocument(ctx context.Context, d Document, createdBy string) (Document, error)
	NextDocumentVersion(ctx context.Context, jobID, templateKind string) (int, error)
	DocumentsForJob(ctx context.Context, jobID string) ([]Document, error)
	DocumentVersions(ctx context.Context, documentID string) ([]Document, error)
}

// compile-time assertion: the real pgx Repository satisfies the store seam.
var _ lienStore = (*Repository)(nil)

// withStore swaps the persistence seam (unit tests inject a DB-free fake).
func (s *Service) withStore(st lienStore) *Service { s.store = st; return s }

// withCaller swaps the guard-stamped StaffContext resolver (unit tests).
func (s *Service) withCaller(f func(context.Context) (identity.StaffContext, bool)) *Service {
	s.callerFrom = f
	return s
}

// withClock injects a deterministic clock (unit tests).
func (s *Service) withClock(now func() time.Time) *Service { s.now = now; return s }

// clock returns the service clock (defaults to time.Now).
func (s *Service) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// caller resolves the trusted, server-stamped acting staff member (never a spoofable header, C-1).
func (s *Service) caller(ctx context.Context) (identity.StaffContext, error) {
	if s.callerFrom == nil {
		return identity.StaffContext{}, errUnauthorized
	}
	c, ok := s.callerFrom(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, errUnauthorized
	}
	return c, nil
}
