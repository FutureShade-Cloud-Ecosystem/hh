package billing

import (
	"testing"
)

const (
	swUUID  = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	swToken = "P063LEAK"
)

// return credit (CREDIT_MEMO, pos_return) reads in the credit application's words, never "Credit memo".
func TestStatementDescription_AccountCreditApplicationWordsByFamily(t *testing.T) {
	for _, fam := range []struct{ txn, src string }{
		{"CREDIT_MEMO", "pos_return"}, {"ADJUSTMENT", "receipt_change"}, {"ADJUSTMENT", "deposit_cancel"},
	} {
		for _, c := range []struct{ memo, want string }{
			{"credit applied to #INV-9: " + swToken, "Credit applied"},
			{"credit applied to #INV-9", "Credit applied"},
			{"credit application reversed: " + swToken, "Credit application reversed"},
		} {
			if got := statementDescription(fam.txn, fam.src, c.memo, "", "", ""); got != c.want {
				t.Errorf("%s/%s %q reads %q, want %q", fam.txn, fam.src, c.memo, got, c.want)
			}
		}
	}
}
