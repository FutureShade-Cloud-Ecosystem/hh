package portal

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// passwordResetTTL bounds how long a reset link is valid. Far shorter than the 7-day
// invite TTL (a reset is a high-value, time-sensitive credential).
const passwordResetTTL = 1 * time.Hour

// registerPasswordResetRoutes wires the PUBLIC password-reset pair (no auth). Both must
// be enumeration-safe and rate-limited (M2.3): request ALWAYS returns 202 whether or not
// the email exists, and confirm consumes a single-use, time-boxed, sha256-hashed token -
// reusing the invite token primitives (newInviteToken/hashToken in auth.go) over the new
// portal_password_resets table (0027). A successful reset must revoke existing sessions.
func (h *Handler) registerPasswordResetRoutes(r httpx.Registrar) {
	r.Handle("POST /api/portal/v1/password-reset/request", http.HandlerFunc(h.requestPasswordReset))
	r.Handle("POST /api/portal/v1/password-reset/confirm", http.HandlerFunc(h.confirmPasswordReset))
}

// requestPasswordReset issues a reset token for the given email. It ALWAYS answers 202 with
// an identical body, whether or not the email exists, and even on an internal error, so
// the response can never enumerate accounts. The raw token (created only for a real user)
// is delivered out-of-band by the email seam (F0.2/D-026); it is deliberately NOT echoed.
func (h *Handler) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if _, err := h.svc.RequestPasswordReset(r.Context(), req.Email); err != nil {
		// Swallow: a DB error must not change the response shape (no enumeration).
		h.log.Error("portal: password reset request", "err", err)
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

// confirmPasswordReset consumes a reset token and sets the new password.
func (h *Handler) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := h.svc.ConfirmPasswordReset(r.Context(), req.Token, req.NewPassword); err != nil {
		h.writeAccountErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
