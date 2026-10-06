package messagingprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// prefs.go, the consent record (customer_sms_prefs, migration 0109) and the rules that read it.
//
// THE ONLY QUESTION THIS FILE ANSWERS IS "MAY WE TEXT THIS PERSON, AT WHAT NUMBER, AND WHEN".
// It is deliberately separate from the phone number a salesperson typed into customer_contacts. That
// column is how we CALL somebody. Texting is a different permission, and this program treats it as
// one: express consent, recorded, revocable, with the absent state meaning no.

// SMSPrefs is one customer's messaging consent.
type SMSPrefs struct {
	CustomerID    string `json:"customer_id"`
	PhoneE164     string `json:"phone_e164"`
	Verified      bool   `json:"sms_verified"`
	OptedOut      bool   `json:"opted_out"`
	OptOutReason  string `json:"opt_out_reason,omitempty"`
	QuietStartMin int    `json:"quiet_start_min"`
	QuietEndMin   int    `json:"quiet_end_min"`
	TimeZone      string `json:"time_zone"`
}

// Sendable is the whole consent rule in one place: a verified, non-opted-out, well-formed number.
//
// Every automation in this bundle asks THIS function rather than reading the flags itself, so there is
// exactly one definition of "may we text them" and a future rule (a bounced-number flag, a per-topic
// preference) changes one expression rather than four call sites.
func (p SMSPrefs) Sendable() bool {
	return p.Verified && !p.OptedOut && looksE164(p.PhoneE164)
}

// QuietHours renders this customer's window, falling back to the house rule on an unparseable zone.
// The returned window is never one that permits a 03:00 text, see NewQuietHours and LoadZone.
func (p SMSPrefs) QuietHours() QuietHours {
	loc, _ := LoadZone(p.TimeZone)
	return NewQuietHours(p.QuietStartMin, p.QuietEndMin, loc)
}

// PrefsStore reads and writes customer_sms_prefs. A nil pool is tolerated (scaffold/offline mode):
// every read reports "no consent on file", which is the safe answer.
type PrefsStore struct{ pool *pgxpool.Pool }

// NewPrefsStore builds the store; pass a nil pool in scaffold mode.
func NewPrefsStore(pool *pgxpool.Pool) *PrefsStore { return &PrefsStore{pool: pool} }

const prefsCols = `customer_id::text, phone_e164, sms_verified, opted_out, opt_out_reason,
                   quiet_start_min, quiet_end_min, time_zone`

func scanPrefs(row pgx.Row) (SMSPrefs, error) {
	var p SMSPrefs
	err := row.Scan(&p.CustomerID, &p.PhoneE164, &p.Verified, &p.OptedOut, &p.OptOutReason,
		&p.QuietStartMin, &p.QuietEndMin, &p.TimeZone)
	return p, err
}

// rowQuerier is the one read both the pool and a transaction can answer. It lets the same statement
// serve a pool-level read (Get) and a read that must not move between the decision and the commit
// (getOn under a caller's transaction), so the under-transaction read never asks the shared pool for
// a second connection while the first is held.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Get returns one customer's preferences. found=false means NO ROW, which means no consent, it is
// not an error and callers must treat it as a refusal, not a default.
func (s *PrefsStore) Get(ctx context.Context, customerID string) (SMSPrefs, bool, error) {
	if s.pool == nil || strings.TrimSpace(customerID) == "" {
		return SMSPrefs{}, false, nil
	}
	return getOn(ctx, s.pool, customerID)
}

// getOn is Get through the querier given: the pool for a standalone read, the caller's transaction
// when the answer must be the row as of the commit, not as of some earlier read. found=false means
// NO ROW, the same refusal Get reports.
func getOn(ctx context.Context, q rowQuerier, customerID string) (SMSPrefs, bool, error) {
	p, err := scanPrefs(q.QueryRow(ctx,
		`SELECT `+prefsCols+` FROM customer_sms_prefs WHERE customer_id = $1`, customerID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SMSPrefs{}, false, nil
		}
		return SMSPrefs{}, false, fmt.Errorf("messagingprovider: read sms prefs: %w", err)
	}
	return p, true, nil
}

// Upsert records or updates a customer's consent. Verifying a number stamps verified_at; this is the
// only path that may set sms_verified, and it refuses a number that is not E.164 rather than storing
// something a carrier will reject and bill us for.
func (s *PrefsStore) Upsert(ctx context.Context, customerID, phoneE164 string, verified bool) error {
	if s.pool == nil {
		return errNoDB
	}
	phoneE164 = strings.TrimSpace(phoneE164)
	if verified && !looksE164(phoneE164) {
		return fmt.Errorf("messagingprovider: cannot verify %q, not an E.164 number", redactPhone(phoneE164))
	}
	var verifiedAt any
	if verified {
		verifiedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO customer_sms_prefs (customer_id, phone_e164, sms_verified, verified_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (customer_id) DO UPDATE
		SET phone_e164 = EXCLUDED.phone_e164, sms_verified = EXCLUDED.sms_verified,
		    verified_at = COALESCE(EXCLUDED.verified_at, customer_sms_prefs.verified_at),
		    updated_at = now()`,
		customerID, phoneE164, verified, verifiedAt)
	if err != nil {
		return fmt.Errorf("messagingprovider: upsert sms prefs: %w", err)
	}
	return nil
}

// SetOptOut flips the kill switch. Opting OUT is recorded with a reason and a timestamp; opting back
// IN clears both, because a stale "opted out on 3 March" beside opted_out=false reads as a mistake.
//
// This is deliberately an UPSERT: a STOP reply from a customer we have no prefs row for must still
// create the record that stops us ever texting them, even though we were not texting them yet.
func (s *PrefsStore) SetOptOut(ctx context.Context, customerID string, out bool, reason string) error {
	if s.pool == nil {
		return errNoDB
	}
	var at any
	if out {
		at = time.Now().UTC()
	} else {
		reason = ""
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO customer_sms_prefs (customer_id, opted_out, opted_out_at, opt_out_reason)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (customer_id) DO UPDATE
		SET opted_out = EXCLUDED.opted_out, opted_out_at = EXCLUDED.opted_out_at,
		    opt_out_reason = EXCLUDED.opt_out_reason, updated_at = now()`,
		customerID, out, at, strings.TrimSpace(reason))
	if err != nil {
		return fmt.Errorf("messagingprovider: set opt-out: %w", err)
	}
	return nil
}

// SetQuietHours records a customer's own window. Out-of-range values are rejected rather than clamped:
// silently turning "22:00" into 20:00 would tell the customer we honoured a preference we did not.
func (s *PrefsStore) SetQuietHours(ctx context.Context, customerID string, startMin, endMin int, zone string) error {
	if s.pool == nil {
		return errNoDB
	}
	if startMin < 0 || startMin > 1439 || endMin < 0 || endMin > 1439 || startMin == endMin {
		return fmt.Errorf("messagingprovider: quiet hours %d-%d are not a usable window", startMin, endMin)
	}
	if zone == "" {
		zone = DefaultZone
	}
	if _, err := LoadZone(zone); err != nil {
		return fmt.Errorf("messagingprovider: quiet hours: %w", err)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO customer_sms_prefs (customer_id, quiet_start_min, quiet_end_min, time_zone)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (customer_id) DO UPDATE
		SET quiet_start_min = EXCLUDED.quiet_start_min, quiet_end_min = EXCLUDED.quiet_end_min,
		    time_zone = EXCLUDED.time_zone, updated_at = now()`,
		customerID, startMin, endMin, zone)
	if err != nil {
		return fmt.Errorf("messagingprovider: set quiet hours: %w", err)
	}
	return nil
}

// redactPhone renders a number for an error message without putting it in a log in full: the last two
// digits only, which is enough to tell two numbers apart while debugging and not enough to dial.
func redactPhone(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 2 {
		return "***"
	}
	return "***" + s[len(s)-2:]
}
