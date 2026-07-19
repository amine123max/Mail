package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrMailOperationInProgress = errors.New("mail operation is already in progress")
var ErrMailOperationConflict = errors.New("mail operation id was reused with different input")

type MailOperationClaim string

const (
	MailOperationClaimed   MailOperationClaim = "claimed"
	MailOperationCompleted MailOperationClaim = "completed"
)

func (s *Store) ClaimMailOperation(ctx context.Context, ownerKey, operationID, kind, requestHash string) (MailOperationClaim, error) {
	if ownerKey == "" || operationID == "" || kind == "" || requestHash == "" {
		return "", errors.New("mail operation parameters are invalid")
	}
	now := nowISO()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO mail_operations(owner_key,operation_id,kind,request_hash,status,created_at,updated_at)
		VALUES(?,?,?,?,'pending',?,?)`, ownerKey, operationID, kind, requestHash, now, now)
	if err != nil {
		return "", err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if inserted == 1 {
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return MailOperationClaimed, nil
	}
	var storedKind, storedHash, status, updatedAt string
	if err := tx.QueryRowContext(ctx, `SELECT kind,request_hash,status,updated_at FROM mail_operations
		WHERE owner_key=? AND operation_id=?`, ownerKey, operationID).Scan(&storedKind, &storedHash, &status, &updatedAt); err != nil {
		return "", err
	}
	if storedKind != kind || storedHash != requestHash {
		return "", ErrMailOperationConflict
	}
	if status == "completed" {
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return MailOperationCompleted, nil
	}
	staleBefore := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339Nano)
	if status == "pending" && updatedAt < staleBefore {
		result, err := tx.ExecContext(ctx, `UPDATE mail_operations SET updated_at=?
			WHERE owner_key=? AND operation_id=? AND status='pending' AND updated_at=?`, now, ownerKey, operationID, updatedAt)
		if err != nil {
			return "", err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return "", err
		}
		if updated == 1 {
			if err := tx.Commit(); err != nil {
				return "", err
			}
			return MailOperationClaimed, nil
		}
	}
	return "", ErrMailOperationInProgress
}

func (s *Store) CompleteMailOperation(ctx context.Context, ownerKey, operationID string) error {
	return s.completeMailOperation(ctx, ownerKey, operationID, nil)
}

func (s *Store) StartMailOperation(ctx context.Context, ownerKey, operationID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE mail_operations SET status='running',updated_at=?
		WHERE owner_key=? AND operation_id=? AND status='pending'`, nowISO(), ownerKey, operationID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CompleteMailOperationWithResult(ctx context.Context, ownerKey, operationID, resultJSON string) error {
	encrypted, err := s.box.Encrypt(resultJSON)
	if err != nil {
		return err
	}
	return s.completeMailOperation(ctx, ownerKey, operationID, &encrypted)
}

func (s *Store) completeMailOperation(ctx context.Context, ownerKey, operationID string, resultEncrypted *string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE mail_operations SET status='completed',result_encrypted=?,updated_at=?
		WHERE owner_key=? AND operation_id=? AND status IN ('pending','running')`, resultEncrypted, nowISO(), ownerKey, operationID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return sql.ErrNoRows
	}
	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(time.RFC3339Nano)
	_, _ = s.db.ExecContext(ctx, "DELETE FROM mail_operations WHERE updated_at<?", cutoff)
	return nil
}

func (s *Store) MailOperationResult(ctx context.Context, ownerKey, operationID string) (string, error) {
	var encrypted sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT result_encrypted FROM mail_operations
		WHERE owner_key=? AND operation_id=? AND status='completed'`, ownerKey, operationID).Scan(&encrypted); err != nil {
		return "", err
	}
	if !encrypted.Valid || encrypted.String == "" {
		return "", sql.ErrNoRows
	}
	return s.box.Decrypt(encrypted.String)
}

func (s *Store) ReleaseMailOperation(ctx context.Context, ownerKey, operationID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mail_operations
		WHERE owner_key=? AND operation_id=? AND status IN ('pending','running')`, ownerKey, operationID)
	return err
}
