package dashboard

// viz_repository.go, the dealer's metric curation, read from the 0172 presentation substrate.
//
// NO NEW TABLE. Which metrics a job shape may compose from is presentation curation, so it lives
// where every other presentation curation in the product lives: surface_role_items, keyed by the
// surface id below and the `chip` kind (a metric offered in a composer is a quick-pick, which is
// what that kind means everywhere else it is used).

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MetricSurfaceID is the 0172 surface the metric curation is filed under.
const MetricSurfaceID = "home.metrics"

// MetricRepository reads metric curation. Separate from Repository so a build with no curation
// wired keeps a working composer rather than an empty one.
type MetricRepository struct{ pool *pgxpool.Pool }

func NewMetricRepository(pool *pgxpool.Pool) *MetricRepository { return &MetricRepository{pool: pool} }

var _ MetricCurationStore = (*MetricRepository)(nil)

// CuratedMetrics returns the rows for one job shape, in curated order. No rows is not an error and
// not an empty menu, the service reads it as "not curated" and offers the shipped catalog.
func (r *MetricRepository) CuratedMetrics(ctx context.Context, profile string) ([]CuratedMetric, error) {
	if r.pool == nil || profile == "" {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT item_id, position, visible
		  FROM surface_role_items
		 WHERE surface_id = $1 AND profile = $2 AND item_kind = 'chip'
		 ORDER BY position, item_id`, MetricSurfaceID, profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CuratedMetric{}
	for rows.Next() {
		var c CuratedMetric
		if err := rows.Scan(&c.MetricID, &c.Position, &c.Visible); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
