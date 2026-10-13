//go:build integration

// syncpick_lockorder_integration_test.go: every path that writes a task's pick state takes the task row,
// then the pick counter. A direct door record (UpsertPickedLines) and the sync door's stamp on one task
// at the same time must never deadlock (a device replaying its queued record while a desk records the
// same ticket is ordinary during catch up).
package tasks

import (
	"context"
	"strings"
	gosync "sync"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

func TestPickDoor_DirectAndSyncRecordsOnOneTaskNeverDeadlock(t *testing.T) {
	for _, mode := range []string{"direct against sync", "sync against sync"} {
		t.Run(mode, func(t *testing.T) {
			d := newPickDoor(t)
			repo := NewRepository(d.pool)
			id := d.task(d.branchA)
			var wg gosync.WaitGroup
			var mu gosync.Mutex
			var failures []string
			note := func(err error) {
				if err != nil {
					mu.Lock()
					failures = append(failures, err.Error())
					mu.Unlock()
				}
			}
			direct := func() {
				defer wg.Done()
				for i := 0; i < 40; i++ {
					_, err := repo.UpsertPickedLines(context.Background(), id, d.user, []PickedLineInput{{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "10", UOM: "EA"}})
					note(err)
				}
			}
			push := func() {
				defer wg.Done()
				for i := 0; i < 40; i++ {
					_, err := d.engine.Push(context.Background(), d.info, d.f, []apigen.SyncPushOperation{recordOp(uuid.NewString(), id, pickLine(2))})
					note(err)
				}
			}
			wg.Add(2)
			if strings.HasPrefix(mode, "direct") {
				go direct()
			} else {
				go push()
			}
			go push()
			wg.Wait()
			if len(failures) != 0 {
				t.Fatalf("%d of 80 writes failed, first: %v", len(failures), failures[0])
			}
		})
	}
}
