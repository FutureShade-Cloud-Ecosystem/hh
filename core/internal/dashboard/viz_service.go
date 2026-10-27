package dashboard

// viz_service.go, resolving a person's composed tiles.
//
// The service holds the SeriesProvider registry (the composition root fills it) and the read of the
// dealer's metric curation. It is deliberately a SEPARATE seam from Store: adding a method to Store
// would have made every existing fake in this package's tests stop compiling for a feature they do
// not exercise, and a store nobody injected must degrade to the shipped catalog rather than to an
// error, the same posture the hotkey read takes.

import (
	"context"
	"sync"
)

// MetricCurationStore reads the dealer's metric curation (0172 surface_role_items, surface
// `home.metrics`, kind `chip`). Optional: a nil store means "nobody has curated this", which
// resolves to the shipped catalog, not to an empty composer.
type MetricCurationStore interface {
	CuratedMetrics(ctx context.Context, profile string) ([]CuratedMetric, error)
}

// WithMetricStore binds the curation read. Chainable at wiring time.
func (s *Service) WithMetricStore(st MetricCurationStore) *Service {
	s.metricStore = st
	return s
}

// RegisterSeries binds a series provider to a catalog metric. Called ONLY from the composition
// root. Refused for an unknown metric id for the same reason Register refuses an unknown tile: a
// typo'd id is a silently dead adapter whose symptom is indistinguishable from a module being down.
func (s *Service) RegisterSeries(metricID string, p SeriesProvider) error {
	if _, ok := MetricByID[metricID]; !ok {
		return errUnknownMetric(metricID)
	}
	if p == nil {
		return errNilSeries(metricID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.series == nil {
		s.series = map[string]SeriesProvider{}
	}
	s.series[metricID] = p
	return nil
}

func (s *Service) seriesProvider(metricID string) (SeriesProvider, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.series[metricID]
	return p, ok
}

// curation reads the dealer's metric menu for a profile, degrading TO THE SHIPPED CATALOG (nil)
// rather than to nothing. An unreadable curation table must not take away the composer somebody
// used yesterday, and it must not be able to ADD anything either, which is why the cost gate is
// applied afterwards and not here.
func (s *Service) curation(ctx context.Context, profile string) []CuratedMetric {
	if s.metricStore == nil {
		return nil
	}
	rows, err := s.metricStore.CuratedMetrics(ctx, profile)
	if err != nil {
		if s.log != nil {
			s.log.Warn("dashboard: metric curation unreadable; offering the shipped catalog",
				"profile", profile, "err", err)
		}
		return nil
	}
	return rows
}

// MetricCatalogFor answers the catalog this caller may compose from (the composer's one read).
func (s *Service) MetricCatalogFor(ctx context.Context, scope Scope) []MetricMeta {
	if scope.Profile == "" {
		scope.Profile = s.ProfileFor(ctx, scope.RoleCode)
	}
	return MetricsFor(s.curation(ctx, scope.Profile), scope.CostAllowed)
}

// ResolveViz measures the caller's composed tiles, in the order they were asked for.
//
// THE GATE IS HERE, NOT IN THE CLIENT. Every id goes through three refusals before anything is
// measured, and a refusal is an ABSENCE, the tile is not in the answer at all:
//
//  1. it does not parse into (metric, dimension, shape) from the closed sets;
//  2. its metric is not in THIS caller's catalog (which is where the cost class disappears);
//  3. it names a metric this build no longer ships.
//
// A tile that survives all three but has no series provider is a different answer entirely: it is
// present, StateUnwired, and the screen says so. "We are not measuring this yet" is honest; leaving
// the tile out would look like the person's saved chart had been deleted.
func (s *Service) ResolveViz(ctx context.Context, scope Scope, ids []string) []VizTile {
	s.stampScope(ctx, &scope)
	if scope.Profile == "" {
		scope.Profile = s.ProfileFor(ctx, scope.RoleCode)
	}
	allowed := map[string]MetricMeta{}
	for _, m := range MetricsFor(s.curation(ctx, scope.Profile), scope.CostAllowed) {
		allowed[m.ID] = m
	}

	out := make([]VizTile, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		c, ok := ParseComposition(id)
		if !ok {
			continue
		}
		m, ok := allowed[c.MetricID]
		if !ok {
			continue // BYTE-ABSENT: not refused in words, not an empty tile, simply not there.
		}
		out = append(out, VizTile{
			Composition: c, Label: m.Label, Kind: m.Kind, Module: m.Module,
			Unit: m.Unit, Screen: m.Screen, Position: len(out), State: StateUnwired,
			Note: unwiredNote(m.Module),
		})
	}

	var wg sync.WaitGroup
	for i := range out {
		p, ok := s.seriesProvider(out[i].MetricID)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(idx int, p SeriesProvider) {
			defer wg.Done()
			pts, err := s.measureSeries(ctx, p, scope, out[idx].Dimension)
			t := &out[idx]
			if err != nil {
				t.State, t.Note = StateError, "Temporarily unavailable"
				if s.log != nil {
					s.log.Warn("dashboard: series provider failed",
						"metric", t.MetricID, "dimension", t.Dimension, "err", err)
				}
				return
			}
			total := s.totalFor(ctx, out[idx].Kind, p, scope, pts)
			t.State, t.Points, t.Note = StateOK, pts, ""
			t.Total = total
		}(i, p)
	}
	wg.Wait()
	return out
}

// measureSeries runs one series provider under the same per-provider budget and the same panic
// recovery a tile measurement gets, a composed chart must not be able to take the home screen
// down in a way a shipped tile cannot.
func (s *Service) measureSeries(ctx context.Context, p SeriesProvider, scope Scope, dim string) (pts []Point, err error) {
	defer func() {
		if r := recover(); r != nil {
			pts, err = nil, errSeriesPanic(r)
		}
	}()
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return p.Series(cctx, scope, dim)
}

// totalFor answers a tile's headline total for its measured series.
//
// THE BASIS POINT RULE (FROM-PRINCIPAL 132). Counts and money add: two branches' sales and two
// branches' queues are sums by nature. A RATE does not: two branches at 20% and 50% are not 70%,
// and not 35% either, the all branches rate is total margin cents over total sales cents across
// the branches. A rate's points carry no weights, so the sum is unreachable from them; instead the
// PROVIDER answers its own aggregate when asked for SplitNone (the ungrouped arithmetic over the
// same scope), and that one number is the tile's total. A provider that implements no aggregate
// leaves the total nil: the client shows the series without a headline, never a sum of rates.
func (s *Service) totalFor(ctx context.Context, kind Kind, p SeriesProvider, scope Scope, pts []Point) *int64 {
	if kind != KindBasisPoints {
		n := TotalOf(pts)
		return &n
	}
	agg, err := s.measureSeries(ctx, p, scope, SplitNone)
	if err != nil || len(agg) != 1 {
		return nil
	}
	n := agg[0].Number
	return &n
}
