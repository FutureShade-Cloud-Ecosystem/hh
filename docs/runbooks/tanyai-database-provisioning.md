# TanyAI database provisioning: the assist logins, the claims key and the extension functions

Operator runbook for the database boundary of the assistant (TanyAI slice 4, migration
`0418_assist_roles_and_claims.sql`). The assistant reads data only through two database logins that hold no
privilege except the `ai` and `ai_pro` objects and one function that verifies who is asking. The migration
creates the roles; the oneshots below give them their secrets. Nothing here is a value: every secret comes from
the deployment's vault into the command's environment, and no oneshot prints one.

## Role names carry the database suffix

Role names are cluster wide, and dealer databases may share a cluster, so every name ends in a suffix derived
from the database name: ASCII lower case, every other character an underscore. A name the derivation changed, or
one longer than 30 characters, is cut to 21 characters and joined to the first eight hex characters of the md5
of the original name. With the suffix written `<suffix>`, a database gets:

| Role | Kind | Purpose |
|---|---|---|
| `assist_login_<suffix>` | login, no inherit | the staff tool pool |
| `assist_pro_login_<suffix>` | login, no inherit | the contractor tool pool |
| `assist_reader_<suffix>`, `assist_pro_reader_<suffix>` | no login | what the logins switch to per call |
| `assist_view_owner_<suffix>`, `assist_claims_owner_<suffix>` | no login | own the views, the claims key and the verifier |
| `app_function_users_<suffix>` | no login, not an assist role | holds EXECUTE on the functions the migration closes to PUBLIC |

No assist role may be a member of `app_function_users_<suffix>`; the catalogue test fails if one is. Two
databases on one cluster never share a role.

## The order at deploy

Run each as the role named, against the dealer database, with the database URL in `CORE_DATABASE_URL` (or
`DATABASE_URL`, the legacy name; set only one). Each prints its target (host and database) and never a value.

1. `core migrate`, as the migrating role. Applies `0418` with the rest. The migration creates the roles with no
   password, so no login can sign in yet. On a database whose migrating role is not a superuser, the migrating
   role needs CREATEROLE and must own the database.
2. `core assist-logins`, as the migrating role, with `HOS_ASSIST_READER_PASSWORD` set, and
   `HOS_ASSIST_PRO_READER_PASSWORD` when the contractor path is deployed. It sets each login's password as a
   SCRAM verifier derived inside the command, so the password never reaches the server or a log. A password is
   printable ASCII and at least 16 characters.
3. `core assist-claims-key`, as the migrating role, with `HOS_ASSIST_CLAIMS_KEY` set (at least 32 characters).
   It writes the signing key to `ai.claims_key` as the claims owner under a key id derived from the key.
   Running it again with the same key changes nothing.
4. `core assist-revoke-extension-functions`, run as the role that owns the extensions. See the next section.
5. Start the core with `HOS_ASSIST_READER_DATABASE_URL` (and `HOS_ASSIST_PRO_READER_DATABASE_URL`) pointing at
   the assist logins, and `HOS_ASSIST_CLAIMS_KEY` set to the same key as step 3. The assist path starts only if
   it logs in as this database's assist login (never the app role), two statements in one transaction see one
   backend (so statement level pooling is refused; direct, session and transaction pooling work), and a test
   claim signed with the key verifies. If any check fails the assist path stays off and the rest of the app is
   unaffected; the error names the check, never a value.

Every `HOS_ASSIST_` name is refused on a demo copy until the demo guard opens them.

### Rotating the claims key

Run step 3 again with the new key in `HOS_ASSIST_CLAIMS_KEY`, then restart the core with the new key. The
database accepts the two most recent keys, so claims signed with the old key still verify until the restart; a
third rotation drops the oldest. Rerunning with the key that is already current changes nothing. Rotate the
login passwords by running step 2 with new values and updating the core's database URLs.

## The extension functions, and the open state

`pgcrypto`, `citext` and `pg_trgm` functions are granted to PUBLIC by default. The migration closes every
function outside `ai` to PUBLIC and gives EXECUTE to `app_function_users_<suffix>`, so the assist roles hold no
function outside `ai`.

- Where the migrating role is a superuser, it owns those functions and the migration closes them. Step 4 is
  then a repeat that changes nothing, and is safe to run.
- Where the migrating role is not a superuser, the extensions' functions belong to the bootstrap superuser, so
  the migration's revoke on them is only a warning. Until step 4 runs, the database is in the open state: exactly
  the extension functions the migrating role does not own keep PUBLIC EXECUTE. The assist roles can run those
  functions, which read nothing, but the boundary is not yet as written. The catalogue test in CI describes
  this state and accepts it only for that exact set.
- Step 4 must run as the role that owns the extensions (the bootstrap superuser). It closes every extension
  function to PUBLIC, grants them to `app_function_users_<suffix>`, and gives `hmac` back to the claims owner
  so the verifier keeps working. Run it again after any `CREATE EXTENSION` or extension upgrade.

## A separate serving role

If the core's serving role is separate from the migrating role (a non superuser serve role), the migration
closed PUBLIC EXECUTE on the application's functions and the serve role holds none of it until it is made a
member of the function users role:

    GRANT app_function_users_<suffix> TO <serving role>;

Without it the application loses the extension functions and any function created by the migrating role. The
migrating role is made a member by the migration. Functions the migrating role creates later start closed and
are granted to the function users role by default privileges. The serve startup check of the data slice
verifies the membership.

## pg_hba

Each assist login connects from the core's address only, to its own database only. One line per suffixed login,
with the database the migration ran in, on the dealer database server:

    host  <database>  assist_login_<suffix>      <core address>/32  scram-sha-256
    host  <database>  assist_pro_login_<suffix>  <core address>/32  scram-sha-256

and no other line that matches them (a broader earlier line wins). The logins hold no privilege in any other
database, but connecting there is not otherwise prevented.

## Never log statements on a dealer box

Do not enable `log_statement` or `log_min_duration_statement` on a dealer database server. Bind parameters
appear in the log's detail lines: the signed claim of each tool call, and at provisioning the password verifiers
and the claims key. The provisioning oneshots set both to off for their own transaction where the role may
(they are superuser settings). Where the role may not, the oneshot prints one warning and carries on; then the
server's own setting is all that protects the values.

## What a login can and cannot change

Role defaults on the logins (read only, short timeouts) are defaults, not guarantees: a login can change its own
role settings and password once its session is read write. They bound a runaway call, not a hostile one; the
boundary is the privileges. If the logins stop working, run step 2 again.

## Check

- The catalogue test (`core/internal/assist/readtx`, integration tag) passes on the migrated database.
- `core assist-logins` and `core assist-claims-key` print role names and a key id, never a value.
- The core starts with the assist path on; a start with a wrong password, the app role's URL or an unprovisioned
  key refuses the assist path and names the check.
