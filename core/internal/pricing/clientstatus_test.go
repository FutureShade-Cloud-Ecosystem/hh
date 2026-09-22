package pricing

import (
	"errors"
	"net/http"
	"testing"
)

// TestClientStatus pins pricing's error→HTTP mapping. ClientStatus is the single seam a composing
// module reuses so a pricing failure raised deep inside a larger request surfaces as the SAME
// friendly 4xx the /pricing endpoints return instead of a generic 500. The POS counter sale
// (internal/pos) uses it to keep a walk-in CASH ring whose RETAIL line has no retail anchor / no
// uom conversion factor from 500-ing the whole sale (DIB-197). An unknown/nil error must classify
// as ok=false so genuine server faults still surface as 500.
func TestClientStatus(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		ok     bool
	}{
		{"no uom conversion (retail gap)", errNoConversion, http.StatusUnprocessableEntity, true},
		{"no retail anchor", errNoListPrice, http.StatusUnprocessableEntity, true},
		{"referenced entity missing", errFKViolation, http.StatusUnprocessableEntity, true},
		{"not found", errNotFound, http.StatusNotFound, true},
		{"version conflict", errConflict, http.StatusConflict, true},
		{"bad request", badReq("qty_entered must be > 0"), http.StatusBadRequest, true},
		{"unprocessable", unprocessable("floor > target"), http.StatusUnprocessableEntity, true},
		{"unknown → not a client error (500)", errors.New("boom"), 0, false},
		{"nil → not a client error", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, msg, ok := ClientStatus(tc.err)
			if ok != tc.ok || st != tc.status {
				t.Fatalf("ClientStatus(%v) = (%d, %q, %v); want status=%d ok=%v", tc.err, st, msg, ok, tc.status, tc.ok)
			}
			if ok && msg == "" {
				t.Fatalf("a classified error must carry a client-facing message: %v", tc.err)
			}
		})
	}
}
