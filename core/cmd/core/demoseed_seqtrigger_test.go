//go:build demo

package main

// demoseed_seqtrigger_test.go: the demo repair's table to scope map (demoSeqTables) and the sync seq trigger's
// table to stream map (sync.SeqTriggerStreams, migration 0422) must name one stream per table. A table the
// trigger carries draws from its trigger stream wherever it is sequenced, so a repair that numbered it from
// another counter would put two counters on one column.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// knownStreamDisagreements: a table the repair map and the trigger map name differently, with the ruling that
// settles it. Empty today.
var knownStreamDisagreements = map[string]string{}

func TestDemoSeqRepairMapAgreesWithTheSyncSeqTrigger(t *testing.T) {
	trig := sync.SeqTriggerStreams()
	seen := 0
	for _, s := range demoSeqTables {
		stream, carried := trig[s.table]
		if !carried {
			continue
		}
		seen++
		if _, known := knownStreamDisagreements[s.table]; known {
			continue
		}
		if s.scopeName() != stream {
			t.Errorf("table %s: the demo repair draws scope %q, the sync seq trigger draws stream %q",
				s.table, s.scopeName(), stream)
		}
	}
	if seen != len(trig) {
		t.Errorf("the demo repair map names %d of the %d trigger tables", seen, len(trig))
	}
}
