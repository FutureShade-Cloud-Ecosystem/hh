//go:build integration

package readtx

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
	"testing"

	"github.com/jackc/pgx/v5"
)

// P3-4: the database binds the audience to the login: a staff claim on the
// contractor login, and the reverse, verify to NULL.
func TestAudienceMustMatchTheLoginAtTheDatabase(t *testing.T) {
	admin, _, _, key, cases, urls := setupRedTeam(t)
	_ = admin
	kid := int(KeyID(redact.NewSecret(key)))
	for _, c := range cases {
		conn := connect(t, urls[c.login])
		pid := backendPID(t, conn)
		if _, err := conn.Exec(ctxT(t), `SET ROLE `+pgx.Identifier{c.reader}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		mk := func(aud string) string {
			p := goodPayload(pid)
			p["audience"] = aud
			return rawSigned(kid, key, p)
		}
		if ok, _ := verified(t, conn, mk(c.audience)); !ok {
			t.Errorf("%s: its own audience must verify", c.name)
		}
		other := AudiencePro
		if c.audience == AudiencePro {
			other = AudienceStaff
		}
		if ok, _ := verified(t, conn, mk(other)); ok {
			t.Errorf("%s login verified a %s claim", c.name, other)
		}
	}
}
