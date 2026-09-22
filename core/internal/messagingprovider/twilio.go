package messagingprovider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// twilio.go, the LIVE SMS transport. It compiles, it is unit-tested against a local test server, and
// it is unreachable at runtime unless an operator sets four environment variables (see Settings).
//
// NO CREDENTIAL APPEARS IN THIS FILE, THIS PACKAGE, OR ANY TEST. The SID and token arrive as strings
// on Settings and are used in exactly one place, the HTTP Basic auth header, and are never logged,
// never included in an error message, and never written to the outbox. The error paths below deal in
// status codes and Twilio's own message text; if Twilio ever echoed a credential back, the redaction
// below would still keep it out of our logs.
//
// SCOPE: this sends a message. It does NOT implement delivery-status callbacks, media (MMS), messaging
// services, or number provisioning. Those need a public webhook endpoint and a decision about who owns
// carrier status, a separate piece of work, deliberately not smuggled in here.

// twilioTimeout bounds one API call. The dispatcher holds a row lock while a handler runs, so a
// provider that hangs must not hold that lock indefinitely.
const twilioTimeout = 15 * time.Second

// defaultTwilioBase is the public API host. Overridable ONLY via TWILIO_API_BASE, which exists so the
// unit test can point at an httptest server; production never sets it.
const defaultTwilioBase = "https://api.twilio.com"

// TwilioNotifier delivers SMS through Twilio's Messages API. It satisfies notify.Notifier, so the
// dispatcher, the retry policy and every caller are identical to the stub's, the only difference is
// that this one actually costs money and reaches a human.
type TwilioNotifier struct {
	accountSID string
	authToken  string
	from       string
	base       string
	http       *http.Client
	log        *slog.Logger
}

// NewTwilioNotifier builds the live transport. It performs NO network call, construction is free and
// side-effect-free, which is what lets a test assert the selector picks this type without any
// credential being real.
func NewTwilioNotifier(s Settings, log *slog.Logger) *TwilioNotifier {
	base := s.APIBase
	if base == "" {
		base = defaultTwilioBase
	}
	return &TwilioNotifier{
		accountSID: s.AccountSID,
		authToken:  s.AuthToken,
		from:       s.FromNumber,
		base:       strings.TrimRight(base, "/"),
		http:       &http.Client{Timeout: twilioTimeout},
		log:        log,
	}
}

func (n *TwilioNotifier) Name() string { return "twilio" }

// Live is true: this one really delivers. The boot check uses it to decide whether to warn.
func (n *TwilioNotifier) Live() bool { return true }

// errNotSMS, an EMAIL message was routed to the SMS transport. That is a wiring bug, not a transient
// failure, so it is returned as a permanent error and allowed to dead-letter rather than retry.
var errNotSMS = errors.New("twilio: only SMS messages can be sent through this notifier")

// Send posts one message to Twilio's Messages endpoint.
//
// Retry semantics matter here because the dispatcher retries on any error and a text message is not
// idempotent, a retried send is a SECOND text to a customer. So:
//   - 2xx → nil (published; no retry).
//   - 4xx → a permanent error. Twilio 4xx means the request itself is wrong (bad number, unverified
//     sender, blocked recipient). Retrying it four more times sends nothing and burns the row toward
//     dead-letter anyway, so it fails immediately and lands in the DEAD pile where an operator can see
//     it. The one exception is 429 (rate limited), which IS transient and is retried.
//   - 5xx / transport error → a retryable error (Twilio accepted nothing; a retry is safe).
func (n *TwilioNotifier) Send(ctx context.Context, m notify.Message) error {
	if m.Channel != notify.SMSChannel {
		return errNotSMS
	}
	if strings.TrimSpace(m.To) == "" {
		return errors.New("twilio: empty recipient")
	}

	form := url.Values{}
	form.Set("To", m.To)
	form.Set("From", n.from)
	form.Set("Body", m.Body)

	endpoint := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Messages.json", n.base, url.PathEscape(n.accountSID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("twilio: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// The ONLY place the credentials are used. SetBasicAuth does not log.
	req.SetBasicAuth(n.accountSID, n.authToken)

	res, err := n.http.Do(req)
	if err != nil {
		// A transport failure is retryable; the request never reached Twilio, so no text was sent.
		return fmt.Errorf("twilio: post: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, res.Body); _ = res.Body.Close() }()

	if res.StatusCode >= 200 && res.StatusCode < 300 {
		if n.log != nil {
			// Recipient and status only. Never the body (it can name a customer's order) and never
			// anything derived from the credentials.
			n.log.Info("sms: delivered via twilio", "to", m.To, "status", res.StatusCode)
		}
		return nil
	}

	detail := readBounded(res.Body, 512)
	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
		return fmt.Errorf("twilio: transient %d: %s", res.StatusCode, detail)
	}
	// Permanent: wrap in a value the dispatcher will keep failing on until it dead-letters, but do it
	// on the FIRST attempt's terms, the message text says why, so the DEAD row is diagnosable.
	return fmt.Errorf("twilio: permanent %d (will not succeed on retry): %s", res.StatusCode, detail)
}

// readBounded reads at most n bytes of an error body so a hostile or enormous response cannot end up
// in a log line or a last_error column.
func readBounded(r io.Reader, n int64) string {
	b, _ := io.ReadAll(io.LimitReader(r, n))
	return strings.TrimSpace(string(b))
}
