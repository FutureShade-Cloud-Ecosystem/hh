package readtx

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
	"strconv"
	"time"
)

// DefaultClaimsTTL is how long a signed claim stays valid. One tool call is one
// short transaction; the expiry only bounds a leaked token.
const DefaultClaimsTTL = 30 * time.Second

// minKeyLen is the shortest signing key accepted, the same floor the claims_key
// table's CHECK holds.
const minKeyLen = 32

// Claims is the payload of plan 3.2. It is built from an already resolved
// caller by the assist package (Caller.ReadClaims), never from request input.
// The field names are the keys of ai.claim_set in migration 0418.
type Claims struct {
	Audience string `json:"audience"`
	// Tier is the asker's identity tier (1 leadership, 2 manager, 3 frontline). The row policies require it
	// to be within a view family's floor, so a tier the Go side never signs is still refused by the database.
	// A claim without one verifies and reads as no tier: every floor refuses it.
	Tier          int      `json:"tier"`
	AskerID       string   `json:"asker_id"`
	CustomerID    string   `json:"customer_id,omitempty"`
	Global        bool     `json:"global"`
	BranchIDs     []string `json:"branch_ids"`
	CostBranchIDs []string `json:"cost_branch_ids"`
	// POCostBranchIDs are the branches where the asker may see what the dealer pays on a purchase order,
	// decided by the per branch purchasing policy (the second vocabulary), not by the cost and margin role
	// list. An old claim without the field reads as no purchase cost anywhere.
	POCostBranchIDs []string `json:"po_cost_branch_ids"`
	Margin          bool     `json:"margin"`
	PaymentTrail    bool     `json:"payment_trail"`
	PIIClasses      []string `json:"pii_classes"`
	Capabilities    []string `json:"capabilities"`
	ChannelIDs      []string `json:"channel_ids"`

	// Set by the signer, never by a caller.
	PID   int    `json:"pid"`
	Exp   int64  `json:"exp"`
	Nonce string `json:"nonce"`
}

// Audiences.
const (
	AudienceStaff = "staff"
	AudiencePro   = "pro"
)

// Signer signs claims with the deployment's HMAC key.
type Signer struct {
	keyID int32
	key   redact.Secret
	ttl   time.Duration
	now   func() time.Time
}

// NewSigner refuses an empty or short key.
func NewSigner(key redact.Secret) (*Signer, error) {
	if len(key.Reveal()) < minKeyLen {
		return nil, errors.New("readtx: the claims signing key is missing or shorter than 32 characters")
	}
	return &Signer{keyID: KeyID(key), key: key, ttl: DefaultClaimsTTL, now: time.Now}, nil
}

// KeyID derives the id a key is stored and signed under from the key itself: the
// first four bytes of its SHA 256 with the top bit cleared. The provisioning
// oneshot uses the same function, so Go and the table agree without an extra
// setting, and writing the same key twice is the same row.
func KeyID(key redact.Secret) int32 {
	sum := sha256.Sum256([]byte(key.Reveal()))
	return int32(binary.BigEndian.Uint32(sum[:4]) & 0x7fffffff)
}

// Sign binds c to the backend pid of the transaction it will be read in, sets
// the expiry and a nonce, and returns the signed token. The token is returned as
// a redact.Secret so that no log line or error can carry it.
func (s *Signer) Sign(c Claims, pid int) (redact.Secret, error) {
	if c.Audience != AudienceStaff && c.Audience != AudiencePro {
		return redact.Secret{}, errors.New("readtx: claims audience must be staff or pro")
	}
	if c.AskerID == "" {
		return redact.Secret{}, errors.New("readtx: claims need an asker")
	}
	if pid <= 0 {
		return redact.Secret{}, errors.New("readtx: claims need a backend pid")
	}
	c.PID = pid
	c.Exp = s.now().Add(s.ttl).Unix()
	var n [12]byte
	if _, err := rand.Read(n[:]); err != nil {
		return redact.Secret{}, fmt.Errorf("readtx: nonce: %w", err)
	}
	c.Nonce = hex.EncodeToString(n[:])
	for _, l := range []*[]string{&c.BranchIDs, &c.CostBranchIDs, &c.POCostBranchIDs, &c.PIIClasses, &c.Capabilities, &c.ChannelIDs} {
		if *l == nil {
			*l = []string{}
		}
	}
	body, err := json.Marshal(c)
	if err != nil {
		return redact.Secret{}, errors.New("readtx: claims did not marshal")
	}
	signed := "v1." + strconv.Itoa(int(s.keyID)) + "." + base64.StdEncoding.EncodeToString(body)
	m := hmac.New(sha256.New, []byte(s.key.Reveal()))
	m.Write([]byte(signed))
	return redact.NewSecret(signed + "." + hex.EncodeToString(m.Sum(nil))), nil
}
