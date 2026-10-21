//go:build integration

package parity

// data.query over the slice 7b views, through the querier (so the plan shape check runs): the default read of each
// view and a filtered read answer, rows cite where the app has a reference (customers by account), a filter value on
// a personal column is logged as the marker, and a message channel read never carries a body.

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/adapters"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
)

func TestQuery_TheSlice7bViewsAnswerTheirDefaultReadsAndCite(t *testing.T) {
	env := testsupport.Open(t)
	dw := plantDeliveryWorld(t, env)
	cw := plantCRMWorld(t, env)
	mw := plantMsgWorld(t, env)
	ctx := testsupport.Ctx(t)
	pool, err := pgxpool.New(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	msgSvc := messaging.NewService(messaging.NewRepository(pool))
	if _, err := msgSvc.ListMyChannels(ctx, messaging.Caller{UserID: mw.users["u1"]}); err != nil {
		t.Fatal(err)
	}
	h := assist.NewHandler(nil, nil).WithChannelMembership(adapters.NewChannelMembership(msgSvc))
	log := &memLog{}
	q := &assist.ReadQuerier{Reader: env.Readers.Staff, Now: time.Now, Log: log}
	gm := deliveryCallers(dw.branches()...)[0].sc
	gm.UserID = mw.users["u1"] // the global manager is also the planted message user u1
	caller, _ := claimsOf(t, h, gm)
	for _, c := range []struct {
		name, spec, label string
		min               int
		cites             bool
	}{
		{"delivery runs, default", `{"view":"ai.delivery_runs","limit":100}`, "Run", 3, false},
		{"delivery stops, default", `{"view":"ai.delivery_stops","limit":200}`, "Stop", 6, true},
		{"delivery stops, one run", `{"view":"ai.delivery_stops","where":[{"col":"run_no","op":"contains","value":"S7B-` + dw.tag + `-a-out"}],"order_by":[{"col":"stop_no","dir":"asc"}]}`, "Stop", 3, true},
		{"customers, default", `{"view":"ai.customers","limit":200}`, "Account", 7, true},
		{"customers, planted", `{"view":"ai.customers","where":[{"col":"account_no","op":"contains","value":"S7B-` + cw.tag + `"}]}`, "Account", 7, true},
		{"customer contacts, default", `{"view":"ai.customer_contacts","limit":200}`, "Account", 3, true},
		{"message channels, default", `{"view":"ai.message_channels","limit":200}`, "Channel", 3, false},
		{"message channels, with unread", `{"view":"ai.message_channels","select":["name","unread_count","notify_mode"],"where":[{"col":"unread_count","op":">","value":0}]}`, "Channel", 2, false},
	} {
		res, err := q.Query(ctx, caller, c.spec)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if res.Notice != nil || len(res.Items) < c.min {
			t.Errorf("%s: want at least %d rows, got headline %q notice %+v, %d rows", c.name, c.min, res.Headline, res.Notice, len(res.Items))
			continue
		}
		if c.cites {
			for _, it := range res.Items {
				if it.Ref == nil || it.Ref.ID == "" || it.Ref.Label == "" {
					t.Errorf("%s: a row carries no citation: %+v", c.name, it)
					break
				}
				for _, bad := range []string{"Mrs", "@example.test"} {
					if strings.Contains(it.Ref.Label, bad) {
						t.Errorf("%s: a citation label holds personal data: %q", c.name, it.Ref.Label)
					}
				}
			}
		}
		for _, it := range res.Items {
			for _, f := range it.Fields {
				if strings.Contains(f.Value, bodySecret) || strings.Contains(f.Value, gateCodeSecret) {
					t.Errorf("%s: a field carries a secret: %q", c.name, f.Value)
				}
			}
		}
	}
	// The planted account's columns are in the answer, and the message channels name the asker's channels.
	res, err := q.Query(ctx, caller, `{"view":"ai.message_channels","select":["name"],"where":[{"col":"name","op":"contains","value":"`+mw.tag+`"}],"limit":50}`)
	if err != nil || len(res.Items) != 4 {
		t.Errorf("u1's planted channels: %v, %d rows, want c1, c3-dm, c5-muted and c7-mentions", err, len(res.Items))
	}
	// A filter on a personal column is logged as the marker, never its value.
	before := len(log.rows)
	if _, err := q.Query(ctx, caller, `{"view":"ai.delivery_stops","select":["order_no"],"where":[{"col":"contact_phone","op":"=","value":"555-0199"},{"col":"status","op":"=","value":"DELIVERED"}]}`); err != nil {
		t.Fatal(err)
	}
	last := string(log.rows[len(log.rows)-1].Spec)
	if len(log.rows) != before+1 || strings.Contains(last, "555-0199") || !strings.Contains(last, "(personal)") || !strings.Contains(last, "DELIVERED") {
		t.Errorf("the log of a personal filter: %s", last)
	}
}
