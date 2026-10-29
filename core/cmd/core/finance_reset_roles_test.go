package main

// finance_reset_roles_test.go pins the reset's role mapping (W2 reset fix
// r1, finding 3) without a database: the world's dump is built on one
// cluster, under one database name, by one migrating role, and the box it
// lands on is another. The mapping is what takes the build's names onto the
// box's own, and it never touches the world's data.

import (
	"strings"
	"testing"
)

const financeResetMappingDump = `-- Name: ai; Type: SCHEMA; Schema: -; Owner: postgres
CREATE SCHEMA ai;

--
-- Data for Name: customers; Type: TABLE DATA; Schema: public; Owner: postgres
--

COPY public.customers (id, name) FROM stdin;
1	assist_view_owner_finworld is only a name here
2	the box's own name
\.


--
-- Name: customers; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.customers (id integer NOT NULL, name text);

--
-- Name: TABLE customers; Type: ACL; Schema: public; Owner: -
--

REVOKE ALL ON TABLE public.customers FROM PUBLIC;
GRANT SELECT(id) ON TABLE public.customers TO assist_view_owner_finworld;
GRANT ALL ON SCHEMA ai TO assist_view_owner_finworld;
GRANT SELECT ON TABLE public.customers TO app_function_users_finworld;

--
-- Name: DEFAULT PRIVILEGES FOR FUNCTIONS; Type: DEFAULT ACL; Schema: -; Owner: -
--

ALTER DEFAULT PRIVILEGES FOR ROLE assist_claims_owner_finworld REVOKE ALL ON FUNCTIONS FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE assist_view_owner_finworld REVOKE ALL ON FUNCTIONS FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE postgres REVOKE ALL ON FUNCTIONS FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE postgres GRANT ALL ON FUNCTIONS TO app_function_users_finworld;

--
-- Name: ai; Type: SCHEMA; Schema: -; Owner: postgres
--

CREATE POLICY assist_staff_orders ON public.orders TO assist_view_owner_finworld USING (true);

--
-- Name: claims(); Type: FUNCTION; Schema: ai; Owner: assist_claims_owner_finworld
--

CREATE FUNCTION ai.claims() RETURNS jsonb AS $body$
BEGIN
      IF session_user = 'assist_login_finworld' AND c.audience <> 'staff' THEN
         RETURN NULL;
      END IF;
      IF session_user = 'assist_pro_login_finworld' AND c.audience <> 'pro' THEN
         RETURN NULL;
      END IF;
END;
$body$ LANGUAGE plpgsql;
`

func TestFinanceResetBuildSuffix(t *testing.T) {
	suffix, err := financeResetBuildSuffix([]byte(financeResetMappingDump))
	if err != nil {
		t.Fatalf("the build suffix: %v", err)
	}
	if suffix != "finworld" {
		t.Fatalf("the build suffix is %q, want the world's own %q", suffix, "finworld")
	}
}

func TestFinanceResetMapRoles(t *testing.T) {
	mapped, err := financeResetMapRoles([]byte(financeResetMappingDump), "finworld", "finbox", "finreset_app")
	if err != nil {
		t.Fatalf("mapping the dump: %v", err)
	}
	text := string(mapped)

	// The assist role names carry the box's own suffix.
	for _, want := range []string{
		"GRANT SELECT(id) ON TABLE public.customers TO assist_view_owner_finbox;",
		"GRANT ALL ON SCHEMA ai TO assist_view_owner_finbox;",
		"GRANT SELECT ON TABLE public.customers TO app_function_users_finbox;",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the mapped dump does not carry %q:\n%s", want, text)
		}
	}
	// The build's names are gone from the statements. The data may carry the
	// name as a value: that is the world's own row, and it stays.
	for _, line := range financeResetStatementLines(mapped) {
		for _, unwanted := range []string{"assist_view_owner_finworld", "app_function_users_finworld"} {
			if strings.Contains(line, unwanted) {
				t.Fatalf("the mapped dump still carries the build's name %q in a statement: %s", unwanted, line)
			}
		}
	}
	// The default privileges name the box's own restoring role.
	for _, want := range []string{
		"ALTER DEFAULT PRIVILEGES FOR ROLE finreset_app REVOKE ALL ON FUNCTIONS FROM PUBLIC;",
		"ALTER DEFAULT PRIVILEGES FOR ROLE finreset_app GRANT ALL ON FUNCTIONS TO app_function_users_finbox;",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the mapped dump does not carry %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "FOR ROLE postgres") {
		t.Fatalf("the mapped dump still names the build's migrating role:\n%s", text)
	}
	// The world's own data is copied verbatim: a row that carries the build's
	// role name is data, not a statement.
	if !strings.Contains(text, "1\tassist_view_owner_finworld is only a name here") {
		t.Fatalf("the mapping touched the world's own data:\n%s", text)
	}

	// The row level security policies name the assist role, and so they are
	// mapped: a policy left on the build's role holds nothing on the box.
	want := "CREATE POLICY assist_staff_orders ON public.orders TO assist_view_owner_finbox USING (true);"
	if !strings.Contains(text, want) {
		t.Fatalf("the mapped dump does not carry %q:\n%s", want, text)
	}

	// The world's own functions compare session_user against the login role
	// names, so those names are part of the world's shape and are mapped too.
	for _, want := range []string{
		"IF session_user = 'assist_login_finbox' AND c.audience <> 'staff' THEN",
		"IF session_user = 'assist_pro_login_finbox' AND c.audience <> 'pro' THEN",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the mapped dump does not carry %q:\n%s", want, text)
		}
	}
}

func TestFinanceResetMapRolesRefusesTwoSuffixes(t *testing.T) {
	dump := `GRANT SELECT ON TABLE public.customers TO assist_view_owner_finworld;
GRANT SELECT ON TABLE public.customers TO assist_view_owner_otherworld;
`
	if _, err := financeResetBuildSuffix([]byte(dump)); err == nil {
		t.Fatalf("a dump carrying two assist role suffixes was accepted")
	}
}

func TestFinanceResetMapRolesNoAssistRoles(t *testing.T) {
	dump := `CREATE TABLE public.customers (id integer NOT NULL);
GRANT SELECT ON TABLE public.customers TO somebody_else;
`
	mapped, err := financeResetMapRoles([]byte(dump), "", "finbox", "finreset_app")
	if err != nil {
		t.Fatalf("mapping a dump with no assist roles: %v", err)
	}
	if !strings.Contains(string(mapped), "TO somebody_else;") {
		t.Fatalf("the mapping changed a grant to a role that is not an assist role:\n%s", mapped)
	}
}
