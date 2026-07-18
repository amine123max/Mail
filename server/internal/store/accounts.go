package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/amine123max/Mail/server/internal/model"
)

type ImportResult struct {
	Inserted int `json:"inserted"`
	Updated  int `json:"updated"`
	Skipped  int `json:"skipped"`
}

type AccountChanges struct {
	Remark       *string
	Group        *string
	RefreshToken *string
	LastSync     bool
}

func (s *Store) ListAccounts(ctx context.Context, ownerKey string) ([]model.PublicAccount, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+accountColumns+" FROM accounts WHERE owner_key=? ORDER BY sort_order ASC, id DESC", ownerKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.PublicAccount, 0)
	for rows.Next() {
		row, err := scanStoredAccount(rows)
		if err != nil {
			return nil, err
		}
		public, err := s.publicAccount(row)
		if err != nil {
			return nil, err
		}
		result = append(result, public)
	}
	return result, rows.Err()
}

func (s *Store) GetAccountCredentials(ctx context.Context, ownerKey string, id int64) (*model.AccountCredentials, error) {
	row, err := scanStoredAccount(s.db.QueryRowContext(ctx, "SELECT "+accountColumns+" FROM accounts WHERE owner_key=? AND id=?", ownerKey, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	email, err := s.box.Decrypt(row.EmailEncrypted)
	if err != nil {
		return nil, err
	}
	password, err := s.box.Decrypt(row.PasswordEncrypted)
	if err != nil {
		return nil, err
	}
	clientID, err := s.box.Decrypt(row.ClientIDEncrypted)
	if err != nil {
		return nil, err
	}
	refreshToken, err := s.box.Decrypt(row.RefreshTokenEncrypted)
	if err != nil {
		return nil, err
	}
	return &model.AccountCredentials{
		ID: id, OwnerKey: ownerKey, Email: email, Password: password,
		ClientID: clientID, RefreshToken: refreshToken, Remark: row.Remark,
	}, nil
}

func (s *Store) ImportAccounts(ctx context.Context, ownerKey string, accounts []model.ImportedAccount, mode string) (ImportResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result := ImportResult{}
	for _, account := range accounts {
		emailHash := s.box.BlindIndex(account.Email)
		var existingID int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM accounts WHERE owner_key=? AND email_hash=?", ownerKey, emailHash).Scan(&existingID)
		exists := err == nil
		if err != nil && err != sql.ErrNoRows {
			return result, err
		}
		if exists && mode == "skip" {
			result.Skipped++
			continue
		}
		email, password, clientID, refreshToken, err := s.encryptImported(account)
		if err != nil {
			return result, err
		}
		if exists {
			_, err = tx.ExecContext(ctx, `UPDATE accounts SET email_encrypted=?, password_encrypted=?,
				client_id_encrypted=?, refresh_token_encrypted=?, remark=?, updated_at=?
				WHERE owner_key=? AND email_hash=?`, email, password, clientID, refreshToken,
				account.Remark, nowISO(), ownerKey, emailHash)
			if err != nil {
				return result, err
			}
			result.Updated++
			continue
		}
		var order int
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sort_order),-1)+1 FROM accounts WHERE owner_key=?", ownerKey).Scan(&order); err != nil {
			return result, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO accounts
			(owner_key,email_encrypted,email_hash,password_encrypted,client_id_encrypted,refresh_token_encrypted,remark,sort_order)
			VALUES(?,?,?,?,?,?,?,?)`, ownerKey, email, emailHash, password, clientID, refreshToken, account.Remark, order)
		if err != nil {
			return result, err
		}
		result.Inserted++
	}
	return result, tx.Commit()
}

func (s *Store) UpdateAccount(ctx context.Context, ownerKey string, id int64, changes AccountChanges) (*model.PublicAccount, error) {
	row, err := scanStoredAccount(s.db.QueryRowContext(ctx, "SELECT "+accountColumns+" FROM accounts WHERE owner_key=? AND id=?", ownerKey, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	remark, groupName, refreshEncrypted := row.Remark, row.GroupName, row.RefreshTokenEncrypted
	if changes.Remark != nil {
		remark = *changes.Remark
	}
	if changes.Group != nil {
		groupName = *changes.Group
	}
	if changes.RefreshToken != nil {
		refreshEncrypted, err = s.box.Encrypt(*changes.RefreshToken)
		if err != nil {
			return nil, err
		}
	}
	var lastSync any
	if row.LastSyncAt != nil {
		lastSync = *row.LastSyncAt
	}
	if changes.LastSync {
		lastSync = nowISO()
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE accounts SET remark=?, group_name=?, refresh_token_encrypted=?,
		last_sync_at=?, updated_at=? WHERE owner_key=? AND id=?`, remark, groupName, refreshEncrypted,
		lastSync, nowISO(), ownerKey, id); err != nil {
		return nil, err
	}
	updated, err := scanStoredAccount(s.db.QueryRowContext(ctx, "SELECT "+accountColumns+" FROM accounts WHERE owner_key=? AND id=?", ownerKey, id))
	if err != nil {
		return nil, err
	}
	public, err := s.publicAccount(updated)
	return &public, err
}

func (s *Store) UpdateRefreshToken(ctx context.Context, ownerKey string, id int64, refreshToken string) error {
	encrypted, err := s.box.Encrypt(refreshToken)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE accounts SET refresh_token_encrypted=?,updated_at=? WHERE owner_key=? AND id=?", encrypted, nowISO(), ownerKey, id)
	return err
}

func (s *Store) MarkAccountSynced(ctx context.Context, ownerKey string, id int64) error {
	now := nowISO()
	_, err := s.db.ExecContext(ctx, "UPDATE accounts SET last_sync_at=?,updated_at=? WHERE owner_key=? AND id=?", now, now, ownerKey, id)
	return err
}

func (s *Store) DeleteAccount(ctx context.Context, ownerKey string, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM accounts WHERE owner_key=? AND id=?", ownerKey, id)
	if err != nil {
		return false, err
	}
	changes, err := result.RowsAffected()
	return changes > 0, err
}

func (s *Store) SetAccountsGroup(ctx context.Context, ownerKey string, ids []int64, group string) (bool, error) {
	owned, err := s.ownedAccountIDs(ctx, ownerKey, ids)
	if err != nil || owned == nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range owned {
		if _, err := tx.ExecContext(ctx, "UPDATE accounts SET group_name=?,updated_at=? WHERE owner_key=? AND id=?", group, nowISO(), ownerKey, id); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func (s *Store) DeleteAccounts(ctx context.Context, ownerKey string, ids []int64) (*int, error) {
	owned, err := s.ownedAccountIDs(ctx, ownerKey, ids)
	if err != nil || owned == nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	deleted := 0
	for _, id := range owned {
		result, err := tx.ExecContext(ctx, "DELETE FROM accounts WHERE owner_key=? AND id=?", ownerKey, id)
		if err != nil {
			return nil, err
		}
		changes, _ := result.RowsAffected()
		deleted += int(changes)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &deleted, nil
}

func (s *Store) ReorderAccounts(ctx context.Context, ownerKey string, ids []int64) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id < 1 {
			return false, nil
		}
		if _, exists := seen[id]; exists {
			return false, nil
		}
		seen[id] = struct{}{}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM accounts WHERE owner_key=?", ownerKey)
	if err != nil {
		return false, err
	}
	owned := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return false, err
		}
		owned[id] = struct{}{}
	}
	_ = rows.Close()
	if len(owned) != len(ids) {
		return false, nil
	}
	for _, id := range ids {
		if _, exists := owned[id]; !exists {
			return false, nil
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	for index, id := range ids {
		if _, err := tx.ExecContext(ctx, "UPDATE accounts SET sort_order=? WHERE owner_key=? AND id=?", index, ownerKey, id); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func (s *Store) GetAccountCredentialsBatch(ctx context.Context, ownerKey string, ids []int64) ([]model.AccountCredentials, error) {
	unique, valid := uniquePositiveIDs(ids)
	if !valid {
		return []model.AccountCredentials{}, nil
	}
	result := make([]model.AccountCredentials, 0, len(unique))
	for _, id := range unique {
		account, err := s.GetAccountCredentials(ctx, ownerKey, id)
		if err != nil {
			return nil, err
		}
		if account != nil {
			result = append(result, *account)
		}
	}
	return result, nil
}

func (s *Store) ownedAccountIDs(ctx context.Context, ownerKey string, ids []int64) ([]int64, error) {
	unique, valid := uniquePositiveIDs(ids)
	if !valid {
		return nil, nil
	}
	arguments := []any{ownerKey}
	arguments = append(arguments, anySlice(unique)...)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT id FROM accounts WHERE owner_key=? AND id IN (%s)", placeholders(len(unique))), arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if count != len(unique) {
		return nil, nil
	}
	return unique, nil
}

func (s *Store) publicAccount(row model.StoredAccount) (model.PublicAccount, error) {
	email, err := s.box.Decrypt(row.EmailEncrypted)
	if err != nil {
		return model.PublicAccount{}, err
	}
	return model.PublicAccount{
		ID: row.ID, Email: email, Remark: row.Remark, Group: row.GroupName,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, LastSyncAt: row.LastSyncAt,
	}, nil
}

func (s *Store) encryptImported(account model.ImportedAccount) (string, string, string, string, error) {
	email, err := s.box.Encrypt(account.Email)
	if err != nil {
		return "", "", "", "", err
	}
	password, err := s.box.Encrypt(account.Password)
	if err != nil {
		return "", "", "", "", err
	}
	clientID, err := s.box.Encrypt(account.ClientID)
	if err != nil {
		return "", "", "", "", err
	}
	refreshToken, err := s.box.Encrypt(account.RefreshToken)
	return email, password, clientID, refreshToken, err
}

func nowISO() string { return time.Now().UTC().Format(time.RFC3339Nano) }
