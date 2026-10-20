package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type scriptedRow struct {
	vals []any
	err  error
}

func (r scriptedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = r.vals[i].(string)
		case *bool:
			*p = r.vals[i].(bool)
		}
	}
	return nil
}

// fakeStartupDB answers the three questions the check asks.
type fakeStartupDB struct {
	super    string
	active   map[string]bool
	member   bool
	roleGone bool
	asked    []string
}

func (f *fakeStartupDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.asked = append(f.asked, sql)
	switch {
	case strings.Contains(sql, "current_database"):
		return scriptedRow{vals: []any{"dealer_x", f.super}}
	case strings.Contains(sql, "row_security_active"):
		return scriptedRow{vals: []any{f.active[args[0].(string)]}}
	case strings.Contains(sql, "pg_has_role"):
		if f.roleGone {
			return scriptedRow{err: errors.New("role does not exist")}
		}
		return scriptedRow{vals: []any{f.member}}
	}
	return scriptedRow{err: errors.New("unexpected query")}
}

func TestAssistStartupCheckRefusesAPolicedTableTheAppCannotReadAndNamesIt(t *testing.T) {
	db := &fakeStartupDB{super: "off", active: map[string]bool{"public.orders": false, "public.order_lines": true}, member: true}
	err := assistRequireServingRole(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "order_lines") || strings.Contains(err.Error(), "public.orders ") {
		t.Fatalf("want a refusal naming order_lines only, got %v", err)
	}
}

func TestAssistStartupCheckNeedsMembershipOfTheFunctionUsersRoleUnlessSuperuser(t *testing.T) {
	ok := map[string]bool{"public.orders": false, "public.order_lines": false}
	for name, c := range map[string]struct {
		db      *fakeStartupDB
		wantErr string
	}{
		"superuser needs no membership": {&fakeStartupDB{super: "on", active: ok}, ""},
		"member passes":                 {&fakeStartupDB{super: "off", active: ok, member: true}, ""},
		"non member refused":            {&fakeStartupDB{super: "off", active: ok, member: false}, "app_function_users_dealer_x"},
		"missing role refused":          {&fakeStartupDB{super: "off", active: ok, roleGone: true}, "app_function_users_dealer_x"},
	} {
		err := assistRequireServingRole(context.Background(), c.db)
		if c.wantErr == "" && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: err = %v, want it to name %s", name, err, c.wantErr)
		}
	}
}
