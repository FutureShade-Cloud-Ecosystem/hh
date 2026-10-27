package dashboard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// FROM-PRINCIPAL 174, PR 608 round 7: Manager Home's landing asks
// /api/v1/dashboard/viz for a whole tile list at once, and the handler runs
// every id through ParseComposition before anything else. An id the caller may
// not compose is byte absent from the answer, so its card can only ever say
// "Not connected yet" no matter what the client code hopes for. This leg pins
// the contract from the other side: the landing's WHOLE tile list, read out of
// the client source itself, must parse clean. A client tile nobody can compose
// fails here, so a faked tile cannot come back unnoticed.
func TestLandingTileListAllParse(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "apps", "manager-home", "src", "manager-api.ts"))
	if err != nil {
		t.Fatalf("read the landing client source: %v", err)
	}
	list := regexp.MustCompile(`(?s)const LANDING_VIZ_TILES = \[(.*?)\] as const;`).FindSubmatch(b)
	if list == nil {
		t.Fatal("no LANDING_VIZ_TILES literal in web/apps/manager-home/src/manager-api.ts; the list moved, point this leg at it")
	}
	ids := regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(list[1], -1)
	if len(ids) == 0 {
		t.Fatal("LANDING_VIZ_TILES holds no tile ids")
	}
	for _, id := range ids {
		tile := string(id[1])
		if _, ok := ParseComposition(tile); ok {
			continue
		}
		msg := "ParseComposition refuses it"
		for metricID, m := range MetricByID {
			if strings.HasPrefix(tile, VizPrefix+metricID+"-") {
				msg = "the metric composes dimensions " + quoteList(m.Dimensions) + " and shapes " + quoteList(m.Shapes) + " only"
				break
			}
		}
		t.Errorf("landing tile %q: %s, so the core never returns it and its card is dead weight", tile, msg)
	}
}

func quoteList(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out
}
