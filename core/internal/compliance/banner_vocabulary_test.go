package compliance

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// banner_vocabulary_test.go, item 3b (session B). The twin of internal/lien's, for the other half of
// the same sentence.
//
// THIS CONSTANT *IS* THE BANNER, on every database in existence. `review_status` holds zero rows -
// the table was built so a banner could be a row edit rather than a redeploy, and no row has ever been
// written, so `defaultBanner` is what `ReviewStatusFor` answers with every time, and what every
// screen reading the shared endpoint has ever displayed. It used to end "(DIB-42)".
//
// The gate is per-package because Go cannot share test helpers, and because the two constants are
// genuinely two: `lien.draftBanner` is stamped into a rendered document, this one is served over the
// wire. They must agree in substance and neither may carry a code.

var workstreamCode = regexp.MustCompile(`\b(A-\d{2}|WTY-\d{2}|MSG-\d{2}|ADM-\d{2}|DIB-\d+|SWARM-[A-Z0-9-]+|W\d-[A-Z]|D-\d{3})\b`)

func TestDefaultBanner_CarriesNoWorkstreamCode(t *testing.T) {
	// Guard the guard: prove the regex still recognises what was shipping.
	if !workstreamCode.MatchString("DRAFT, statute-derived, pending client validation (DIB-42)") {
		t.Fatal("the workstream-code regex no longer matches the string this test was written for")
	}
	if strings.TrimSpace(defaultBanner) == "" {
		t.Fatal("defaultBanner is empty, every subject would answer with no banner at all")
	}

	if got := workstreamCode.FindString(defaultBanner); got != "" {
		t.Fatalf("the default review banner carries %q. It is served to every screen that asks for a "+
			"subject's review status, and on a database with no review_status rows it is the ONLY banner "+
			"anybody sees.\nbanner: %q", got, defaultBanner)
	}

	for _, must := range []string{"DRAFT", "statute-derived", "pending client validation"} {
		if !strings.Contains(defaultBanner, must) {
			t.Fatalf("the banner no longer says %q, the code came out, but so did the warning: %q", must, defaultBanner)
		}
	}
}

// 0038:103 declared the OLD text as `review_status.banner_text`'s column DEFAULT, and for part of this
// session the constant above and that default were deliberately out of step, correcting a shipped
// migration is forbidden, and the copy-hygiene item had no migration reservation. 0199 has since
// closed it: the default now matches this constant exactly.
//
// THIS TEST STAYS, and it is now guarding a closed gap rather than a known one. What it pins is the
// property that made the divergence survivable in the first place, every write names banner_text
// explicitly, because that property is what stops the NEXT stale default (in any column, in any
// migration) from reaching a row. The moment somebody adds an INSERT that omits the column, this
// says which line did it.
func TestNoWritePathCanPickUpTheStaleColumnDefault(t *testing.T) {
	src := readRepositorySource(t)
	inserts := strings.Count(src, "INSERT INTO review_status")
	if inserts == 0 {
		t.Fatal("no INSERT INTO review_status found in repository.go, this gate is reading the wrong file")
	}
	for _, stmt := range strings.Split(src, "INSERT INTO review_status")[1:] {
		head := stmt
		if i := strings.Index(head, ")"); i > 0 {
			head = head[:i]
		}
		if !strings.Contains(head, "banner_text") {
			t.Fatalf("an INSERT INTO review_status does not name banner_text, so the row would take "+
				"0038's column default, which still ends \"(DIB-42)\". Name the column, or take a "+
				"migration number and correct the default.\ncolumns: %s", strings.TrimSpace(head))
		}
	}
}

// readRepositorySource reads this package's repository.go. The relative path works because `go test`
// runs with the package directory as its working directory, the same assumption dispatch's own
// source-scan gates make, and the same one that means this file must move if repository.go does.
func readRepositorySource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("repository.go")
	if err != nil {
		t.Fatalf("cannot read repository.go: %v", err)
	}
	return string(b)
}
