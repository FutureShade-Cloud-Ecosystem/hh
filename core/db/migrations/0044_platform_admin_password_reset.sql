-- 0044: reset the platform-owner (colton@futurebuild.ai) password off the
-- migration-0043 placeholder hash, which no one actually holds the plaintext for.
-- Deliberately NOT the commander's real credential — no real password should ever
-- be committed to git, even hashed. This is a one-time throwaway bootstrap value;
-- change it via the app (or a direct DB update) immediately after first login.
-- Idempotent: re-running sets the same hash again, no-op in practice.

UPDATE users
SET password_hash = '$2a$10$n4gHBpnG/4nV9G2tu40QiOnfcPOryTWh3dzRYi5hdSzMgyMsXzLG6'
WHERE email = 'colton@futurebuild.ai';
