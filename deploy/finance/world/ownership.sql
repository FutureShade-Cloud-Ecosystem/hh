-- The owner statements the world's dump leaves out.
--
-- The world's dump is taken with --no-owner, so the objects the assist roles
-- own (the ai schema's views, its claims function and type, its claims key
-- table and the zone functions) would land on the box owned by the role that
-- restores them, not by the role that owns them. Two things break then: the
-- world's own migrations that alter those objects refuse (a migration that
-- alters a type the claims owner owns needs that owner), and the views in ai
-- lose the owner whose row level security policies they run under.
--
-- This query writes those owner statements in the dump's own words, so the
-- dump carries the world's shape. The reset maps the role names onto the box
-- database's own roles before it restores, the way it maps the grants.
--
-- A handover needs the new owner to hold CREATE on the schema the object
-- lives in, and the world's final shape does not always give it that: the
-- zone role created its functions while it held CREATE on ai, and the
-- migration after it took that back. So the statements that need it are
-- written around the handover and taken back afterwards, and the schema
-- ends the way the world left it.
--
-- The statements are read only: the query reads the catalogue and writes
-- text, and the text is appended to the dump after pg_dump has written it.

CREATE TEMP VIEW world_ownership AS
SELECT 'TYPE' AS kind,
       n.nspname AS schema,
       format('%I', c.relname) AS ident,
       ''::text AS args,
       r.rolname AS owner
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_roles r ON r.oid = c.relowner
 WHERE n.nspname IN ('ai', 'ai_pro')
   AND c.relkind = 'c'
   AND r.rolname LIKE 'assist\_%'
UNION ALL
SELECT 'TABLE',
       n.nspname,
       format('%I', c.relname),
       '',
       r.rolname
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_roles r ON r.oid = c.relowner
 WHERE n.nspname IN ('ai', 'ai_pro')
   AND c.relkind IN ('r', 'p')
   AND r.rolname LIKE 'assist\_%'
UNION ALL
SELECT 'VIEW',
       n.nspname,
       format('%I', c.relname),
       '',
       r.rolname
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_roles r ON r.oid = c.relowner
 WHERE n.nspname IN ('ai', 'ai_pro')
   AND c.relkind = 'v'
   AND r.rolname LIKE 'assist\_%'
UNION ALL
SELECT 'FUNCTION',
       n.nspname,
       format('%I', p.proname),
       format('(%s)', pg_get_function_identity_arguments(p.oid)),
       r.rolname
  FROM pg_proc p
  JOIN pg_namespace n ON n.oid = p.pronamespace
  JOIN pg_roles r ON r.oid = p.proowner
 WHERE n.nspname IN ('ai', 'ai_pro')
   AND r.rolname LIKE 'assist\_%';

-- The schema privilege the new owner needs for the handover.
SELECT DISTINCT format('GRANT USAGE, CREATE ON SCHEMA %I TO %I;', schema, owner)
  FROM world_ownership
 WHERE NOT has_schema_privilege(owner, schema, 'CREATE');

-- The handovers.
SELECT format('ALTER %s %I.%s%s OWNER TO %I;', kind, schema, ident, args, owner)
  FROM world_ownership
 ORDER BY kind, schema, ident;

-- The schema privilege taken back, the same one the world left.
SELECT DISTINCT format('REVOKE %s ON SCHEMA %I FROM %I;',
              CASE WHEN has_schema_privilege(owner, schema, 'USAGE') THEN 'CREATE' ELSE 'ALL' END,
              schema, owner)
  FROM world_ownership
 WHERE NOT has_schema_privilege(owner, schema, 'CREATE');
