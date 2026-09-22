package portal

import (
	"encoding/json"
	"time"
)

// User mirrors the contractor portal's PortalUser type (Lit app, app/src/portal/) (password_hash never serialized).
type User struct {
	ID                string          `json:"id"`
	CustomerID        string          `json:"customer_id"`
	Email             string          `json:"email"`
	Name              string          `json:"name"`
	Role              string          `json:"role"`
	Status            string          `json:"status"`
	NotificationPrefs json.RawMessage `json:"notification_prefs"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	passwordHash      string          // never marshaled
}

// Invite mirrors the contractor portal's PortalInvite type. Token is the RAW token,
// populated only in the create response (D-026 deferred-email accept-link); it is
// never read back from the DB (only token_hash is stored).
type Invite struct {
	ID         string    `json:"id"`
	CustomerID string    `json:"customer_id"`
	Email      string    `json:"email"`
	Role       string    `json:"role"`
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	CreatedAt  time.Time `json:"created_at"`
}

// LoginResponse matches the contractor portal's PortalLoginResponse {token, user, config}.
type LoginResponse struct {
	Token  string `json:"token"`
	User   User   `json:"user"`
	Config Config `json:"config"`
}

// Config is the minimal branding payload the portal expects in the login response.
type Config struct {
	ID           string `json:"id"`
	DealerName   string `json:"dealer_name"`
	LogoURL      string `json:"logo_url"`
	PrimaryColor string `json:"primary_color"`
	SupportEmail string `json:"support_email"`
	SupportPhone string `json:"support_phone"`
}

// defaultConfig is the Dibbits branding stub (real config service is a later CP mission).
func defaultConfig() Config {
	return Config{
		ID:           "dibbits",
		DealerName:   "Dibbits Landscape Supply",
		PrimaryColor: "#E8A74E",
		SupportEmail: "support@dibbits.example",
	}
}

// NewPortalContact is the CRM signal emitted to the events outbox on activation
// (topic crm.NewPortalContact; no consumer yet, D-026).
type NewPortalContact struct {
	CustomerID string `json:"customer_id"`
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Role       string `json:"role"`
}

const TopicNewPortalContact = "crm.NewPortalContact"
