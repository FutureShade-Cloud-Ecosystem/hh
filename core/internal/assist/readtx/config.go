package readtx

import "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"

// The environment names of the assist read path. All three sit under the
// HOS_ASSIST_ prefix, which a demo copy refuses (core/cmd/core/demoenv_inventory.go):
// the read path is not on any demo box yet.
const (
	// EnvReaderURL is the database URL of the staff pool; it logs in as the staff
	// assist login, never as the app role.
	EnvReaderURL = "HOS_ASSIST_READER_DATABASE_URL"
	// EnvProReaderURL is the contractor pool's URL (slice 10 uses it); optional.
	EnvProReaderURL = "HOS_ASSIST_PRO_READER_DATABASE_URL"
	// EnvClaimsKey is the HMAC signing key for the claims. The provisioning
	// oneshot writes the same key to ai.claims_key.
	EnvClaimsKey = "HOS_ASSIST_CLAIMS_KEY"
)

// Config is the read path's deployment configuration. Every field is a redact.Secret:
// a database URL carries its password.
type Config struct {
	ReaderURL    redact.Secret
	ProReaderURL redact.Secret
	ClaimsKey    redact.Secret
}

// ConfigFromEnv reads the three names through getenv.
func ConfigFromEnv(getenv func(string) string) Config {
	return Config{
		ReaderURL:    redact.NewSecret(getenv(EnvReaderURL)),
		ProReaderURL: redact.NewSecret(getenv(EnvProReaderURL)),
		ClaimsKey:    redact.NewSecret(getenv(EnvClaimsKey)),
	}
}
