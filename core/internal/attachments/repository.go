package attachments

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("attachment not found")
	ErrNoDB     = errors.New("no database configured")
)

// AttachmentMeta carries metadata returned across the module boundary.
type AttachmentMeta struct {
	ID             string
	AppwriteFileID string
	Bucket         string
	Kind           string
	Mime           string
	SizeBytes      int64
}

// AttachmentCreateInput is the validated input to create an attachment record.
type AttachmentCreateInput struct {
	Kind           string
	AppwriteFileID string
	Bucket         string
	Mime           string
	SizeBytes      *int64
	Checksum       string
	TenantID       *string
}

// Repository handles database operations on attachment and attachment_link tables.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository constructs a repository over the pgxpool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// CreateAttachment inserts an attachment record for the caller's org.
func (r *Repository) CreateAttachment(ctx context.Context, orgID, createdBy string, in AttachmentCreateInput) (Attachment, error) {
	if r.pool == nil {
		return Attachment{}, ErrNoDB
	}
	var att Attachment
	var mime, checksum, cBy string
	var tid *string
	row := r.pool.QueryRow(ctx, `
		INSERT INTO attachment (
			org_id, tenant_id, kind, appwrite_file_id, bucket, mime, size_bytes, checksum, created_by, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now()
		) RETURNING id::text, org_id::text, tenant_id::text, kind, appwrite_file_id, bucket,
		            COALESCE(mime, ''), size_bytes, COALESCE(checksum, ''), COALESCE(created_by, ''), created_at, deleted_at`,
		orgID, in.TenantID, in.Kind, in.AppwriteFileID, in.Bucket, in.Mime, in.SizeBytes, in.Checksum, createdBy,
	)
	if err := row.Scan(&att.ID, &att.OrgID, &tid, &att.Kind, &att.AppwriteFileID, &att.Bucket,
		&mime, &att.SizeBytes, &checksum, &cBy, &att.CreatedAt, &att.DeletedAt); err != nil {
		return Attachment{}, fmt.Errorf("create attachment: %w", err)
	}
	att.TenantID = tid
	att.Mime = mime
	att.Checksum = checksum
	att.CreatedBy = cBy
	att.Links = []Link{}
	return att, nil
}

// GetAttachment loads one attachment by ID in the caller's org.
func (r *Repository) GetAttachment(ctx context.Context, orgID, id string) (Attachment, error) {
	if r.pool == nil {
		return Attachment{}, ErrNoDB
	}
	var att Attachment
	var mime, checksum, cBy string
	var tid *string
	row := r.pool.QueryRow(ctx, `
		SELECT id::text, org_id::text, tenant_id::text, kind, appwrite_file_id, bucket,
		       COALESCE(mime, ''), size_bytes, COALESCE(checksum, ''), COALESCE(created_by, ''), created_at, deleted_at
		FROM attachment
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL`,
		id, orgID,
	)
	if err := row.Scan(&att.ID, &att.OrgID, &tid, &att.Kind, &att.AppwriteFileID, &att.Bucket,
		&mime, &att.SizeBytes, &checksum, &cBy, &att.CreatedAt, &att.DeletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Attachment{}, ErrNotFound
		}
		return Attachment{}, fmt.Errorf("get attachment: %w", err)
	}
	att.TenantID = tid
	att.Mime = mime
	att.Checksum = checksum
	att.CreatedBy = cBy
	return att, nil
}

// GetLinks returns all links for an attachment.
func (r *Repository) GetLinks(ctx context.Context, attachmentID string) ([]Link, error) {
	if r.pool == nil {
		return nil, ErrNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT attachment_id::text, owner_kind, owner_id::text, COALESCE(role, '')
		FROM attachment_link
		WHERE attachment_id = $1
		ORDER BY created_at ASC`,
		attachmentID,
	)
	if err != nil {
		return nil, fmt.Errorf("get attachment links: %w", err)
	}
	defer rows.Close()

	out := make([]Link, 0)
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.AttachmentID, &l.OwnerKind, &l.OwnerID, &l.Role); err != nil {
			return nil, fmt.Errorf("scan attachment link: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CreateLink creates one polymorphic attachment link.
func (r *Repository) CreateLink(ctx context.Context, link Link) (Link, error) {
	if r.pool == nil {
		return Link{}, ErrNoDB
	}
	var out Link
	row := r.pool.QueryRow(ctx, `
		INSERT INTO attachment_link (attachment_id, owner_kind, owner_id, role)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (attachment_id, owner_kind, owner_id, role) DO NOTHING
		RETURNING attachment_id::text, owner_kind, owner_id::text, COALESCE(role, '')`,
		link.AttachmentID, link.OwnerKind, link.OwnerID, link.Role,
	)
	if err := row.Scan(&out.AttachmentID, &out.OwnerKind, &out.OwnerID, &out.Role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return link, nil
		}
		return Link{}, fmt.Errorf("create attachment link: %w", err)
	}
	return out, nil
}

// VerifyRoomAttachment checks whether an attachment exists in the org and is linked to the room.
func (r *Repository) VerifyRoomAttachment(ctx context.Context, orgID, attachmentID, channelID string) (*AttachmentMeta, error) {
	if r.pool == nil {
		return nil, ErrNoDB
	}
	var meta AttachmentMeta
	row := r.pool.QueryRow(ctx, `
		SELECT a.id::text, a.appwrite_file_id, a.bucket, a.kind, COALESCE(a.mime, ''), COALESCE(a.size_bytes, 0)
		FROM attachment a
		JOIN attachment_link l ON l.attachment_id = a.id
		WHERE a.id = $1 AND a.org_id = $2 AND a.deleted_at IS NULL
		  AND l.owner_kind = 'channel' AND l.owner_id = $3
		LIMIT 1`,
		attachmentID, orgID, channelID,
	)
	if err := row.Scan(&meta.ID, &meta.AppwriteFileID, &meta.Bucket, &meta.Kind, &meta.Mime, &meta.SizeBytes); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("verify room attachment: %w", err)
	}
	return &meta, nil
}

// ResolveAttachmentMetas resolves metadata for a list of attachment IDs in the org.
func (r *Repository) ResolveAttachmentMetas(ctx context.Context, orgID string, attachmentIDs []string) (map[string]AttachmentMeta, error) {
	if r.pool == nil || len(attachmentIDs) == 0 {
		return map[string]AttachmentMeta{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, appwrite_file_id, bucket, kind, COALESCE(mime, ''), COALESCE(size_bytes, 0)
		FROM attachment
		WHERE id = ANY($1) AND org_id = $2 AND deleted_at IS NULL`,
		attachmentIDs, orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve attachment metas: %w", err)
	}
	defer rows.Close()

	out := make(map[string]AttachmentMeta, len(attachmentIDs))
	for rows.Next() {
		var meta AttachmentMeta
		if err := rows.Scan(&meta.ID, &meta.AppwriteFileID, &meta.Bucket, &meta.Kind, &meta.Mime, &meta.SizeBytes); err != nil {
			return nil, fmt.Errorf("scan attachment meta: %w", err)
		}
		out[meta.ID] = meta
	}
	return out, rows.Err()
}
