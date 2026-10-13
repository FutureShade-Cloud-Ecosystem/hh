package ratelimit

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedProxies is the set of peer addresses allowed to name the client
// for a request (CORE_TRUSTED_PROXIES: a comma list of addresses, no
// ranges). The empty set trusts no peer, so the connection's own address
// always stands.
type TrustedProxies map[netip.Addr]bool

// ParseTrustedProxies reads the comma list. An empty or blank list is the
// empty set; any entry that is not a bare address, and any empty entry
// inside a non blank list, is an error naming no value.
func ParseTrustedProxies(list string) (TrustedProxies, error) {
	set := TrustedProxies{}
	if strings.TrimSpace(list) == "" {
		return set, nil
	}
	for _, raw := range strings.Split(list, ",") {
		addr, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil {
			return nil, errors.New("ratelimit: the trusted proxy list holds an entry that is not an address")
		}
		set[addr.Unmap()] = true
	}
	return set, nil
}

// ClientAddress is the address a request counts against. From a peer on
// the trusted list the X-Forwarded-For header names the client, but only
// in the shape a proxy that OVERWRITES the header writes: exactly one
// header line holding exactly one address. A chain (a proxy that appends),
// a second line or a value that is not an address is not that shape, and
// the peer's own address stands, so a client that can write the header to
// a proxy that appends gains nothing. From any other peer the header is
// ignored whatever it says. A peer address the net package cannot split is
// taken as it stands, so a peer shape the split does not expect still
// counts against something.
func ClientAddress(r *http.Request, trusted TrustedProxies) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !trusted[peer.Unmap()] {
		return host
	}
	values := r.Header.Values("X-Forwarded-For")
	if len(values) != 1 {
		return host
	}
	named, err := netip.ParseAddr(strings.TrimSpace(values[0]))
	if err != nil {
		return host
	}
	return named.Unmap().String()
}
