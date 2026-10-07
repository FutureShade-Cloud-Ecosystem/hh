//go:build integration

package pos

// returns_lookup_refs_integration_test.go: the Returns lookup finds a sale however the clerk types
// it (round 11). Order numbers are stored with a hash (#SO-TRT-21) and invoice numbers likewise
// (#INV-17); the clerk reads "SO-TRT-21", "INV-17" or just "21" off a receipt. The lookup accepts
// each, with or without the hash, in any case, and the lines it answers carry the product's name
// (a POS ring stores no description of its own). Run like the Trenton exit test: migrated and
// seeded database, `go test -tags integration ./internal/pos/ -run TestIntegration_ReturnsLookupRefs`.

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestIntegration_ReturnsLookupRefs_AnySpellingOfTheNumberFindsTheSale(t *testing.T) {
	s := newTrentonStack(t)
	sale := s.ringSale(uuid.NewString(), trentonBranchID, s.trentonTill, "trenton", []float64{4}, TenderCash)

	var invoiceNo string
	if err := s.pool.QueryRow(context.Background(), `SELECT invoice_no FROM invoices WHERE order_id::text = $1`, sale.OrderID).Scan(&invoiceNo); err != nil {
		t.Fatalf("the sale's invoice number: %v", err)
	}
	trail := regexp.MustCompile(`(\d+)$`).FindString(sale.OrderNo)
	if trail == "" {
		t.Fatalf("the order number %q ends in no digits", sale.OrderNo)
	}
	bare := strings.TrimPrefix(sale.OrderNo, "#")
	invBare := strings.TrimPrefix(invoiceNo, "#")

	found := func(ref string) map[string]any {
		t.Helper()
		rec := s.do(http.MethodGet, "/api/v1/pos/returns/lookup?order_no="+url.QueryEscape(ref), s.bearer(trentonClerkSub), "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q must find the sale, got %d: %s", ref, rec.Code, rec.Body.String())
		}
		lk := s.decodeBody(rec)
		if lk["order_id"] != sale.OrderID {
			t.Fatalf("%q found another sale: %v", ref, lk["order_id"])
		}
		return lk
	}
	for _, ref := range []string{
		sale.OrderNo,          // as stored, with the hash
		bare,                  // without the hash
		strings.ToLower(bare), // any case
		"  " + bare + " ",     // stray spaces
		invoiceNo, invBare,    // the invoice number, with and without the hash
		strings.ToLower(invBare), // and in lower case
	} {
		found(ref)
	}

	// The product's name rides each returnable line, never an empty description.
	lk := found(bare)
	lines, _ := lk["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("the sale has one line: %v", lk)
	}
	desc, _ := lines[0].(map[string]any)["description"].(string)
	var name string
	if err := s.pool.QueryRow(context.Background(), `SELECT name FROM products WHERE id::text = $1`, l2cProductID).Scan(&name); err != nil {
		t.Fatalf("the product's name: %v", err)
	}
	if desc != name {
		t.Fatalf("the returned line reads %q, want the product's name %q", desc, name)
	}

	// A wrong number still says no sale answers, in every spelling.
	for _, ref := range []string{"SO-TRT-99999999", "#SO-TRT-99999999", "INV-99999999", "99999999"} {
		rec := s.do(http.MethodGet, "/api/v1/pos/returns/lookup?order_no="+url.QueryEscape(ref), s.bearer(trentonClerkSub), "", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%q must find no sale, got %d: %s", ref, rec.Code, rec.Body.String())
		}
	}

	// The bare trailing number finds the sale too (the clerk reads only the last digits off the slip).
	found(trail)
}

// PR 255 review P2: branch counters are per branch, so the same trailing number in two branches is
// the normal case. A bare number must find the caller's OWN sale even when another branch has a newer
// sale ending in the same digits; a full foreign number still answers with the wall's refusal.
func TestIntegration_ReturnsLookupRefs_ABareNumberFindsTheCallersOwnSaleNotANewerOtherBranchOne(t *testing.T) {
	s := newTrentonStack(t)
	trenton := s.ringSale(uuid.NewString(), trentonBranchID, s.trentonTill, "trenton", []float64{3}, TenderCash)
	kingston := s.ringSale(uuid.NewString(), kingstonBranchID, s.kingstonTill, "kingston", []float64{3}, TenderCash)
	trail := regexp.MustCompile(`(\d+)$`).FindString(trenton.OrderNo)
	// The newer Kingston sale ends in the very same digits as the Trenton one.
	if _, err := s.pool.Exec(context.Background(), `UPDATE orders SET order_no = $1 WHERE id::text = $2`, "#SO-KIN-"+trail, kingston.OrderID); err != nil {
		t.Fatalf("number the Kingston sale like the Trenton one: %v", err)
	}
	look := func(sub, ref string) (int, map[string]any) {
		rec := s.do(http.MethodGet, "/api/v1/pos/returns/lookup?order_no="+url.QueryEscape(ref), s.bearer(sub), "", nil)
		return rec.Code, s.decodeBody(rec)
	}
	// The Trenton clerk typing the digits gets the Trenton sale, the Kingston clerk the Kingston one.
	if code, lk := look(trentonClerkSub, trail); code != http.StatusOK || lk["order_id"] != trenton.OrderID {
		t.Fatalf("the Trenton clerk's bare number must find the Trenton sale, got %d %v", code, lk)
	}
	if code, lk := look(kingstonClerkSub, trail); code != http.StatusOK || lk["order_id"] != kingston.OrderID {
		t.Fatalf("the Kingston clerk's bare number must find the Kingston sale, got %d %v", code, lk)
	}
	// A number that only another branch holds is not found by bare digits (no oracle on a foreign
	// branch), and the full foreign number still meets the wall.
	onlyKingston := "7" + trail + "7"
	if _, err := s.pool.Exec(context.Background(), `UPDATE orders SET order_no = $1 WHERE id::text = $2`, "#SO-KIN-"+onlyKingston, kingston.OrderID); err != nil {
		t.Fatalf("renumber: %v", err)
	}
	if code, _ := look(trentonClerkSub, onlyKingston); code != http.StatusNotFound {
		t.Fatalf("a bare number only another branch holds must read as no sale, got %d", code)
	}
	if code, _ := look(trentonClerkSub, "SO-KIN-"+onlyKingston); code != http.StatusForbidden {
		t.Fatalf("the full foreign number still meets the wall, got %d", code)
	}
}

// PR 255 review P3: a commit whose order_id and order_no name different sales is refused outright.
func TestIntegration_ReturnsLookupRefs_ACommitWithTwoDifferentSalesIsRefused(t *testing.T) {
	s := newTrentonStack(t)
	own := s.ringSale(uuid.NewString(), trentonBranchID, s.trentonTill, "trenton", []float64{6}, TenderCash)
	other := s.ringSale(uuid.NewString(), trentonBranchID, s.trentonTill, "trenton", []float64{6}, TenderCash)
	body := s.returnBody(uuid.NewString(), own, 0, RefundCash, 1)
	body["order_no"] = other.OrderNo
	rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("an order_id and an order_no naming different sales must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	// The agreeing pair commits.
	ok := s.returnBody(uuid.NewString(), own, 0, RefundCash, 1)
	ok["order_no"] = own.OrderNo
	if rec := s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", ok); rec.Code != http.StatusCreated {
		t.Fatalf("a consistent pair must commit, got %d: %s", rec.Code, rec.Body.String())
	}
}
