-- 0416_assist_aliases_and_conversations.sql, TanyAI slice 1 (the model layer).
-- Owner module: internal/assist.
--
-- THREE THINGS, ALL FOR THE MODEL LAYER.
--
-- 1. THE ALIAS TABLE. HH code calls aliases (tanyai-main, tanyai-alt, tanyai-premium, tanyai-ca),
--    never models. A row names the adapter kind, the base URL, the model, whether it is enabled, and
--    the licence and processing region recorded for Colton's provider decision. tanyai-premium is
--    the native Anthropic adapter on claude-sonnet-5-5 (the demo and premium route); the other
--    three are unconfigured and resolve to the deterministic stub until an operator fills them in.
--    A model string that is not in this table is never sent to any vendor (internal/assist/aliases.go).
--
-- 2. SERVER KEPT CONVERSATIONS (review r2, P2-5). A conversation is bound to the asker's user id AND
--    a session key taken from the verified bearer's jti, because demo personas are shared users:
--    history keyed by user would hand one visitor another's turns. The exchange log is APPEND ONLY,
--    enforced in the database with the same trigger pattern as the token ledger (0154): the
--    history a model is shown is rebuilt from rows nothing can quietly edit.
--
-- 3. THE 0153 MODEL SEED MADE HARMLESS. 0153 seeded the setting model='hos-stub-1', a label for the
--    stub. On main, switching only the provider to 'anthropic' sent that string to the vendor as a
--    model name. The code now treats the model setting as an alias and ignores anything else; this
--    migration also rewrites the stored seed to an alias so the row says what it means. The
--    provider setting stays 'stub', so nothing changes for a deployment until an admin switches it.

CREATE TABLE IF NOT EXISTS assist_model_aliases (
    alias        text PRIMARY KEY CHECK (alias ~ '^tanyai-[a-z]+$'),
    adapter_kind text NOT NULL CHECK (adapter_kind IN ('anthropic', 'openai_compatible', 'stub')),
    base_url     text NOT NULL DEFAULT '',
    model        text NOT NULL DEFAULT '',
    enabled      boolean NOT NULL DEFAULT false,
    licence      text NOT NULL DEFAULT '',
    region       text NOT NULL DEFAULT '',
    updated_at   timestamptz NOT NULL DEFAULT now(),
    -- The stub's label is not a vendor model, in any row, ever.
    CONSTRAINT assist_model_aliases_no_stub_seed CHECK (adapter_kind = 'stub' OR model <> 'hos-stub-1')
);

INSERT INTO assist_model_aliases (alias, adapter_kind, base_url, model, enabled, licence, region) VALUES
    ('tanyai-main',    'stub',      '', '',                  false, '',                      ''),
    ('tanyai-alt',     'stub',      '', '',                  false, '',                      ''),
    ('tanyai-premium', 'anthropic', '', 'claude-sonnet-5-5', true,  'commercial API terms',  'us'),
    ('tanyai-ca',      'stub',      '', '',                  false, '',                      '')
ON CONFLICT (alias) DO NOTHING;

CREATE OR REPLACE FUNCTION assist_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION '% is append-only: % is refused', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TABLE IF NOT EXISTS assist_conversations (
    id          text PRIMARY KEY CHECK (id ~ '^cv_[0-9a-f]{32}$'),
    -- ON DELETE RESTRICT: a conversation is not deleted by removing a login.
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- The verified bearer's jti. A bearer with no jti never gets a row (the service is stateless).
    session_key text NOT NULL CHECK (session_key <> ''),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS assist_conversation_exchanges (
    seq             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conversation_id text NOT NULL REFERENCES assist_conversations (id) ON DELETE RESTRICT,
    question        text NOT NULL,
    answer          text NOT NULL,
    -- The structured half the resolver reads on the next turn, server written.
    ran             jsonb NOT NULL DEFAULT '[]'::jsonb,
    clarify         jsonb,
    at              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS assist_conversation_exchanges_conv_idx
    ON assist_conversation_exchanges (conversation_id, seq);

DROP TRIGGER IF EXISTS assist_conversations_append_only ON assist_conversations;
CREATE TRIGGER assist_conversations_append_only
    BEFORE UPDATE OR DELETE ON assist_conversations
    FOR EACH ROW EXECUTE FUNCTION assist_append_only();
DROP TRIGGER IF EXISTS assist_conversations_no_truncate ON assist_conversations;
CREATE TRIGGER assist_conversations_no_truncate
    BEFORE TRUNCATE ON assist_conversations
    FOR EACH STATEMENT EXECUTE FUNCTION assist_append_only();

DROP TRIGGER IF EXISTS assist_conversation_exchanges_append_only ON assist_conversation_exchanges;
CREATE TRIGGER assist_conversation_exchanges_append_only
    BEFORE UPDATE OR DELETE ON assist_conversation_exchanges
    FOR EACH ROW EXECUTE FUNCTION assist_append_only();
DROP TRIGGER IF EXISTS assist_conversation_exchanges_no_truncate ON assist_conversation_exchanges;
CREATE TRIGGER assist_conversation_exchanges_no_truncate
    BEFORE TRUNCATE ON assist_conversation_exchanges
    FOR EACH STATEMENT EXECUTE FUNCTION assist_append_only();

-- Item 3: the 0153 seed made harmless. Only the untouched seed value is rewritten.
UPDATE assist_settings SET value = 'tanyai-premium', updated_at = now()
 WHERE key = 'model' AND value = 'hos-stub-1';
