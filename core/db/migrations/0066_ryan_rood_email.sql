-- 0066: Scrubname0025 Scrubname0024's homework/login email → scrubbed-0008@example.invalid (was scrubbed-0006@example.invalid).
-- Scrubname0006 directive 2026-07-16. Runs AFTER 0061 (which seeds the account) and 0062.
-- Idempotent: a no-op once applied or on a DB already updated out-of-band. user_roles and
-- branch grants key on user_id (FK), so changing only the email is safe and preserves them.
-- users.email is UNIQUE; on a fresh migrate no scrubbed-0008@example.invalid exists yet, so the UPDATE is clean.
UPDATE users
   SET email = 'scrubbed-0008@example.invalid'
 WHERE email = 'scrubbed-0006@example.invalid';
