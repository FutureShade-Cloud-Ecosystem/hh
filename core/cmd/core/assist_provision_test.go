package main

import (
	"bytes"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// The two oneshots refuse before they connect when their own value is missing,
// and say which variable, never a value.
func TestAssistOneshotsRefuseMissingValuesBeforeConnecting(t *testing.T) {
	dsn := "postgres://app:CANARYDBPASSWORD@127.0.0.1:1/none?sslmode=disable"
	var out bytes.Buffer
	err := assistLoginsRole(env(map[string]string{"DATABASE_URL": dsn}), &out)
	if err == nil || !strings.Contains(err.Error(), "HOS_ASSIST_READER_PASSWORD") {
		t.Fatalf("logins: %v", err)
	}
	err = assistClaimsKeyRole(env(map[string]string{"DATABASE_URL": dsn}), &out)
	if err == nil || !strings.Contains(err.Error(), "HOS_ASSIST_CLAIMS_KEY") {
		t.Fatalf("claims key: %v", err)
	}
	if strings.Contains(out.String(), "CANARYDBPASSWORD") {
		t.Errorf("output carries the database password: %q", out.String())
	}
}

func TestAssistOneshotsNeverEchoAValue(t *testing.T) {
	var out bytes.Buffer
	dsn := "postgres://app:CANARYDBPASSWORD@127.0.0.1:1/none?sslmode=disable"
	pw := "CANARYLOGINPASSWORD-0123456789"
	key := "CANARYCLAIMSKEY-0123456789abcdef0123456789"
	e := env(map[string]string{"DATABASE_URL": dsn, "HOS_ASSIST_READER_PASSWORD": pw, "HOS_ASSIST_CLAIMS_KEY": key})
	e1 := assistLoginsRole(e, &out)
	e2 := assistClaimsKeyRole(e, &out)
	for _, s := range []string{out.String(), errString(e1), errString(e2)} {
		for _, c := range []string{"CANARYDBPASSWORD", "CANARYLOGINPASSWORD", "CANARYCLAIMSKEY"} {
			if strings.Contains(s, c) {
				t.Errorf("%q leaked in %q", c, s)
			}
		}
	}
	if e1 == nil || e2 == nil {
		t.Error("an unreachable database must fail the oneshot")
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
