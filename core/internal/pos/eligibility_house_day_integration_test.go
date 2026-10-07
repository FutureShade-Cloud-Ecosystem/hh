//go:build integration

package pos

import (
	"context"
	"testing"
	"time"
)

// A certificate that lapses today is good for the whole of the dealer's day. The read decides
// "today" in the house zone, so on the evening after the UTC day change (the house zone still on
// the earlier day) a certificate expiring that house day is not yet expired, and one that expired
// the house day before is.
func TestIntegration_EligibilityProfile_ExpiryIsTheHouseDay(t *testing.T) {
	s := newTrentonStack(t)
	ctx := context.Background()
	repo := NewRepository(s.pool)

	plant := func(expiry string) string {
		t.Helper()
		var id string
		if err := s.pool.QueryRow(ctx, `
			INSERT INTO purchaser_eligibility_profile (purchaser_type, ontario_eligibility, proof_identifier_mask, tcrd_expiry)
			VALUES ('INDIVIDUAL','ON_RESIDENT','•••• 0001',$1::date) RETURNING id::text`, expiry).Scan(&id); err != nil {
			t.Fatalf("plant the profile: %v", err)
		}
		t.Cleanup(func() {
			_, _ = s.pool.Exec(context.Background(), `DELETE FROM purchaser_eligibility_profile WHERE id = $1::uuid`, id)
		})
		return id
	}
	read := func(id string) bool {
		t.Helper()
		p, err := repo.ReadEligibilityProfileMasked(ctx, id)
		if err != nil {
			t.Fatalf("read the profile: %v", err)
		}
		return p.Expired
	}

	houseToday := time.Now().In(houseLocation())
	if read(plant(houseToday.Format("2006-01-02"))) {
		t.Fatalf("a certificate expiring on the house zone's day is still good that whole day")
	}
	if !read(plant(houseToday.AddDate(0, 0, -1).Format("2006-01-02"))) {
		t.Fatalf("a certificate that expired the house day before is expired")
	}
}
