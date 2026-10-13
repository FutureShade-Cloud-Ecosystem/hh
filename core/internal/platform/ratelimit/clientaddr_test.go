package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func requestFrom(peer string, forwarded ...string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.RemoteAddr = peer
	for _, f := range forwarded {
		r.Header.Add("X-Forwarded-For", f)
	}
	return r
}

func TestParseTrustedProxies(t *testing.T) {
	good := []string{"", "  ", "127.0.0.1", "127.0.0.1, ::1", "10.0.0.5,10.0.0.6"}
	for _, in := range good {
		if _, err := ParseTrustedProxies(in); err != nil {
			t.Errorf("%q refused: %v", in, err)
		}
	}
	bad := []string{"nope", "10.0.0.0/8", "127.0.0.1,,::1", "127.0.0.1;::1", "256.0.0.1", "10.0.0.5:80"}
	for _, in := range bad {
		if _, err := ParseTrustedProxies(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestClientAddressTrustsTheHeaderOnlyFromAListedPeer(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.5, ::1")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		req  *http.Request
		want string
	}{
		{"listed peer, header names the client", requestFrom("10.0.0.5:4444", "203.0.113.7"), "203.0.113.7"},
		{"listed v6 peer", requestFrom("[::1]:4444", "203.0.113.8"), "203.0.113.8"},
		{"unlisted peer, header ignored", requestFrom("198.51.100.9:4444", "203.0.113.7"), "198.51.100.9"},
		{"loopback is not trusted unless listed", requestFrom("127.0.0.1:4444", "203.0.113.7"), "127.0.0.1"},
		{"listed peer, no header", requestFrom("10.0.0.5:4444"), "10.0.0.5"},
		{"listed peer, appended chain is not the proxy's overwrite", requestFrom("10.0.0.5:4444", "203.0.113.7, 198.51.100.1"), "10.0.0.5"},
		{"listed peer, two header lines", requestFrom("10.0.0.5:4444", "203.0.113.7", "203.0.113.8"), "10.0.0.5"},
		{"listed peer, junk header", requestFrom("10.0.0.5:4444", "not-an-address"), "10.0.0.5"},
		{"unsplittable peer stands as it is", requestFrom("weird"), "weird"},
	}
	for _, c := range cases {
		if got := ClientAddress(c.req, trusted); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestClientAddressWithNoTrustedList(t *testing.T) {
	if got := ClientAddress(requestFrom("127.0.0.1:1", "203.0.113.7"), nil); got != "127.0.0.1" {
		t.Fatalf("got %q", got)
	}
}
