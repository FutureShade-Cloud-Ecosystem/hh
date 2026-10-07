package load

// receivables.go is the open receivables door (HH-LOAD-1 LD-11, CP0-6
// answer B): each open item of the dealer's AR extract lands as an
// imported invoice or credit memo through billing's own writer, inside
// the frame's transaction, under the money doors' all-or-nothing batch
// protocol (door_money.go). The door's own work is the boundary: the
// record's shape and its money (integer cents, exact, never a float),
// the references the invoice shape needs (the customer and the branch,
// named by legacy key), and the ledger's date order rule, which the
// door enforces by writing each customer's items oldest first so the
// receivables ledger's running balances read in date order (the plan's
// LD-11: one ledger row per item, in date order per customer, so aging
// survives).
//
// The source job and address keys ride on the record as legacy key
// references (source_job_key, source_address_key) until their homes
// exist next cycle; they are never resolved against the registry,
// because the registry holds no job or address names yet.
//
// The record is closed: the seven fields below are the whole of it, and
// a record naming anything else refuses, so two records that differ
// anywhere the door cannot land also cannot hash alike and answer
// unchanged.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ReceivablesKind is the door's registry kind and the batch record's
// kind: one name per open item.
const ReceivablesKind = "receivable"

// The reference kinds the invoice shape resolves. The customer and
// branch doors own the names; this door resolves them through the
// registry like every reference.
const (
	RefKindCustomer = "customer"
	RefKindBranch   = "branch"
)

// The record's field names, the contract's LoadReceivablesRecord.
const (
	receivablesFieldDocNo   = "source_doc_no"
	receivablesFieldIssue   = "issue_date"
	receivablesFieldDue     = "due_date"
	receivablesFieldAmount  = "open_amount_cents"
	receivablesFieldJobKey  = "source_job_key"
	receivablesFieldAddrKey = "source_address_key"
)

// receivablesDateLayout is the one date shape the record accepts: a
// strict calendar day. A due date with a time on it, a loosely padded
// day or an impossible one refuses the batch at the boundary.
const receivablesDateLayout = "2006-01-02"

// receivablesMaxDocNo and receivablesMaxCarriedKey bound the record's
// text, the same bounds the contract carries.
const (
	receivablesMaxDocNo      = 128
	receivablesMaxCarriedKey = MaxKeyLength
)

// ValidateReceivablesItems grades every item's record at the boundary:
// money as exact cents (an integer, a json.Number with no fraction and
// no exponent, never a float), the dates as strict days, the document
// number present and bounded, the carried job and address keys bounded,
// the references exactly the customer and the branch, and each legacy
// id once per batch. The first offending item answers as an *ItemError
// naming it, the field and the reason; the batch never reaches the
// store.
func ValidateReceivablesItems(items []Item) error {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if item.LegacyID == "" {
			return &ItemError{LegacyID: item.LegacyID, Code: ReasonRecordShape, Field: "legacy_id",
				Detail: "the item names no legacy id"}
		}
		if seen[item.LegacyID] {
			return &ItemError{LegacyID: item.LegacyID, Code: ReasonRecordShape, Field: "legacy_id",
				Detail: fmt.Sprintf("the batch names legacy key %s twice: a money batch names each open item once", item.LegacyID)}
		}
		seen[item.LegacyID] = true
		if err := validateReceivablesRecord(item); err != nil {
			return err
		}
		if err := validateReceivablesRefs(item); err != nil {
			return err
		}
	}
	return nil
}

// validateReceivablesRecord grades one item's record.
func validateReceivablesRecord(item Item) error {
	bad := func(field, detail string) *ItemError {
		return &ItemError{LegacyID: item.LegacyID, Code: ReasonRecordShape, Field: field, Detail: detail}
	}
	rec := item.Record
	if rec == nil {
		return bad("record", "the item carries no record")
	}
	for key := range rec {
		switch key {
		case receivablesFieldDocNo, receivablesFieldIssue, receivablesFieldDue, receivablesFieldAmount,
			receivablesFieldJobKey, receivablesFieldAddrKey:
		default:
			return bad(key, fmt.Sprintf("the record names a field the door does not carry: %s", key))
		}
	}
	docNo, _ := rec[receivablesFieldDocNo].(string)
	if docNo == "" || len(docNo) > receivablesMaxDocNo {
		return bad(receivablesFieldDocNo, "the source document number is missing or over its bound")
	}
	for _, field := range []string{receivablesFieldIssue, receivablesFieldDue} {
		v, _ := rec[field].(string)
		if _, err := time.Parse(receivablesDateLayout, v); err != nil {
			return bad(field, "the date is missing or not a strict calendar day")
		}
	}
	if _, err := receivablesMoney(rec, item.LegacyID); err != nil {
		return err
	}
	for _, field := range []string{receivablesFieldJobKey, receivablesFieldAddrKey} {
		if v, ok := rec[field]; ok {
			s, _ := v.(string)
			if len(s) > receivablesMaxCarriedKey {
				return bad(field, "the carried legacy key is over its bound")
			}
		}
	}
	return nil
}

// validateReceivablesRefs grades one item's references: the customer
// and the branch by legacy key, one of each, nothing else. A job or
// address name has no registry home yet and rides on the record.
func validateReceivablesRefs(item Item) error {
	var haveCustomer, haveBranch bool
	for _, r := range item.Requires {
		switch r.Kind {
		case RefKindCustomer:
			if haveCustomer {
				return &ItemError{LegacyID: item.LegacyID, Code: ReasonRecordShape, Field: "requires",
					Detail: "the item names more than one customer"}
			}
			haveCustomer = true
		case RefKindBranch:
			if haveBranch {
				return &ItemError{LegacyID: item.LegacyID, Code: ReasonRecordShape, Field: "requires",
					Detail: "the item names more than one branch"}
			}
			haveBranch = true
		default:
			return &ItemError{LegacyID: item.LegacyID, Code: ReasonRecordShape, Field: "requires",
				Detail: fmt.Sprintf("the item names a reference kind the door does not resolve: %s", r.Kind)}
		}
	}
	if !haveCustomer || !haveBranch {
		return &ItemError{LegacyID: item.LegacyID, Code: ReasonRecordShape, Field: "requires",
			Detail: "the item must name one customer and one branch by legacy key"}
	}
	return nil
}

// receivablesMoney reads the open amount as exact whole cents. The
// accepted spellings are the wire's number kept literal (json.Number
// with an exact integer value: no fraction, no exponent) and an integer
// an in-process caller hands over. A float64 always refuses, whatever
// it holds: money that has been through a float is money somebody will
// argue about (AGENTS.md). Zero refuses: an open item of nothing is not
// an open item.
func receivablesMoney(rec map[string]any, legacyID string) (int64, error) {
	v, ok := rec[receivablesFieldAmount]
	if !ok || v == nil {
		return 0, &ItemError{LegacyID: legacyID, Code: ReasonRecordShape, Field: receivablesFieldAmount,
			Detail: "the open amount is missing"}
	}
	var cents int64
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, &ItemError{LegacyID: legacyID, Code: ReasonRecordShape, Field: receivablesFieldAmount,
				Detail: "the open amount is not exact whole cents"}
		}
		cents = i
	case int:
		cents = int64(n)
	case int64:
		cents = n
	default:
		return 0, &ItemError{LegacyID: legacyID, Code: ReasonRecordShape, Field: receivablesFieldAmount,
			Detail: "the open amount is not an integer: money is whole cents, never a float or text"}
	}
	if cents == 0 {
		return 0, &ItemError{LegacyID: legacyID, Code: ReasonRecordShape, Field: receivablesFieldAmount,
			Detail: "the open amount is zero: an open item of nothing is not an open item"}
	}
	return cents, nil
}

// orderReceivablesItems orders the batch for the ledger: grouped by the
// customer's legacy key, then by issue date, then by legacy id, so each
// customer's rows write oldest first and the running balances read in
// date order whatever order the wire carried. The second return maps
// each legacy id to its wire position, so the outcomes answer in the
// caller's order. The batch has already passed validation, so every
// item carries exactly one customer reference and a strict issue date.
func orderReceivablesItems(items []Item) ([]Item, map[string]int) {
	wire := make(map[string]int, len(items))
	key := func(it Item) (string, string, string) {
		customer := ""
		for _, r := range it.Requires {
			if r.Kind == RefKindCustomer {
				customer = r.LegacyID
				break
			}
		}
		issue, _ := it.Record[receivablesFieldIssue].(string)
		return customer, issue, it.LegacyID
	}
	ordered := make([]Item, len(items))
	for i, it := range items {
		wire[it.LegacyID] = i
		ordered[i] = it
	}
	sort.SliceStable(ordered, func(a, b int) bool {
		ca, ia, la := key(ordered[a])
		cb, ib, lb := key(ordered[b])
		if ca != cb {
			return ca < cb
		}
		if ia != ib {
			return ia < ib
		}
		return la < lb
	})
	return ordered, wire
}

// ReceivablesDoor is the open receivables door: the frame's money batch
// protocol bound to billing's import writer, with the door's own
// boundary rules in front (the record's shape and money) and the
// ledger's date order rule between the boundary and the batch.
type ReceivablesDoor struct {
	door  *Door
	order func([]Item) ([]Item, map[string]int)
	valid func([]Item) error
}

// NewReceivablesDoor binds the door: the store and audit sink the frame
// owns, and the owning module's writer for the invoices and AR
// subledger rows (billing's, handed over by the wiring).
func NewReceivablesDoor(store *Store, w Writer, a BatchAuditSink) *ReceivablesDoor {
	return &ReceivablesDoor{
		door:  NewDoor(store, ReceivablesKind, w, a),
		order: orderReceivablesItems,
		valid: ValidateReceivablesItems,
	}
}

// UpsertReceivables runs one receivables batch: the boundary's record
// rules first (a bad record answers an *ItemError and nothing runs),
// then the items reordered for the ledger, then the money batch
// protocol, then the outcomes mapped back to the wire's order so every
// item's answer stands beside its submission.
func (d *ReceivablesDoor) UpsertReceivables(ctx context.Context, in Batch) (BatchResult, error) {
	if err := d.valid(in.Items); err != nil {
		return BatchResult{}, err
	}
	ordered, wire := d.order(in.Items)
	run, err := d.door.UpsertBatchAtomic(ctx, Batch{
		OrgID: in.OrgID, SourceSystem: in.SourceSystem, BatchID: in.BatchID, Caller: in.Caller, Items: ordered,
	})
	if err != nil {
		return BatchResult{}, err
	}
	out := BatchResult{BatchID: run.BatchID, Counts: run.Counts, Items: make([]Outcome, len(run.Items))}
	for _, o := range run.Items {
		out.Items[wire[o.LegacyID]] = o
	}
	return out, nil
}
