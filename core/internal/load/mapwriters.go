// mapwriters.go carries the two map doors' writers (HH-LOAD-1 LD-4):
// the branch map and the user map. A map records names against rows
// that already exist and creates nothing: the writer resolves the row
// the record names through the owning module's read seam, refuses when
// no row answers (naming the field, never a value), and refuses a
// record that would re-point a recorded name at a second row. It opens
// no connection and commits or rolls back nothing: the transaction it
// runs on is the frame's, its refusals come back as *Refusal, and its
// errors wrap the statement's own (%w) so a deadlock stays recognisable
// at the batch boundary.
package load

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// The map doors' registry kinds. The kind is the batch record's kind
// and the reference kind later doors name these rows by. KindUnit and
// KindUnitSet are HH-LOAD-1 LD-3's: the unit catalogue door and the
// product unit set door. KindProduct, KindProductImage and
// KindSupplierSku are HH-LOAD-1 LD-5's: the product master door (the
// kind the unit set door's items already name their product by), the
// product image door (the image name rides the product's legacy key
// under its own kind, so the image's content hash never collides with
// the master record's), and the supplier SKU door.
const (
	KindBranch       = "branch"
	KindUser         = "user"
	KindCategory     = "category"
	KindSupplier     = "supplier"
	KindUnit         = "unit"
	KindUnitSet      = "unit_set"
	KindProduct      = "product"
	KindProductImage = "product_image"
	KindSupplierSku  = "supplier_sku"
	// The customer, contact and job doors' kinds (HH-LOAD-1 LD-6).
	KindCustomer = "customer"
	KindContact  = "contact"
	KindJob      = "job"
)

// The writer level refusal codes the maps carry. A refusal names the
// field and the legacy key, never a record value.
const (
	// ReasonNoBranchRow: the record's code answers no live BRANCH row.
	ReasonNoBranchRow = "no_branch_row"
	// ReasonNoStaffRow: the record's email answers no live staff row.
	ReasonNoStaffRow = "no_staff_row"
	// ReasonWrongReference: the item carries a reference the door's
	// record shape takes none of.
	ReasonWrongReference = "wrong_reference"
)

// BranchRowSource is the branches module's read the branch map attaches
// through: one live BRANCH row's id by its yard code, inside the
// frame's transaction. The consumer declares the narrow interface it
// needs; the concrete reader is the branches package's BranchCodeReader.
type BranchRowSource interface {
	BranchRowByCodeTx(ctx context.Context, tx pgx.Tx, code string) (branchID string, ok bool, err error)
}

// StaffRowSource is the identity module's read the user map attaches
// through: one live staff row's id by its verified login email, inside
// the frame's transaction. The concrete reader is the identity package's
// StaffEmailReader.
type StaffRowSource interface {
	StaffRowByEmailTx(ctx context.Context, tx pgx.Tx, email string) (userID string, ok bool, err error)
}

// BranchMapWriter attaches the dealer's branch keys to HH's existing
// branches. The record names one field, the yard code; the writer
// resolves it and returns the row's id for the registry, writing no
// row: no branch is ever created by a map.
type BranchMapWriter struct {
	Branches BranchRowSource
}

// Apply answers one branch map item. The create arm and the update arm
// both resolve by code: on the create arm the id is the row the name
// attaches to; on the update arm a code that resolves to a row other
// than the recorded one is a re-point and refuses as a key conflict.
func (w *BranchMapWriter) Apply(ctx context.Context, tx pgx.Tx, in Write) (string, error) {
	code, _ := in.Record["code"].(string)
	if code == "" {
		return "", &Refusal{Code: "field_required", Field: "code",
			Detail: "the record names no branch code"}
	}
	if err := refuseAnyReference(in); err != nil {
		return "", err
	}
	id, ok, err := w.Branches.BranchRowByCodeTx(ctx, tx, code)
	if err != nil {
		return "", fmt.Errorf("load: branch row read: %w", err)
	}
	if !ok {
		return "", &Refusal{Code: ReasonNoBranchRow, Field: "code",
			Detail: "no branch row answers that code; branches are attached, never created"}
	}
	if in.RowID != "" && in.RowID != id {
		return "", &Refusal{Code: ReasonKeyConflict, Field: "code",
			Detail: "the legacy key is recorded against another branch row; a map never re-points"}
	}
	return id, nil
}

// UserMapWriter attaches the dealer's user and sales rep keys to
// existing staff rows by verified email. The record names one field,
// the email; no user is ever created (D38, the plan's reading 13).
type UserMapWriter struct {
	Staff StaffRowSource
}

// Apply answers one user map item, the same arms as the branch map.
func (w *UserMapWriter) Apply(ctx context.Context, tx pgx.Tx, in Write) (string, error) {
	email, _ := in.Record["email"].(string)
	if email == "" {
		return "", &Refusal{Code: "field_required", Field: "email",
			Detail: "the record names no verified email"}
	}
	if err := refuseAnyReference(in); err != nil {
		return "", err
	}
	id, ok, err := w.Staff.StaffRowByEmailTx(ctx, tx, email)
	if err != nil {
		return "", fmt.Errorf("load: staff row read: %w", err)
	}
	if !ok {
		return "", &Refusal{Code: ReasonNoStaffRow, Field: "email",
			Detail: "no staff row answers that email; users are attached, never created"}
	}
	if in.RowID != "" && in.RowID != id {
		return "", &Refusal{Code: ReasonKeyConflict, Field: "email",
			Detail: "the legacy key is recorded against another staff row; a map never re-points"}
	}
	return id, nil
}

// refuseAnyReference refuses an item that carries references for a door
// whose record shape takes none. The frame has resolved them already;
// the map ignores nothing silently.
func refuseAnyReference(in Write) error {
	if len(in.Refs) == 0 {
		return nil
	}
	return &Refusal{Code: ReasonWrongReference, Field: in.Refs[0].Kind,
		Detail: "the map record takes no references"}
}
