package pricing

// pricefile_handler.go, the four routes behind the price-file import screen.
//
// - WHY PREVIEW IS BEHIND THE *READ* GUARD -
//
// It is a POST that uploads a file, which reads like a write, and it is not one: PreviewPriceFile
// touches no table. Putting it behind writeGuard would have been the reflex, and it would have made
// the acceptance clause unfalsifiable, "a clerk role sees no cost bytes anywhere in the import
// payloads" is a claim about a payload a clerk can actually receive, and a manager-only endpoint has
// no clerk payload to inspect. The gate that matters here is not who may LOOK at a diff; it is who
// may commit one, and that is apply, which is Manager+ like every other pricing write.
//
// So: preview and the two lists are Sales+ (readGuard), apply is Manager+ (writeGuard), and the cost
// columns inside every one of those responses are gated a second time, per caller, at the marshal
// layer. Two independent gates on two different questions.
//
// - WHY MULTIPART AND NOT A RAW BODY -
//
// A-08's import endpoint takes the CSV as the raw request body with the filename in a query string.
// That works for one file and one profile and stops working the moment a request has to carry a file
// AND the column mapping AND the digest of the preview being committed, the mapping would become
// URL-encoded JSON in a query parameter, which is a data structure hiding in a string. multipart is
// stdlib, it is what a browser's own <form> produces from a file input, and it keeps the four fields
// four fields. internal/feedback already takes an upload this way.

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxPriceFileUpload bounds the whole multipart envelope: the file cap plus headroom for the
// mapping, the digest and the framing. Enforced by MaxBytesReader BEFORE the body is buffered, so an
// oversized upload is refused rather than read into memory and then rejected.
const maxPriceFileUpload = (12 << 20) + (256 << 10)

func (h *Handler) registerPriceFileRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/pricing/price-file/vendors", g.RequireStaff(h.staff(h.priceFileVendors)))
	r.Handle("POST /api/v1/pricing/price-file/preview", g.RequireStaff(h.staff(h.previewPriceFile)))
	r.Handle("POST /api/v1/pricing/price-file/apply", g.RequireManager(h.staff(h.applyPriceFile)))
	r.Handle("GET /api/v1/pricing/price-file/runs", g.RequireStaff(h.staff(h.priceFileRuns)))
}

func (h *Handler) priceFileVendors(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.PriceFileVendors(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if out == nil {
		out = []PriceFileVendor{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) priceFileRuns(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	vendorID := r.URL.Query().Get("vendor_id")
	if vendorID != "" && !uuidRe.MatchString(vendorID) {
		httpx.Error(w, http.StatusBadRequest, "vendor_id must be a uuid")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := h.svc.PriceImportRuns(r.Context(), vendorID, limit)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) previewPriceFile(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	in, ok := h.readPriceFileForm(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	in.Limit = limit
	out, err := h.svc.PreviewPriceFile(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) applyPriceFile(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	in, ok := h.readPriceFileForm(w, r)
	if !ok {
		return
	}
	out, err := h.svc.ApplyPriceFile(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

// readPriceFileForm parses the shared multipart envelope. One reader for both routes so preview and
// apply cannot disagree about what a request looks like, which is the same reason PriceFileInput is
// one struct.
func (h *Handler) readPriceFileForm(w http.ResponseWriter, r *http.Request) (PriceFileInput, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPriceFileUpload)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		// The one message covers a body that is not multipart, one that is malformed, and one that is
		// too large. Distinguishing them would tell an operator which of three things to do about a
		// file picker they did not use, the browser form cannot produce the first two.
		httpx.Error(w, http.StatusBadRequest, "attach a price file to import")
		return PriceFileInput{}, false
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	vendorID := strings.TrimSpace(r.FormValue("vendor_id"))
	if !uuidRe.MatchString(vendorID) {
		httpx.Error(w, http.StatusBadRequest, "vendor_id must be a uuid")
		return PriceFileInput{}, false
	}
	mapping, err := decodeMappingJSON(r.FormValue("mapping"))
	if err != nil {
		h.writeErr(w, r, err)
		return PriceFileInput{}, false
	}

	file, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "attach a price file to import")
		return PriceFileInput{}, false
	}
	defer file.Close()
	// LimitReader at cap+1, so a file that exactly fills the cap is not mistaken for one that
	// overflowed it, and a file that DOES overflow is refused rather than truncated. A price list
	// silently missing its tail is the worst failure available here: the preview would look clean,
	// the apply would stage a partial file, and no screen would say so.
	data, err := io.ReadAll(io.LimitReader(file, priceFileMaxBytes+1))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "the uploaded file could not be read")
		return PriceFileInput{}, false
	}
	if len(data) > priceFileMaxBytes {
		httpx.Error(w, http.StatusBadRequest, "the uploaded file is larger than this import accepts")
		return PriceFileInput{}, false
	}

	in := PriceFileInput{
		VendorID: vendorID,
		UserID:   staffUserID(r),
		Data:     data,
		Mapping:  mapping,
		Digest:   strings.TrimSpace(r.FormValue("digest")),
	}
	if hdr != nil {
		in.FileName = hdr.Filename
	}
	if d := strings.TrimSpace(r.FormValue("effective_date")); d != "" {
		t, perr := time.Parse("2006-01-02", d)
		if perr != nil {
			httpx.Error(w, http.StatusBadRequest, "effective_date must be YYYY-MM-DD")
			return PriceFileInput{}, false
		}
		in.EffectiveDate = &t
	}
	return in, true
}
