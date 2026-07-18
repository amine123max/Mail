package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amine123max/Mail/internal/model"
	"github.com/amine123max/Mail/internal/secure"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	box, err := secure.New(dir, strings.Repeat("02", 32), false)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := Open(dir, box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return storage
}

func TestOwnerIsolationAndEncryptedPersistence(t *testing.T) {
	storage := openTestStore(t)
	ctx := context.Background()
	alpha := model.ImportedAccount{Email: "alpha@example.invalid", Password: "alpha-password", ClientID: "alpha-client", RefreshToken: "alpha-refresh-token"}
	beta := model.ImportedAccount{Email: "beta@example.invalid", Password: "beta-password", ClientID: "beta-client", RefreshToken: "beta-refresh-token"}
	if _, err := storage.ImportAccounts(ctx, "user:1", []model.ImportedAccount{alpha}, "skip"); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.ImportAccounts(ctx, "user:2", []model.ImportedAccount{beta}, "skip"); err != nil {
		t.Fatal(err)
	}
	alphaList, _ := storage.ListAccounts(ctx, "user:1")
	betaList, _ := storage.ListAccounts(ctx, "user:2")
	if len(alphaList) != 1 || alphaList[0].Email != alpha.Email || len(betaList) != 1 || betaList[0].Email != beta.Email {
		t.Fatalf("owner isolation failed: %#v %#v", alphaList, betaList)
	}
	foreign, err := storage.GetAccountCredentials(ctx, "user:2", alphaList[0].ID)
	if err != nil || foreign != nil {
		t.Fatal("foreign account was accessible")
	}
	var emailEncrypted, passwordEncrypted string
	if err := storage.DB().QueryRow("SELECT email_encrypted,password_encrypted FROM accounts WHERE owner_key='user:1'").Scan(&emailEncrypted, &passwordEncrypted); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(emailEncrypted, "v1:") || !strings.HasPrefix(passwordEncrypted, "v1:") || strings.Contains(emailEncrypted, alpha.Email) || strings.Contains(passwordEncrypted, alpha.Password) {
		t.Fatal("credentials were not encrypted")
	}
}

func TestGuestTransferAndBatchScope(t *testing.T) {
	storage := openTestStore(t)
	ctx := context.Background()
	guest := model.ImportedAccount{Email: "guest@example.invalid", Password: "pass", ClientID: "client", RefreshToken: "refresh-token-long"}
	if err := storage.CreateGuestSession(ctx, "guest-id", nowTime().Add(60_000_000_000)); err != nil {
		t.Fatal(err)
	}
	_, _ = storage.ImportAccounts(ctx, "guest:guest-id", []model.ImportedAccount{guest}, "skip")
	transferred, err := storage.TransferGuestAccounts(ctx, "guest-id", 9)
	if err != nil || transferred != 1 {
		t.Fatalf("transfer failed: %d %v", transferred, err)
	}
	accounts, _ := storage.ListAccounts(ctx, "user:9")
	if len(accounts) != 1 || accounts[0].Email != guest.Email {
		t.Fatal("transferred account missing")
	}
}

func TestMigratesLegacyPlainEmailAccounts(t *testing.T) {
	dataDir := t.TempDir()
	box, err := secure.New(dataDir, strings.Repeat("05", 32), false)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := box.Encrypt("legacy-password")
	clientID, _ := box.Encrypt("legacy-client-id")
	refreshToken, _ := box.Encrypt("legacy-refresh-token")
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dataDir, "mail.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`CREATE TABLE accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		email TEXT NOT NULL UNIQUE COLLATE NOCASE,
		password_encrypted TEXT NOT NULL,
		client_id_encrypted TEXT NOT NULL,
		refresh_token_encrypted TEXT NOT NULL,
		remark TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_sync_at TEXT
	)`)
	if err == nil {
		_, err = database.Exec(`INSERT INTO accounts(email,password_encrypted,client_id_encrypted,refresh_token_encrypted,remark)
			VALUES(?,?,?,?,?)`, "legacy@example.com", password, clientID, refreshToken, "legacy")
	}
	if closeErr := database.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	storage, err := Open(dataDir, box)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	account, err := storage.GetAccountCredentials(context.Background(), "user:1", 1)
	if err != nil || account == nil || account.Email != "legacy@example.com" || account.Password != "legacy-password" || account.ClientID != "legacy-client-id" || account.RefreshToken != "legacy-refresh-token" {
		t.Fatalf("legacy migration mismatch: %#v %v", account, err)
	}
}

func nowTime() time.Time { return time.Now().UTC() }
