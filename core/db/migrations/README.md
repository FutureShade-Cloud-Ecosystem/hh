# core/db/migrations

Forward-only SQL migrations, numbered `NNNN_name.sql`. A migration is never
edited after commit and never renumbered: the platform snapshots the database,
migrates, then starts the new binary, rolling back on failure, so history must
stay immutable. Run by the binary's `migrate` role, aimed with `DATABASE_URL`.
