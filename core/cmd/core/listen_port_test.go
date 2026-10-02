package main

import (
	"strings"
	"testing"
)

// HH-200: PORT is honoured when CORE_LISTEN_ADDR is unset, CORE_LISTEN_ADDR
// keeps priority when set, and a PORT that is set but not a whole number
// from 1 to 65535 is refused by name at serve boot (never silently ignored).
func TestLoadServeConfigHonoursPort(t *testing.T) {
	const unset = "<unset>"
	cases := []struct {
		name       string
		port       string // unset means the variable is absent
		listenAddr string // unset means the variable is absent
		want       string
		wantErr    bool
	}{
		{"neither set", unset, unset, ":8080", false},
		{"port 8081", "8081", unset, ":8081", false},
		{"port 1", "1", unset, ":1", false},
		{"port 65535", "65535", unset, ":65535", false},
		{"port 0 refused", "0", unset, "", true},
		{"port 70000 refused", "70000", unset, "", true},
		{"port abc refused", "abc", unset, "", true},
		{"port negative refused", "-5", unset, "", true},
		{"port empty reads as unset", "", unset, ":8080", false},
		{"port blank reads as unset", "  ", unset, ":8080", false},
		{"listen addr alone", unset, "127.0.0.1:9000", "127.0.0.1:9000", false},
		{"listen addr beats port", "8081", "127.0.0.1:9000", "127.0.0.1:9000", false},
		{"listen addr beats a junk port", "abc", "127.0.0.1:9000", "127.0.0.1:9000", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := fullEnv()
			if tc.port != unset {
				env["PORT"] = tc.port
			}
			if tc.listenAddr != unset {
				env["CORE_LISTEN_ADDR"] = tc.listenAddr
			}
			// The getenv seam is os.Getenv, which cannot tell an empty
			// value from an absent one, so an empty PORT reads as unset.
			lookup := func(k string) string { return env[k] }
			cfg, err := loadServeConfig(lookup)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("PORT %q accepted, listenAddr %q", tc.port, cfg.listenAddr)
				}
				if !strings.Contains(err.Error(), "PORT") {
					t.Fatalf("refusal does not name PORT: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if cfg.listenAddr != tc.want {
				t.Fatalf("listenAddr = %q, want %q", cfg.listenAddr, tc.want)
			}
		})
	}
}
