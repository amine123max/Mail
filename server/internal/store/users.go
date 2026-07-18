package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/amine123max/Mail/server/internal/model"
)

type VerificationResult string

const (
	VerificationVerified         VerificationResult = "verified"
	VerificationMissing          VerificationResult = "missing"
	VerificationExpired          VerificationResult = "expired"
	VerificationInvalid          VerificationResult = "invalid"
	VerificationAttemptsExceeded VerificationResult = "attempts_exceeded"
)

func (s *Store) FindUserByUsername(ctx context.Context, username string) (*model.User, error) {
	return scanUserRow(s.db.QueryRowContext(ctx, "SELECT id,username,email_encrypted,email_hash,password_hash,is_admin,created_at FROM users WHERE username=?", username))
}

func (s *Store) FindUserByID(ctx context.Context, id int64) (*model.User, error) {
	return scanUserRow(s.db.QueryRowContext(ctx, "SELECT id,username,email_encrypted,email_hash,password_hash,is_admin,created_at FROM users WHERE id=?", id))
}

func (s *Store) FindUserByEmail(ctx context.Context, email string) (*model.User, error) {
	return scanUserRow(s.db.QueryRowContext(ctx, "SELECT id,username,email_encrypted,email_hash,password_hash,is_admin,created_at FROM users WHERE email_hash=?", s.box.BlindIndex(email)))
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash, email string, administrator bool) (*model.User, error) {
	return s.createUserWith(ctx, s.db, username, passwordHash, email, administrator)
}

func (s *Store) UpdateUserPassword(ctx context.Context, userID int64, passwordHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE id=?", passwordHash, userID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("user %d not found", userID)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM user_sessions WHERE user_id=?", userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) IsSetupRequired(ctx context.Context) (bool, error) {
	var present int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM users LIMIT 1").Scan(&present)
	if err == sql.ErrNoRows {
		return true, nil
	}
	return false, err
}

func (s *Store) CreateAdministrator(ctx context.Context, username, passwordHash, email string) (*model.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var present int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM users LIMIT 1").Scan(&present)
	if err == nil {
		return nil, ErrSetupCompleted
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	user, err := s.createUserWith(ctx, tx, username, passwordHash, email, true)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *Store) SaveEmailVerification(ctx context.Context, email, codeHash string, expiresAt time.Time) error {
	encryptedEmail, err := s.box.Encrypt(email)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO email_verifications
		(email_hash,email_encrypted,code_hash,attempts,created_at,expires_at)
		VALUES(?,?,?,0,?,?)
		ON CONFLICT(email_hash) DO UPDATE SET email_encrypted=excluded.email_encrypted,
		code_hash=excluded.code_hash,attempts=0,created_at=excluded.created_at,expires_at=excluded.expires_at`,
		s.box.BlindIndex(email), encryptedEmail, codeHash, nowISO(), expiresAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) CanSendEmailVerification(ctx context.Context, email string, cooldown time.Duration) (bool, error) {
	var createdAt string
	err := s.db.QueryRowContext(ctx, "SELECT created_at FROM email_verifications WHERE email_hash=?", s.box.BlindIndex(email)).Scan(&createdAt)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	created, err := parseTime(createdAt)
	if err != nil {
		return false, err
	}
	return !created.After(time.Now().UTC().Add(-cooldown)), nil
}

func (s *Store) ConsumeEmailVerification(ctx context.Context, email, codeHash string) (VerificationResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VerificationMissing, err
	}
	defer func() { _ = tx.Rollback() }()
	emailHash := s.box.BlindIndex(email)
	var storedHash, expiresAt string
	var attempts int
	err = tx.QueryRowContext(ctx, "SELECT code_hash,attempts,expires_at FROM email_verifications WHERE email_hash=?", emailHash).Scan(&storedHash, &attempts, &expiresAt)
	if err == sql.ErrNoRows {
		return VerificationMissing, nil
	}
	if err != nil {
		return VerificationMissing, err
	}
	expires, err := parseTime(expiresAt)
	if err != nil || !expires.After(time.Now().UTC()) {
		_, deleteErr := tx.ExecContext(ctx, "DELETE FROM email_verifications WHERE email_hash=?", emailHash)
		if deleteErr != nil {
			return VerificationExpired, deleteErr
		}
		return VerificationExpired, tx.Commit()
	}
	if attempts >= 5 {
		_, err = tx.ExecContext(ctx, "DELETE FROM email_verifications WHERE email_hash=?", emailHash)
		if err != nil {
			return VerificationAttemptsExceeded, err
		}
		return VerificationAttemptsExceeded, tx.Commit()
	}
	if storedHash != codeHash {
		attempts++
		if attempts >= 5 {
			_, err = tx.ExecContext(ctx, "DELETE FROM email_verifications WHERE email_hash=?", emailHash)
			if err != nil {
				return VerificationAttemptsExceeded, err
			}
			return VerificationAttemptsExceeded, tx.Commit()
		}
		_, err = tx.ExecContext(ctx, "UPDATE email_verifications SET attempts=? WHERE email_hash=?", attempts, emailHash)
		if err != nil {
			return VerificationInvalid, err
		}
		return VerificationInvalid, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM email_verifications WHERE email_hash=?", emailHash)
	if err != nil {
		return VerificationVerified, err
	}
	return VerificationVerified, tx.Commit()
}

func (s *Store) DeleteEmailVerification(ctx context.Context, email string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM email_verifications WHERE email_hash=?", s.box.BlindIndex(email))
	return err
}

func (s *Store) CreateGuestSession(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO guest_sessions(id,created_at,expires_at) VALUES(?,?,?)`, id, nowISO(), expiresAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) GuestSessionExists(ctx context.Context, id string) (bool, error) {
	var expiresAt string
	err := s.db.QueryRowContext(ctx, "SELECT expires_at FROM guest_sessions WHERE id=?", id).Scan(&expiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	expires, err := parseTime(expiresAt)
	return err == nil && expires.After(time.Now().UTC()), err
}

func (s *Store) DeleteGuestSession(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "DELETE FROM accounts WHERE owner_key=?", "guest:"+id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM guest_sessions WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) TransferGuestAccounts(ctx context.Context, guestID string, userID int64) (int, error) {
	guestOwner, userOwner := "guest:"+guestID, "user:"+formatInt(userID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM accounts WHERE owner_key=?", guestOwner).Scan(&count); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO accounts
		(owner_key,email_encrypted,email_hash,password_encrypted,client_id_encrypted,refresh_token_encrypted,remark,group_name,sort_order,created_at,updated_at,last_sync_at)
		SELECT ?,email_encrypted,email_hash,password_encrypted,client_id_encrypted,refresh_token_encrypted,remark,group_name,
		((SELECT COALESCE(MAX(sort_order),-1)+1 FROM accounts WHERE owner_key=?)+sort_order),created_at,?,last_sync_at
		FROM accounts WHERE owner_key=?
		ON CONFLICT(owner_key,email_hash) DO UPDATE SET email_encrypted=excluded.email_encrypted,
		password_encrypted=excluded.password_encrypted,client_id_encrypted=excluded.client_id_encrypted,
		refresh_token_encrypted=excluded.refresh_token_encrypted,remark=excluded.remark,group_name=excluded.group_name,updated_at=excluded.updated_at`,
		userOwner, userOwner, nowISO(), guestOwner); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM accounts WHERE owner_key=?", guestOwner); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM guest_sessions WHERE id=?", guestID); err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

func (s *Store) CreateUserSession(ctx context.Context, id string, userID int64, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO user_sessions(id,user_id,created_at,expires_at) VALUES(?,?,?,?)", id, userID, nowISO(), expiresAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) UserSessionExists(ctx context.Context, id string, userID int64) (bool, error) {
	var expiresAt string
	err := s.db.QueryRowContext(ctx, "SELECT expires_at FROM user_sessions WHERE id=? AND user_id=?", id, userID).Scan(&expiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	expires, err := parseTime(expiresAt)
	return err == nil && expires.After(time.Now().UTC()), err
}

func (s *Store) DeleteUserSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM user_sessions WHERE id=?", id)
	return err
}

func (s *Store) CleanupExpired(ctx context.Context) error {
	now := nowISO()
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM guest_sessions WHERE expires_at<=?", now)
	if err != nil {
		return err
	}
	guestIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		guestIDs = append(guestIDs, id)
	}
	_ = rows.Close()
	for _, id := range guestIDs {
		if err := s.DeleteGuestSession(ctx, id); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM user_sessions WHERE expires_at<=?", now); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM email_verifications WHERE expires_at<=? OR attempts>=5", now)
	return err
}

func (s *Store) createUserWith(ctx context.Context, executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, username, passwordHash, email string, administrator bool) (*model.User, error) {
	var encryptedEmail, emailHash any
	if email != "" {
		encrypted, err := s.box.Encrypt(email)
		if err != nil {
			return nil, err
		}
		encryptedEmail, emailHash = encrypted, s.box.BlindIndex(email)
	}
	result, err := executor.ExecContext(ctx, "INSERT INTO users(username,email_encrypted,email_hash,password_hash,is_admin) VALUES(?,?,?,?,?)", username, encryptedEmail, emailHash, passwordHash, boolInt(administrator))
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if tx, ok := executor.(*sql.Tx); ok {
		return scanUserRow(tx.QueryRowContext(ctx, "SELECT id,username,email_encrypted,email_hash,password_hash,is_admin,created_at FROM users WHERE id=?", id))
	}
	return s.FindUserByID(ctx, id)
}

func scanUserRow(scanner interface{ Scan(...any) error }) (*model.User, error) {
	var user model.User
	var emailEncrypted, emailHash sql.NullString
	var isAdmin int
	err := scanner.Scan(&user.ID, &user.Username, &emailEncrypted, &emailHash, &user.PasswordHash, &isAdmin, &user.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if emailEncrypted.Valid {
		user.EmailEncrypted = &emailEncrypted.String
	}
	if emailHash.Valid {
		user.EmailHash = &emailHash.String
	}
	user.IsAdmin = isAdmin != 0
	return &user, nil
}

func parseTime(value string) (time.Time, error) {
	formats := []string{time.RFC3339Nano, "2006-01-02 15:04:05"}
	var last error
	for _, format := range formats {
		parsed, err := time.Parse(format, value)
		if err == nil {
			return parsed, nil
		}
		last = err
	}
	return time.Time{}, last
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatInt(value int64) string { return fmt.Sprintf("%d", value) }
