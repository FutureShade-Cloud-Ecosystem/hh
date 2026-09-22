package warranty

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// intake.go, DIB-222 WTY-INTAKE v1: the PUBLIC warranty intake form.
//
// THE DOORWAY. A homeowner with a spalling driveway has no login and never will. The will-call precedent
// (DIB-79) settled how this codebase opens a door to someone with no session: a SEPARATE RegisterPublicRoutes
// method, so "this route has no guard" is a sentence someone wrote deliberately rather than a prefix match.
//
// THE DIFFERENCE FROM WILL-CALL IS THAT THIS ONE WRITES. That makes four properties load-bearing:
//
//  1. IT WRITES EXACTLY ONE THING. A claim in INTAKE phase, source WEB, with its lines and its photos, in one
//     transaction, through ONE store method that can reach nothing else. It cannot assign an owner, cannot pick
//     a vendor, cannot set money, cannot create a customer, and cannot touch a table outside warranty. The
//     contact details it captures are self-reported JSONB on the claim, writing them to the customer master
//     would be letting the public write to the customer master.
//  2. THERE IS NO PUBLIC READ. None. The reference number this returns is a RECEIPT to quote on the phone, not
//     a lookup key, because a public GET keyed on it is precisely the auth bypass the ticket forbids. It is
//     high-entropy anyway (see mintPublicRef) so that it is not guessable even if a read route is ever added
//     against explicit advice.
//  3. SIZE IS BOUNDED AT THE DOOR. Whole-body cap, per-photo cap, photo-count cap, all from the 0131 config
//     row, all refused with a 413 and a sentence rather than a constraint violation surfacing as a 500.
//  4. THE CONTENT TYPE IS CHECKED AGAINST THE BYTES. A declared type is a claim by the uploader and the
//     uploader is the attacker in the case that matters. Same sniffer posture as internal/product's catalogue
//     images: an allowlist of three raster types, and the magic bytes must agree with the declaration, so
//     "image/png" containing HTML is refused rather than stored and later served back on a domain that also
//     serves the authenticated app.
//
// RATE LIMITING IS A DB ROW, KEYED ON A HASH. An in-memory counter resets every deploy and is per-instance,
// which means the limit is whatever the load balancer feels like. And the key is a SHA-256 of the client
// address, never the address: this table throttles abuse, it is not a log of who looked at the form.

// - DTOs -

// IntakeConfig is the 0131 singleton, every tunable of the public door.
type IntakeConfig struct {
	IsEnabled       bool   `json:"is_enabled"`
	MaxBodyBytes    int    `json:"max_body_bytes"`
	MaxPhotoBytes   int    `json:"max_photo_bytes"`
	MaxPhotos       int    `json:"max_photos"`
	WindowSeconds   int    `json:"window_seconds"`
	MaxPerWindow    int    `json:"max_per_window"`
	DefaultBranchID string `json:"default_branch_id,omitempty"`
}

// IntakeContact is what the submitter says about themselves. Untrusted display data.
type IntakeContact struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Phone   string `json:"phone"`
	Company string `json:"company,omitempty"`
}

// PublicIntakeLine is one product line as a member of the public can describe it: free text and a quantity.
// There is no product picker on a public form and there must not be one, a product id arriving from an
// unauthenticated body is an id an attacker chose.
type PublicIntakeLine struct {
	ProductText string `json:"product_text"`
	Qty         string `json:"qty"`
	Notes       string `json:"notes"`
}

// PublicIntakePhoto is one uploaded photo: a declared content type and base64 bytes.
type PublicIntakePhoto struct {
	ContentType string `json:"content_type"`
	DataBase64  string `json:"data_base64"`
	Caption     string `json:"caption"`
}

// PublicIntakeInput is the whole public form payload.
type PublicIntakeInput struct {
	Contact     IntakeContact       `json:"contact"`
	JobAddress  Address             `json:"job_address"`
	Description string              `json:"description"`
	PurchasedOn string              `json:"purchased_on"` // YYYY-MM-DD, self-reported
	Lines       []PublicIntakeLine  `json:"lines"`
	Photos      []PublicIntakePhoto `json:"photos"`
}

// PublicIntakeResult is what the form gets back. Deliberately minimal: a reference and a sentence. No claim id,
// no claim_no, no branch, no anything that would help someone map the inside of the business.
type PublicIntakeResult struct {
	Reference string `json:"reference"`
	Message   string `json:"message"`
}

// IntakePhoto is a stored intake photo (staff-side read).
type IntakePhoto struct {
	ID          string `json:"id,omitempty"`
	ClaimID     string `json:"claim_id,omitempty"`
	ContentType string `json:"content_type"`
	ByteSize    int    `json:"byte_size"`
	Caption     string `json:"caption,omitempty"`
	Checksum    string `json:"checksum,omitempty"`
	UploadedAt  string `json:"uploaded_at,omitempty"`
	Bytes       []byte `json:"-"`
}

// allowedIntakeTypes mirrors the 0131 CHECK. An allowlist, and a security boundary rather than a format
// preference: svg and html carry script and these bytes are served back from this origin.
var allowedIntakeTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

// PublicIntakeLimits exposes the configured door limits to the HTTP layer so the body cap is the OPERATOR's
// number rather than a constant compiled into the handler. Deliberately not a public route: the caps are an
// operational detail and publishing them is publishing the shape of the throttle.
func (s *Service) PublicIntakeLimits(ctx context.Context) (IntakeConfig, error) {
	return s.store.IntakeConfig(ctx)
}

// - the public submit path -

// SubmitPublicIntake is the ONE public write. clientAddr is the server-observed remote address, never a header
// (see rateBucketKey). It performs, in order: the enabled check, the rate-limit check, validation, photo
// decoding + sniffing, and then a single transactional claim write.
//
// It takes NO caller: there is no staff context on this path, and the absence is the point. Nothing downstream
// of here reads s.caller, which is what stops the public doorway from ever becoming an authenticated one by
// accident.
func (s *Service) SubmitPublicIntake(ctx context.Context, in PublicIntakeInput, clientAddr string) (PublicIntakeResult, error) {
	cfg, err := s.store.IntakeConfig(ctx)
	if err != nil {
		return PublicIntakeResult{}, err
	}
	if !cfg.IsEnabled {
		// The form is CLOSED, not broken. 503 says "come back / phone us", which is the true statement.
		return PublicIntakeResult{}, clientErr(503, "warranty: the online warranty intake form is currently closed, please contact the branch")
	}
	if cfg.DefaultBranchID == "" {
		// A claim must land in a branch (0053 NOT NULL). Guessing one would put a real claim in the wrong yard.
		return PublicIntakeResult{}, clientErr(503, "warranty: online intake is not configured (no default branch), please contact the branch")
	}

	// - rate limit. Fixed window, atomic increment, refuse on the FIRST hit over the limit. -
	if err := s.checkIntakeRate(ctx, cfg, clientAddr); err != nil {
		return PublicIntakeResult{}, err
	}

	// - validation. A public form's error messages must be useful to a member of the public and useless to
	// someone mapping the system: they name the FIELD, never a table, id or internal rule. -
	name := strings.TrimSpace(in.Contact.Name)
	if name == "" {
		return PublicIntakeResult{}, unprocessable("warranty: your name is required")
	}
	if strings.TrimSpace(in.Contact.Email) == "" && strings.TrimSpace(in.Contact.Phone) == "" {
		return PublicIntakeResult{}, unprocessable("warranty: an email address or a phone number is required so we can reach you")
	}
	if strings.TrimSpace(in.Description) == "" {
		return PublicIntakeResult{}, unprocessable("warranty: a description of the problem is required")
	}
	if len(in.Description) > 8000 {
		return PublicIntakeResult{}, unprocessable("warranty: the description is too long (8000 characters maximum)")
	}
	if len(in.Lines) > 20 {
		return PublicIntakeResult{}, unprocessable("warranty: at most 20 product lines can be submitted online")
	}
	if len(in.Photos) > cfg.MaxPhotos {
		return PublicIntakeResult{}, unprocessable("warranty: at most %d photos can be attached", cfg.MaxPhotos)
	}
	if strings.TrimSpace(in.PurchasedOn) != "" {
		if _, perr := parseFlexTime(in.PurchasedOn); perr != nil {
			return PublicIntakeResult{}, unprocessable("warranty: the purchase date must be a YYYY-MM-DD date")
		}
	}

	// - photos: decode, cap, sniff. -
	photos := make([]IntakePhoto, 0, len(in.Photos))
	for i, p := range in.Photos {
		ph, perr := decodeIntakePhoto(p, cfg.MaxPhotoBytes, i)
		if perr != nil {
			return PublicIntakeResult{}, perr
		}
		photos = append(photos, ph)
	}

	// - the single write. -
	ref, err := mintPublicRef(s.clock())
	if err != nil {
		return PublicIntakeResult{}, err
	}
	claim, err := s.store.InsertPublicClaim(ctx, in, "", ref, cfg.DefaultBranchID, photos, s.clock())
	if err != nil {
		return PublicIntakeResult{}, err
	}
	s.log.Info("warranty: public intake claim created", "claim_no", claim.ClaimNo, "photos", len(photos))
	return PublicIntakeResult{
		Reference: ref,
		Message:   "Thank you, your warranty claim has been received. Quote reference " + ref + " when you contact us.",
	}, nil
}

// checkIntakeRate increments this address's bucket for the current fixed window and refuses once the count
// EXCEEDS the configured maximum. The increment happens before the decision on purpose: a limiter that only
// counts requests it allowed lets a flood of refused requests reset nothing and cost nothing to send.
func (s *Service) checkIntakeRate(ctx context.Context, cfg IntakeConfig, clientAddr string) error {
	if cfg.MaxPerWindow <= 0 || cfg.WindowSeconds <= 0 {
		return nil
	}
	window := time.Duration(cfg.WindowSeconds) * time.Second
	start := s.clock().UTC().Truncate(window)
	hits, err := s.store.IntakeRateLimitHit(ctx, rateBucketKey(clientAddr), start)
	if err != nil {
		// A limiter that cannot count must not become a limiter that does not limit, but it also must not take
		// the form offline for a legitimate homeowner because a sweep locked a row. Log and allow: the size caps
		// and the content checks are still in force, and this is the least-bad of two bad options, chosen
		// explicitly rather than by omission.
		s.log.Warn("warranty: intake rate-limit accounting failed (request allowed)", "err", err)
		return nil
	}
	if hits > cfg.MaxPerWindow {
		return clientErr(429, "warranty: too many submissions from this connection, please try again later, or contact the branch directly")
	}
	return nil
}

// rateBucketKey hashes the SERVER-OBSERVED client address.
//
// IT IS DELIBERATELY NOT X-Forwarded-For. That header is set by the client on a direct connection, so keying
// the limiter on it would let any submitter mint a fresh bucket per request and make the limit decorative. The
// same reasoning the comms substrate applies to a payload's To field: a value the sender controls is not an
// identity. When a trusted reverse proxy is introduced, the fix is to resolve the real address ONCE at the edge
// and hand it in here, not to start trusting a header at this layer.
//
// The hash means the table never stores an address: this is a throttle, not a visitor log.
func rateBucketKey(clientAddr string) string {
	addr := strings.TrimSpace(clientAddr)
	if i := strings.LastIndex(addr, ":"); i > 0 && !strings.HasSuffix(addr, "]") {
		// Strip the ephemeral source port, otherwise every request from one machine is its own bucket.
		if host := addr[:i]; host != "" {
			addr = strings.Trim(host, "[]")
		}
	}
	if addr == "" {
		addr = "unknown"
	}
	sum := sha256.Sum256([]byte("warranty-intake|" + addr))
	return hex.EncodeToString(sum[:])
}

// decodeIntakePhoto decodes, caps and SNIFFS one photo. Every refusal names which photo (1-based) so a
// submitter with five attachments knows which one to drop.
func decodeIntakePhoto(p PublicIntakePhoto, maxBytes, idx int) (IntakePhoto, error) {
	n := idx + 1
	ct := normalizeIntakeType(p.ContentType)
	if !allowedIntakeTypes[ct] {
		return IntakePhoto{}, clientErr(415, "warranty: photo %d must be a JPEG, PNG or WebP image", n)
	}
	raw := strings.TrimSpace(p.DataBase64)
	// Tolerate a data: URL prefix, browsers hand one back from FileReader and stripping it here is kinder than
	// making every caller remember.
	if i := strings.Index(raw, ";base64,"); i >= 0 {
		raw = raw[i+len(";base64,"):]
	}
	if raw == "" {
		return IntakePhoto{}, unprocessable("warranty: photo %d is empty", n)
	}
	// Cheap pre-check on the ENCODED length: refuse an oversized upload before allocating its decoded form.
	// base64 is 4 bytes per 3, so decoded ≈ encoded × 3/4.
	if len(raw)/4*3 > maxBytes {
		return IntakePhoto{}, clientErr(413, "warranty: photo %d is larger than the %s limit", n, byteLabel(maxBytes))
	}
	body, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return IntakePhoto{}, unprocessable("warranty: photo %d could not be read", n)
	}
	if len(body) == 0 {
		return IntakePhoto{}, unprocessable("warranty: photo %d is empty", n)
	}
	if len(body) > maxBytes {
		return IntakePhoto{}, clientErr(413, "warranty: photo %d is larger than the %s limit", n, byteLabel(maxBytes))
	}
	if sniffed := sniffIntakeType(body); sniffed == "" || sniffed != ct {
		// The declaration and the bytes disagree. This is the arm that stops a text/html payload being stored as
		// "image/png" and served back with a type a browser will render.
		return IntakePhoto{}, clientErr(415, "warranty: photo %d is not a valid %s image", n, ct)
	}
	sum := md5.Sum(body)
	return IntakePhoto{
		ContentType: ct,
		ByteSize:    len(body),
		Bytes:       body,
		Caption:     strings.TrimSpace(p.Caption),
		Checksum:    hex.EncodeToString(sum[:]),
	}, nil
}

// normalizeIntakeType strips parameters ("image/png; charset=binary") and lowercases.
func normalizeIntakeType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

// sniffIntakeType identifies the three allowed formats from their magic bytes; "" for anything else, which the
// caller treats as a refusal. An allowlist by construction, not a denylist that has to anticipate every
// dangerous format. (Same three signatures internal/product checks, kept local because warranty does not
// import a peer for a cross-module need.)
func sniffIntakeType(b []byte) string {
	switch {
	case len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}

// byteLabel renders a byte cap the way a person would say it.
func byteLabel(n int) string {
	switch {
	case n >= 1<<20:
		return strconv.Itoa(n/(1<<20)) + " MB"
	case n >= 1<<10:
		return strconv.Itoa(n/(1<<10)) + " KB"
	default:
		return strconv.Itoa(n) + " bytes"
	}
}

// publicLineNotes preserves a public submitter's quantity VERBATIM when it is not a bare number.
//
// The DIB-222 form asks "how much?" and suggests "180 sq ft", that is how a homeowner knows the answer, and
// the column is DECIMAL(19,4). nullDecimal keeps the number; this keeps the words. Dropping the units would
// leave a triager choosing between square feet, pallets and pieces with no way to tell, which is a worse
// outcome than a slightly redundant note.
func publicLineNotes(l PublicIntakeLine) string {
	notes := strings.TrimSpace(l.Notes)
	raw := strings.TrimSpace(l.Qty)
	if raw == "" || raw == leadingDecimal(raw) {
		return notes // a bare number: the qty column already says everything
	}
	verbatim := "quantity as submitted: " + raw
	if notes == "" {
		return verbatim
	}
	return notes + " · " + verbatim
}

// publicRefAlphabet excludes 0/O/1/I/L, a reference number gets read down a phone line.
const publicRefAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// mintPublicRef generates the client-facing reference: WR-<year>-XXXXXXXX, ~40 bits of crypto entropy in the
// suffix. It is NOT #WTY-n: handing the public a gap-free sequence tells every submitter how many warranty
// claims the business has and lets anyone guess a neighbour's.
func mintPublicRef(now time.Time) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("warranty: generating an intake reference: %w", err)
	}
	out := make([]byte, 8)
	for i, b := range buf {
		out[i] = publicRefAlphabet[int(b)%len(publicRefAlphabet)]
	}
	return "WR-" + strconv.Itoa(now.UTC().Year()) + "-" + string(out), nil
}

// - staff-side reads of the intake evidence -

// IntakePhotos lists a claim's public-intake photos (metadata only, bytes ride the byte route). Branch-scoped
// like every other claim read.
func (s *Service) IntakePhotos(ctx context.Context, claimID string) ([]IntakePhoto, error) {
	if _, _, err := s.callerScopedClaim(ctx, claimID); err != nil {
		return nil, err
	}
	return s.store.IntakePhotosForClaim(ctx, claimID)
}

// IntakePhotoBytes reads one photo's bytes for the staff byte route. The branch gate runs FIRST: the photo is
// evidence attached to a claim, and reaching it must obey the same fence the claim does.
func (s *Service) IntakePhotoBytes(ctx context.Context, claimID, photoID string) (IntakePhoto, error) {
	if _, _, err := s.callerScopedClaim(ctx, claimID); err != nil {
		return IntakePhoto{}, err
	}
	return s.store.IntakePhoto(ctx, claimID, photoID)
}
