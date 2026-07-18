package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/amine123max/Mail/server/internal/model"
	"github.com/amine123max/Mail/server/internal/secure"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	box  *secure.Box
	Path string
}

func Open(dataDir string, box *secure.Box) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "mail.sqlite")
	dsn := "file:" + filepath.ToSlash(path) + "?_txlock=immediate&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	store := &Store{db: db, box: box, Path: path}
	if err := store.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) initialize(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		PRAGMA journal_mode = WAL;
		PRAGMA foreign_keys = ON;
		PRAGMA busy_timeout = 5000;

		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE COLLATE NOCASE,
			email_encrypted TEXT,
			email_hash TEXT,
			password_hash TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS guest_sessions (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS user_sessions (
			id TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS email_verifications (
			email_hash TEXT PRIMARY KEY,
			email_encrypted TEXT NOT NULL,
			code_hash TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS announcements (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			created_by INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(created_by) REFERENCES users(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS announcement_reads (
			announcement_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			read_at TEXT NOT NULL,
			PRIMARY KEY(announcement_id, user_id),
			FOREIGN KEY(announcement_id) REFERENCES announcements(id) ON DELETE CASCADE,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);
	`); err != nil {
		return err
	}
	if err := s.migrateUsers(ctx); err != nil {
		return err
	}
	if err := s.migrateAccounts(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_accounts_owner_sort ON accounts(owner_key, sort_order ASC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_guest_sessions_expiry ON guest_sessions(expires_at);
		CREATE INDEX IF NOT EXISTS idx_user_sessions_expiry ON user_sessions(expires_at);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_hash ON users(email_hash) WHERE email_hash IS NOT NULL;
		CREATE INDEX IF NOT EXISTS idx_email_verifications_expiry ON email_verifications(expires_at);
		CREATE INDEX IF NOT EXISTS idx_announcements_created ON announcements(created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_announcement_reads_user ON announcement_reads(user_id, announcement_id);
	`)
	return err
}

func (s *Store) migrateUsers(ctx context.Context) error {
	columns, err := tableColumns(ctx, s.db, "users")
	if err != nil {
		return err
	}
	statements := make([]string, 0, 3)
	if !columns["email_encrypted"] {
		statements = append(statements, "ALTER TABLE users ADD COLUMN email_encrypted TEXT")
	}
	if !columns["email_hash"] {
		statements = append(statements, "ALTER TABLE users ADD COLUMN email_hash TEXT")
	}
	if !columns["is_admin"] {
		statements = append(statements, "ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0")
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	var users, administrators int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&users); err != nil {
		return err
	}
	if users > 0 {
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE is_admin = 1").Scan(&administrators); err != nil {
			return err
		}
		if administrators == 0 {
			_, err = s.db.ExecContext(ctx, "UPDATE users SET is_admin = 1 WHERE id = (SELECT id FROM users ORDER BY id ASC LIMIT 1)")
		}
	}
	return err
}

func (s *Store) migrateAccounts(ctx context.Context) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='accounts'").Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		_, err := s.db.ExecContext(ctx, accountsSchema("accounts"))
		return err
	}
	columns, err := tableColumns(ctx, s.db, "accounts")
	if err != nil {
		return err
	}
	if !columns["owner_key"] || !columns["email_hash"] {
		if err := s.migratePlainAccountEmails(ctx, columns); err != nil {
			return err
		}
		columns, err = tableColumns(ctx, s.db, "accounts")
		if err != nil {
			return err
		}
	}
	if !columns["group_name"] {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE accounts ADD COLUMN group_name TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !columns["sort_order"] {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE accounts ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
		return s.backfillAccountOrder(ctx)
	}
	return nil
}

func (s *Store) migratePlainAccountEmails(ctx context.Context, columns map[string]bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, "SELECT id, "+columnOr(columns, "owner_key", "'user:1'")+", email, password_encrypted, client_id_encrypted, refresh_token_encrypted, remark, created_at, updated_at, last_sync_at FROM accounts ORDER BY id")
	if err != nil {
		return err
	}
	type legacy struct {
		id                                                                           int64
		owner, email, password, clientID, refreshToken, remark, createdAt, updatedAt string
		lastSync                                                                     sql.NullString
	}
	legacyRows := make([]legacy, 0)
	for rows.Next() {
		var item legacy
		if err := rows.Scan(&item.id, &item.owner, &item.email, &item.password, &item.clientID, &item.refreshToken, &item.remark, &item.createdAt, &item.updatedAt, &item.lastSync); err != nil {
			_ = rows.Close()
			return err
		}
		legacyRows = append(legacyRows, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE accounts RENAME TO accounts_plain_email"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, accountsSchema("accounts")); err != nil {
		return err
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO accounts
		(id, owner_key, email_encrypted, email_hash, password_encrypted, client_id_encrypted,
		 refresh_token_encrypted, remark, group_name, sort_order, created_at, updated_at, last_sync_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for index, item := range legacyRows {
		encryptedEmail, err := s.box.Encrypt(item.email)
		if err != nil {
			return err
		}
		if _, err := statement.ExecContext(ctx, item.id, item.owner, encryptedEmail, s.box.BlindIndex(item.email), item.password, item.clientID, item.refreshToken, item.remark, index, item.createdAt, item.updatedAt, nullableString(item.lastSync)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE accounts_plain_email"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) backfillAccountOrder(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT owner_key, id FROM accounts ORDER BY owner_key, updated_at DESC, id DESC")
	if err != nil {
		return err
	}
	defer rows.Close()
	owners := make(map[string][]int64)
	for rows.Next() {
		var owner string
		var id int64
		if err := rows.Scan(&owner, &id); err != nil {
			return err
		}
		owners[owner] = append(owners[owner], id)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for owner, ids := range owners {
		for index, id := range ids {
			if _, err := tx.ExecContext(ctx, "UPDATE accounts SET sort_order=? WHERE owner_key=? AND id=?", index, owner, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func tableColumns(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func accountsSchema(name string) string {
	return fmt.Sprintf(`CREATE TABLE %s (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		owner_key TEXT NOT NULL,
		email_encrypted TEXT NOT NULL,
		email_hash TEXT NOT NULL,
		password_encrypted TEXT NOT NULL,
		client_id_encrypted TEXT NOT NULL,
		refresh_token_encrypted TEXT NOT NULL,
		remark TEXT NOT NULL DEFAULT '',
		group_name TEXT NOT NULL DEFAULT '',
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_sync_at TEXT,
		UNIQUE(owner_key, email_hash)
	)`, name)
}

func columnOr(columns map[string]bool, column, fallback string) string {
	if columns[column] {
		return column
	}
	return fallback
}

func nullableString(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func scanStoredAccount(scanner interface{ Scan(...any) error }) (model.StoredAccount, error) {
	var row model.StoredAccount
	var lastSync sql.NullString
	err := scanner.Scan(
		&row.ID, &row.OwnerKey, &row.EmailEncrypted, &row.EmailHash,
		&row.PasswordEncrypted, &row.ClientIDEncrypted, &row.RefreshTokenEncrypted,
		&row.Remark, &row.GroupName, &row.SortOrder, &row.CreatedAt, &row.UpdatedAt, &lastSync,
	)
	if lastSync.Valid {
		row.LastSyncAt = &lastSync.String
	}
	return row, err
}

const accountColumns = `id, owner_key, email_encrypted, email_hash, password_encrypted,
	client_id_encrypted, refresh_token_encrypted, remark, group_name, sort_order,
	created_at, updated_at, last_sync_at`

func uniquePositiveIDs(ids []int64) ([]int64, bool) {
	if len(ids) == 0 {
		return nil, false
	}
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id < 1 {
			return nil, false
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, true
}

func sortedKeys(values map[string][]int64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var ErrSetupCompleted = errors.New("SETUP_ALREADY_COMPLETED")
