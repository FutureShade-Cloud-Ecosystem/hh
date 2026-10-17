-- Migration 0403: extend msg_attachments for room files and photos.
-- Supports listing all files and photos shared in a room (channel, group, or DM),
-- whether bound to a specific message or uploaded directly to the room.

ALTER TABLE msg_attachments ALTER COLUMN message_id DROP NOT NULL;
ALTER TABLE msg_attachments ADD COLUMN IF NOT EXISTS channel_id UUID REFERENCES msg_channels(id) ON DELETE CASCADE;
ALTER TABLE msg_attachments ADD COLUMN IF NOT EXISTS attachment_id UUID;
ALTER TABLE msg_attachments ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'document' CHECK (kind IN ('photo', 'document'));
ALTER TABLE msg_attachments ADD COLUMN IF NOT EXISTS uploaded_by UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_msg_attachments_channel_created ON msg_attachments(channel_id, created_at DESC, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_msg_attachments_channel_attachment ON msg_attachments(channel_id, attachment_id) WHERE attachment_id IS NOT NULL;
