package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/amine123max/Mail/server/internal/model"
)

type DesktopRefreshStatus int

const (
	DesktopRefreshRotated DesktopRefreshStatus = iota
	DesktopRefreshInvalid
	DesktopRefreshExpired
	DesktopRefreshReplayed
)

func (s *Store) CreateDesktopSession(ctx context.Context, session model.DesktopSession, refreshTokenHash string, tokenExpiresAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := nowISO()
	if _, err := tx.ExecContext(ctx, `UPDATE desktop_sessions
		SET revoked_at=?, revoke_reason='DEVICE_REPLACED'
		WHERE user_id=? AND device_id=? AND revoked_at IS NULL`, now, session.UserID, session.DeviceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO desktop_sessions
		(id,family_id,device_id,user_id,device_name,client_version,created_at,last_used_at,idle_expires_at,absolute_expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, session.ID, session.FamilyID, session.DeviceID, session.UserID, session.DeviceName, session.ClientVersion,
		session.CreatedAt, session.LastUsedAt, session.IdleExpiresAt, session.AbsoluteExpiresAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO desktop_session_tokens(token_hash,session_id,created_at,expires_at)
		VALUES(?,?,?,?)`, refreshTokenHash, session.ID, now, tokenExpiresAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RotateDesktopRefreshToken(ctx context.Context, currentHash, replacementHash string, now, idleExpiresAt, tokenExpiresAt time.Time) (model.DesktopSession, DesktopRefreshStatus, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.DesktopSession{}, DesktopRefreshInvalid, err
	}
	defer func() { _ = tx.Rollback() }()
	var session model.DesktopSession
	var tokenExpires string
	var tokenUsed, revokedAt, revokeReason sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT s.id,s.family_id,s.device_id,s.user_id,s.device_name,s.client_version,
		s.created_at,s.last_used_at,s.idle_expires_at,s.absolute_expires_at,s.revoked_at,s.revoke_reason,
		t.expires_at,t.used_at
		FROM desktop_session_tokens t JOIN desktop_sessions s ON s.id=t.session_id
		WHERE t.token_hash=?`, currentHash).Scan(
		&session.ID, &session.FamilyID, &session.DeviceID, &session.UserID, &session.DeviceName, &session.ClientVersion,
		&session.CreatedAt, &session.LastUsedAt, &session.IdleExpiresAt, &session.AbsoluteExpiresAt, &revokedAt, &revokeReason,
		&tokenExpires, &tokenUsed,
	)
	if err == sql.ErrNoRows {
		return model.DesktopSession{}, DesktopRefreshInvalid, nil
	}
	if err != nil {
		return model.DesktopSession{}, DesktopRefreshInvalid, err
	}
	if tokenUsed.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE desktop_sessions SET revoked_at=?,revoke_reason='REFRESH_TOKEN_REPLAY'
			WHERE family_id=? AND revoked_at IS NULL`, now.UTC().Format(time.RFC3339Nano), session.FamilyID); err != nil {
			return model.DesktopSession{}, DesktopRefreshInvalid, err
		}
		if err := tx.Commit(); err != nil {
			return model.DesktopSession{}, DesktopRefreshInvalid, err
		}
		return model.DesktopSession{}, DesktopRefreshReplayed, nil
	}
	absoluteExpires, absoluteErr := parseTime(session.AbsoluteExpiresAt)
	idleExpires, idleErr := parseTime(session.IdleExpiresAt)
	refreshExpires, refreshErr := parseTime(tokenExpires)
	if revokedAt.Valid || absoluteErr != nil || idleErr != nil || refreshErr != nil || !absoluteExpires.After(now) || !idleExpires.After(now) || !refreshExpires.After(now) {
		if !revokedAt.Valid {
			_, _ = tx.ExecContext(ctx, `UPDATE desktop_sessions SET revoked_at=?,revoke_reason='SESSION_EXPIRED'
				WHERE id=? AND revoked_at IS NULL`, now.UTC().Format(time.RFC3339Nano), session.ID)
		}
		if err := tx.Commit(); err != nil {
			return model.DesktopSession{}, DesktopRefreshInvalid, err
		}
		return model.DesktopSession{}, DesktopRefreshExpired, nil
	}
	nowText := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, "UPDATE desktop_session_tokens SET used_at=? WHERE token_hash=? AND used_at IS NULL", nowText, currentHash); err != nil {
		return model.DesktopSession{}, DesktopRefreshInvalid, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO desktop_session_tokens(token_hash,session_id,created_at,expires_at)
		VALUES(?,?,?,?)`, replacementHash, session.ID, nowText, tokenExpiresAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return model.DesktopSession{}, DesktopRefreshInvalid, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE desktop_sessions SET last_used_at=?,idle_expires_at=? WHERE id=?`, nowText, idleExpiresAt.UTC().Format(time.RFC3339Nano), session.ID); err != nil {
		return model.DesktopSession{}, DesktopRefreshInvalid, err
	}
	if err := tx.Commit(); err != nil {
		return model.DesktopSession{}, DesktopRefreshInvalid, err
	}
	session.LastUsedAt = nowText
	session.IdleExpiresAt = idleExpiresAt.UTC().Format(time.RFC3339Nano)
	return session, DesktopRefreshRotated, nil
}

func (s *Store) DesktopSessionActive(ctx context.Context, sessionID string, userID int64, now time.Time) (bool, error) {
	var absoluteExpiresAt, idleExpiresAt string
	err := s.db.QueryRowContext(ctx, `SELECT absolute_expires_at,idle_expires_at FROM desktop_sessions
		WHERE id=? AND user_id=? AND revoked_at IS NULL`, sessionID, userID).Scan(&absoluteExpiresAt, &idleExpiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	absolute, err := parseTime(absoluteExpiresAt)
	if err != nil {
		return false, err
	}
	idle, err := parseTime(idleExpiresAt)
	return err == nil && absolute.After(now) && idle.After(now), err
}

func (s *Store) RevokeDesktopSession(ctx context.Context, sessionID string, userID int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE desktop_sessions SET revoked_at=?,revoke_reason=?
		WHERE id=? AND user_id=? AND revoked_at IS NULL`, nowISO(), reason, sessionID, userID)
	return err
}

func (s *Store) RevokeDesktopDevice(ctx context.Context, userID int64, deviceID, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE desktop_sessions SET revoked_at=?,revoke_reason=?
		WHERE user_id=? AND device_id=? AND revoked_at IS NULL`, nowISO(), reason, userID, deviceID)
	return err
}

func (s *Store) RevokeAllDesktopSessions(ctx context.Context, userID int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE desktop_sessions SET revoked_at=?,revoke_reason=?
		WHERE user_id=? AND revoked_at IS NULL`, nowISO(), reason, userID)
	return err
}

func (s *Store) ListDesktopDevices(ctx context.Context, userID int64, currentSessionID string) ([]model.DesktopDeviceSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT device_id,device_name,client_version,created_at,last_used_at,
		CASE WHEN idle_expires_at<absolute_expires_at THEN idle_expires_at ELSE absolute_expires_at END,
		revoked_at,id
		FROM desktop_sessions WHERE user_id=? ORDER BY last_used_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devices := make([]model.DesktopDeviceSummary, 0)
	for rows.Next() {
		var device model.DesktopDeviceSummary
		var revokedAt sql.NullString
		var sessionID string
		if err := rows.Scan(&device.DeviceId, &device.DeviceName, &device.ClientVersion, &device.CreatedAt, &device.LastUsedAt, &device.ExpiresAt, &revokedAt, &sessionID); err != nil {
			return nil, err
		}
		if revokedAt.Valid {
			device.RevokedAt = &revokedAt.String
		}
		device.Current = sessionID == currentSessionID
		devices = append(devices, device)
	}
	return devices, rows.Err()
}
