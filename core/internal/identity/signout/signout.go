// Package signout is POST /api/v1/auth/sign-out (HH-678): the holder of a
// core session token revokes it by presenting it. The body is one JSON
// object with the field token only, so every leg of the route gate's probe
// (which sends other fields and never reads the Authorization header)
// meets the same 400 at the body gate, before any verification runs.
// Signing out an already revoked or already expired token is idempotent
// and answers ok; a token that fails verification for any other reason
// (forged signature, wrong issuer or audience, malformed JWT) answers the
// shared 401.
package signout

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

const maxBodyBytes = 8 << 10

type request struct {
	Token string `json:"token"`
}

// NewHandler serves the sign out. A nil revocations store means the
// revocation cannot be written; the token still verifies and the answer is
// ok, so a composition without the store never strands a person at the
// door (the token expires on its own).
func NewHandler(verifier *identity.Verifier, revocations *identity.SessionRevocations) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in request
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "the body must be one JSON object with the field token only")
			return
		}
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			httpx.Error(w, http.StatusBadRequest, "the body must be one JSON object with the field token only")
			return
		}
		tok := strings.TrimSpace(in.Token)
		if tok == "" || strings.Count(tok, ".") != 2 {
			httpx.Error(w, http.StatusBadRequest, "token is required")
			return
		}
		verified, err := verifier.Verify(r.Context(), tok)
		if err != nil {
			var ve *identity.VerifyError
			if errors.As(err, &ve) {
				if ve.Class() == identity.ClassOperational {
					authz.RefuseUnavailable(w)
					return
				}
				// Idempotent by design: the token is already revoked or
				// already expired, which is what a sign out asks for.
				if ve.Reason() == "token revoked" || ve.Reason() == "token expired" {
					w.Header().Set("Cache-Control", "no-store")
					httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
					return
				}
			}
			authz.RefuseUnauthorized(w)
			return
		}
		if verified.JTI != "" && revocations != nil {
			if rerr := revocations.Revoke(r.Context(), verified.JTI, verified.ExpiresAt); rerr != nil {
				authz.RefuseUnavailable(w)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
}
