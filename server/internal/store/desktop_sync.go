package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const desktopSyncCursorLifetime = 30 * 24 * time.Hour

type DesktopSyncCursor struct {
	Token     string
	OwnerKey  string
	AccountID int64
	Folder    string
	Provider  string
	StateJSON string
	CreatedAt string
	UpdatedAt string
}

func (s *Store) GetDesktopSyncCursor(ctx context.Context, ownerKey string, accountID int64, folder, token string) (*DesktopSyncCursor, error) {
	if ownerKey == "" || accountID < 1 || folder == "" || token == "" {
		return nil, nil
	}
	cutoff := time.Now().UTC().Add(-desktopSyncCursorLifetime).Format(time.RFC3339Nano)
	var cursor DesktopSyncCursor
	err := s.db.QueryRowContext(ctx, `SELECT token,owner_key,account_id,folder,provider,state_json,created_at,updated_at
		FROM desktop_sync_cursors WHERE token=? AND owner_key=? AND account_id=? AND folder=? AND updated_at>=?`,
		token, ownerKey, accountID, folder, cutoff,
	).Scan(&cursor.Token, &cursor.OwnerKey, &cursor.AccountID, &cursor.Folder, &cursor.Provider, &cursor.StateJSON, &cursor.CreatedAt, &cursor.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &cursor, err
}

func (s *Store) CommitDesktopSyncCursor(ctx context.Context, ownerKey string, accountID int64, folder, provider, stateJSON string, markSynced bool) (string, *string, error) {
	if ownerKey == "" || accountID < 1 || folder == "" || len(folder) > 1000 || provider == "" || len(provider) > 20 || stateJSON == "" || len(stateJSON) > 16<<20 {
		return "", nil, errors.New("desktop sync cursor parameters are invalid")
	}
	token, err := newDesktopSyncCursorToken()
	if err != nil {
		return "", nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO desktop_sync_cursors(token,owner_key,account_id,folder,provider,state_json,created_at,updated_at)
		SELECT ?,?,?,?,?,?,?,? FROM accounts WHERE id=? AND owner_key=?`, token, ownerKey, accountID, folder, provider, stateJSON, now, now, accountID, ownerKey)
	if err != nil {
		return "", nil, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return "", nil, err
	}
	if inserted != 1 {
		return "", nil, fmt.Errorf("desktop sync account not found")
	}
	var lastSyncAt *string
	if markSynced {
		result, err := tx.ExecContext(ctx, "UPDATE accounts SET last_sync_at=?,updated_at=? WHERE owner_key=? AND id=?", now, now, ownerKey, accountID)
		if err != nil {
			return "", nil, err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return "", nil, err
		}
		if updated != 1 {
			return "", nil, fmt.Errorf("desktop sync account not found")
		}
		lastSyncAt = &now
	}
	cutoff := time.Now().UTC().Add(-desktopSyncCursorLifetime).Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, "DELETE FROM desktop_sync_cursors WHERE updated_at<?", cutoff); err != nil {
		return "", nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM desktop_sync_cursors
		WHERE owner_key=? AND account_id=? AND folder=? AND token NOT IN (
			SELECT token FROM desktop_sync_cursors WHERE owner_key=? AND account_id=? AND folder=? ORDER BY updated_at DESC LIMIT 8
		)`, ownerKey, accountID, folder, ownerKey, accountID, folder); err != nil {
		return "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, err
	}
	return token, lastSyncAt, nil
}

func (s *Store) DeleteDesktopSyncCursor(ctx context.Context, ownerKey, token string) error {
	if strings.TrimSpace(ownerKey) == "" || strings.TrimSpace(token) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM desktop_sync_cursors WHERE owner_key=? AND token=?", ownerKey, token)
	return err
}

func newDesktopSyncCursorToken() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
