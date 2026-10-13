package exchange

import (
	"errors"
	"net/http"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/ratelimit"
)

// MethodsAddressAllowance is the per address allowance of the public methods
// route per LimitWindow: a sign in screen asks once per load, so this is
// generous for a depot behind one address and still bounds a prober.
const MethodsAddressAllowance = 60

// MethodsHandler is GET /api/v1/auth/methods?org=<slug>: the sign in screen
// asks which methods the dealer allows. Public leg, no bearer, the codes
// only. It is no directory oracle: a malformed slug, an unknown org and an
// org holding the default set all answer the same bytes (status, headers and
// body), and the slug shape is checked before anything is looked up, so no
// request is answered 400 for its shape.
type MethodsHandler struct {
	cfg  MethodsConfig
	addr *ratelimit.AddressLimit
}

// MethodsConfig is the constructor input; Orgs and Methods are required.
type MethodsConfig struct {
	Orgs    Orgs
	Methods SignInMethods
	// Trusted names the proxies allowed to speak for a client address.
	Trusted ratelimit.TrustedProxies
	// AddressLimit selects the allowance; nil picks MethodsAddressAllowance.
	AddressLimit *ratelimit.AddressLimit
	Now          func() time.Time
}

// NewMethodsHandler validates the collaborators.
func NewMethodsHandler(c MethodsConfig) (*MethodsHandler, error) {
	switch {
	case c.Orgs == nil:
		return nil, errors.New("methods: the org resolver is required")
	case c.Methods == nil:
		return nil, errors.New("methods: the sign in method reader is required")
	}
	h := &MethodsHandler{cfg: c, addr: c.AddressLimit}
	if h.addr == nil {
		h.addr = ratelimit.NewAddressLimit(MethodsAddressAllowance, LimitWindow, c.Now)
	}
	return h, nil
}

type methodsResponse struct {
	Methods []string `json:"methods"`
}

// ServeHTTP answers. Every answer for a slug that is not a known org with its
// own row is the default body.
func (h *MethodsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.addr.Allow(ratelimit.ClientAddress(r, h.cfg.Trusted)) {
		httpx.Error(w, http.StatusTooManyRequests, "too many requests, wait a moment and try again")
		return
	}
	slug := r.URL.Query().Get("org")
	if !authz.ValidOrgSlug(slug) {
		h.write(w, identity.DefaultSignInMethods())
		return
	}
	orgID, ok, err := h.cfg.Orgs.ResolveSlug(r.Context(), slug)
	if err != nil {
		authz.RefuseUnavailable(w)
		return
	}
	if !ok {
		h.write(w, identity.DefaultSignInMethods())
		return
	}
	codes, err := h.cfg.Methods.AllowedSignInMethods(r.Context(), orgID)
	if err != nil {
		authz.RefuseUnavailable(w)
		return
	}
	h.write(w, codes)
}

func (h *MethodsHandler) write(w http.ResponseWriter, codes []string) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, methodsResponse{Methods: codes})
}
