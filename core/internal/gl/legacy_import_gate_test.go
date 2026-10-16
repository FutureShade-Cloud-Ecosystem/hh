package gl

// The legacy import check (the BisTrack tie out route) is a migration aid for a dealer that names a
// legacy import source in its configuration. Without one the route answers a plain 404 sentence; with
// one it answers exactly as before.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

type fakeLegacySource struct {
	src string
	err error
}

func (f fakeLegacySource) LegacyImportSource(context.Context) (string, error) { return f.src, f.err }

func callLegacyCheck(h *Handler, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/gl/tieout/bistrack?"+query, nil)
	rr := httptest.NewRecorder()
	h.bistrackTieOut(rr, req, guards.StaffCaller{})
	return rr
}

func TestLegacyImportCheck_NoSourceIsA404Sentence(t *testing.T) {
	const sentence = "the legacy import check is not enabled for this dealer"
	for name, h := range map[string]*Handler{
		"no source wired": newTestHandler(newFakeStore()),
		"blank source":    newTestHandler(newFakeStore()).WithLegacyImport(fakeLegacySource{}),
	} {
		rr := callLegacyCheck(h, "year=2026&month=6")
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s: want 404, got %d (%s)", name, rr.Code, rr.Body.String())
		}
		if !strings.Contains(bodyMsg(rr.Body.Bytes()), sentence) {
			t.Fatalf("%s: want the sentence %q, got %q", name, sentence, bodyMsg(rr.Body.Bytes()))
		}
		if strings.Contains(strings.ToLower(rr.Body.String()), "bistrack") {
			t.Fatalf("%s: the refusal must stay neutral: %s", name, rr.Body.String())
		}
	}
}

func TestLegacyImportCheck_WithASourceKeepsTodaysAnswer(t *testing.T) {
	h := newTestHandler(newFakeStore()).WithLegacyImport(fakeLegacySource{src: "bistrack"})
	// A closing balance needs an explicit month: today's answer to month 0 is the service's 400.
	rr := callLegacyCheck(h, "year=2026&month=0")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("a dealer with a source keeps the service's own answer: want 400, got %d (%s)", rr.Code, rr.Body.String())
	}
	rr = callLegacyCheck(h, "year=2026&month=6")
	if rr.Code == http.StatusNotFound {
		t.Fatalf("a dealer with a source must not be refused: %s", rr.Body.String())
	}
}

func TestLegacyImportCheck_AFailedSourceReadIsNotAnAnswer(t *testing.T) {
	h := newTestHandler(newFakeStore()).WithLegacyImport(fakeLegacySource{err: errors.New("down")})
	rr := callLegacyCheck(h, "year=2026&month=6")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("a failed read must answer 500, not a guess: got %d", rr.Code)
	}
}
