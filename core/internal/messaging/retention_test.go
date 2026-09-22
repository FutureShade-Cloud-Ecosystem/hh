package messaging

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeRetentionStore is a DB-free retentionStore double: it scripts the per-channel windows and records
// the cutoff each channel is archived through, so the worker's selection loop (cutoff math + per-channel
// dispatch + log-and-continue) is proven without a live Postgres.
type fakeRetentionStore struct {
	rets     []ChannelRetention
	counts   map[string]int       // channelID -> archive rows to return
	failCh   string               // channelID whose archive returns an error
	archived map[string]time.Time // channelID -> cutoff actually passed
	polErr   error                // ChannelRetentions error
}

func (f *fakeRetentionStore) ChannelRetentions(_ context.Context) ([]ChannelRetention, error) {
	return f.rets, f.polErr
}
func (f *fakeRetentionStore) ArchiveChannelOlderThan(_ context.Context, channelID string, cutoff time.Time) (int, error) {
	if f.archived == nil {
		f.archived = map[string]time.Time{}
	}
	f.archived[channelID] = cutoff
	if channelID == f.failCh {
		return 0, errors.New("boom")
	}
	return f.counts[channelID], nil
}

// - pure retention-selection logic (Feature B) -

func TestRetentionCutoff(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	if got, want := retentionCutoff(now, 12), now.AddDate(0, -12, 0); !got.Equal(want) {
		t.Errorf("12-month cutoff = %v, want %v", got, want)
	}
	if got, want := retentionCutoff(now, 6), now.AddDate(0, -6, 0); !got.Equal(want) {
		t.Errorf("6-month cutoff = %v, want %v", got, want)
	}
	// A non-positive months must fall back to the default so a misconfigured policy never archives live
	// content (which a 0/negative window would).
	if got, want := retentionCutoff(now, 0), now.AddDate(0, -defaultRetentionMonths, 0); !got.Equal(want) {
		t.Errorf("zero months should fall back to default, got %v want %v", got, want)
	}
	if got, want := retentionCutoff(now, -5), now.AddDate(0, -defaultRetentionMonths, 0); !got.Equal(want) {
		t.Errorf("negative months should fall back to default, got %v want %v", got, want)
	}
}

// - worker selection loop (Feature B) -

func TestRetentionWorker_ArchiveOnce_PerChannelCutoff(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	f := &fakeRetentionStore{
		rets:   []ChannelRetention{{ChannelID: "ch-a", Months: 12}, {ChannelID: "ch-b", Months: 6}},
		counts: map[string]int{"ch-a": 3, "ch-b": 0},
	}
	w := NewRetentionWorker(f, nil)
	total := w.ArchiveOnce(context.Background(), now)
	if total != 3 {
		t.Fatalf("ArchiveOnce total = %d, want 3 (3 from ch-a + 0 from ch-b)", total)
	}
	if got, want := f.archived["ch-a"], now.AddDate(0, -12, 0); !got.Equal(want) {
		t.Errorf("ch-a archived through %v, want its 12-month cutoff %v", got, want)
	}
	if got, want := f.archived["ch-b"], now.AddDate(0, -6, 0); !got.Equal(want) {
		t.Errorf("ch-b archived through %v, want its 6-month cutoff %v", got, want)
	}
}

// A per-channel archive failure is logged and skipped, it must NOT abort the sweep for other channels.
func TestRetentionWorker_ArchiveOnce_SkipsFailingChannel(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	f := &fakeRetentionStore{
		rets:   []ChannelRetention{{ChannelID: "ch-bad", Months: 12}, {ChannelID: "ch-ok", Months: 12}},
		counts: map[string]int{"ch-ok": 2},
		failCh: "ch-bad",
	}
	w := NewRetentionWorker(f, nil)
	total := w.ArchiveOnce(context.Background(), now)
	if total != 2 {
		t.Fatalf("a failing channel must be skipped; total = %d, want 2 (from ch-ok)", total)
	}
	if _, ran := f.archived["ch-ok"]; !ran {
		t.Fatalf("ch-ok must still be archived after ch-bad failed")
	}
}

// A policy-load failure is logged and yields a zero sweep (the next tick retries), never a panic.
func TestRetentionWorker_ArchiveOnce_PolicyLoadError(t *testing.T) {
	f := &fakeRetentionStore{polErr: errors.New("db down")}
	w := NewRetentionWorker(f, nil)
	if total := w.ArchiveOnce(context.Background(), time.Now()); total != 0 {
		t.Fatalf("policy load error should yield 0 archived, got %d", total)
	}
}
