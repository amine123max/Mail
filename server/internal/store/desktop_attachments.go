package store

import (
	"context"
	"database/sql"
)

type DesktopAttachmentUpload struct {
	ID          string
	OwnerKey    string
	Filename    string
	ContentType string
	Size        int64
	SHA256      string
	CreatedAt   string
	ExpiresAt   string
}

func (s *Store) CreateDesktopAttachmentUpload(ctx context.Context, upload DesktopAttachmentUpload) error {
	filename, err := s.box.Encrypt(upload.Filename)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO desktop_attachment_uploads
		(id, owner_key, filename_encrypted, content_type, size, sha256, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		upload.ID, upload.OwnerKey, filename, upload.ContentType, upload.Size, upload.SHA256, upload.CreatedAt, upload.ExpiresAt)
	return err
}

func (s *Store) DesktopAttachmentUpload(ctx context.Context, ownerKey, id string) (*DesktopAttachmentUpload, error) {
	var upload DesktopAttachmentUpload
	var filename string
	err := s.db.QueryRowContext(ctx, `SELECT id, owner_key, filename_encrypted, content_type, size, sha256, created_at, expires_at
		FROM desktop_attachment_uploads WHERE owner_key = ? AND id = ?`, ownerKey, id).Scan(
		&upload.ID, &upload.OwnerKey, &filename, &upload.ContentType, &upload.Size, &upload.SHA256, &upload.CreatedAt, &upload.ExpiresAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	upload.Filename, err = s.box.Decrypt(filename)
	if err != nil {
		return nil, err
	}
	return &upload, nil
}

func (s *Store) DeleteDesktopAttachmentUpload(ctx context.Context, ownerKey, id string) (bool, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM desktop_attachment_uploads WHERE owner_key = ? AND id = ?", ownerKey, id)
	if err != nil {
		return false, err
	}
	deleted, err := result.RowsAffected()
	return deleted > 0, err
}

func (s *Store) DeleteExpiredDesktopAttachmentUploads(ctx context.Context, before string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM desktop_attachment_uploads WHERE expires_at <= ?", before)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return ids, nil
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM desktop_attachment_uploads WHERE expires_at <= ?", before)
	return ids, err
}
