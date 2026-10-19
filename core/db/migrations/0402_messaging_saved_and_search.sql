-- 0402: per-person saved messages for House Messages search and saved views.
CREATE TABLE msg_saved_messages (
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  message_id UUID NOT NULL REFERENCES msg_messages(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES msg_channels(id) ON DELETE CASCADE,
  saved_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (user_id, message_id)
);
CREATE INDEX idx_msg_saved_user_time ON msg_saved_messages(user_id, saved_at DESC);
