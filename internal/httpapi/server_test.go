package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amine123max/Mail/internal/auth"
	"github.com/amine123max/Mail/internal/config"
	"github.com/amine123max/Mail/internal/mailservice"
	"github.com/amine123max/Mail/internal/secure"
	"github.com/amine123max/Mail/internal/store"
)

func newAPITestServer(t *testing.T) (*httptest.Server, *store.Store) {
	return newAPITestServerWithCookiePath(t, "/")
}

func newAPITestServerWithCookiePath(t *testing.T, cookiePath string) (*httptest.Server, *store.Store) {
	t.Helper()
	dataDir := t.TempDir()
	webRoot := filepath.Join(dataDir, "dist")
	if err := os.MkdirAll(webRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("<!doctype html><title>Mail test</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		DataDir:       dataDir,
		WebRoot:       webRoot,
		SessionSecret: strings.Repeat("api-session-secret-", 3),
		CookiePath:    cookiePath,
		IMAPHosts:     []string{"127.0.0.1"},
		SMTPHosts:     []string{"127.0.0.1"},
	}
	box, err := secure.New(dataDir, strings.Repeat("04", 32), false)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := store.Open(dataDir, box)
	if err != nil {
		t.Fatal(err)
	}
	authentication := auth.New(cfg, storage)
	if _, err := authentication.BootstrapAdministrator(context.Background(), "mailadmin", "admin@example.com", "AdminPassword!123"); err != nil {
		_ = storage.Close()
		t.Fatal(err)
	}
	server := httptest.NewServer(New(cfg, storage, authentication, mailservice.New(cfg, storage)).Handler())
	t.Cleanup(func() {
		server.Close()
		_ = storage.Close()
	})
	return server, storage
}

func TestConfiguredBasePathServesAPIAndSPA(t *testing.T) {
	server, _ := newAPITestServerWithCookiePath(t, "/mail")
	client := newCookieClient(t)
	status, health := apiJSON(t, client, http.MethodGet, server.URL+"/mail/api/health", nil)
	if status != http.StatusOK || health["runtime"] != "go" {
		t.Fatalf("base-path health failed: %d %#v", status, health)
	}
	response, err := client.Get(server.URL + "/mail/accounts")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	page, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("Mail test")) {
		t.Fatalf("base-path SPA failed: %d %s", response.StatusCode, page)
	}
}

func newCookieClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func apiJSON(t *testing.T, client *http.Client, method, endpoint string, payload any) (int, map[string]any) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	result := make(map[string]any)
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("invalid JSON response (%d): %s", response.StatusCode, data)
		}
	}
	return response.StatusCode, result
}

func TestAuthenticationIsolationImportExportAndGuestTransfer(t *testing.T) {
	server, _ := newAPITestServer(t)
	anonymous := newCookieClient(t)
	if status, _ := apiJSON(t, anonymous, http.MethodGet, server.URL+"/api/accounts", nil); status != http.StatusUnauthorized {
		t.Fatalf("anonymous account status = %d", status)
	}

	user := newCookieClient(t)
	status, login := apiJSON(t, user, http.MethodPost, server.URL+"/api/auth/login", map[string]any{
		"email": "admin@example.com", "password": "AdminPassword!123",
	})
	if status != http.StatusOK || login["authenticated"] != true || login["administrator"] != true {
		t.Fatalf("login failed: %d %#v", status, login)
	}
	adminRaw := "alpha@example.invalid----password----client-id-alpha----refresh-token-alpha-long"
	status, imported := apiJSON(t, user, http.MethodPost, server.URL+"/api/accounts/import", map[string]any{"raw": adminRaw, "mode": "skip"})
	if status != http.StatusCreated || imported["inserted"] != float64(1) {
		t.Fatalf("admin import failed: %d %#v", status, imported)
	}
	status, listed := apiJSON(t, user, http.MethodGet, server.URL+"/api/accounts", nil)
	accounts, _ := listed["accounts"].([]any)
	if status != http.StatusOK || len(accounts) != 1 {
		t.Fatalf("admin account list failed: %d %#v", status, listed)
	}
	account := accounts[0].(map[string]any)
	for _, secret := range []string{"password", "clientId", "refreshToken", "ownerKey"} {
		if _, leaked := account[secret]; leaked {
			t.Fatalf("account response leaked %s", secret)
		}
	}
	status, exported := apiJSON(t, user, http.MethodPost, server.URL+"/api/accounts/export", map[string]any{"ids": []any{account["id"]}})
	if status != http.StatusOK || exported["filename"] != "mail.txt" || exported["content"] != adminRaw {
		t.Fatalf("account export mismatch: %d %#v", status, exported)
	}

	guest := newCookieClient(t)
	if status, _ := apiJSON(t, guest, http.MethodPost, server.URL+"/api/auth/guest", map[string]any{}); status != http.StatusCreated {
		t.Fatalf("guest creation status = %d", status)
	}
	guestRaw := "guest@example.invalid----password----client-id-guest----refresh-token-guest-long"
	if status, _ := apiJSON(t, guest, http.MethodPost, server.URL+"/api/accounts/import", map[string]any{"raw": guestRaw, "mode": "skip"}); status != http.StatusCreated {
		t.Fatalf("guest import status = %d", status)
	}
	status, guestList := apiJSON(t, guest, http.MethodGet, server.URL+"/api/accounts", nil)
	guestAccounts := guestList["accounts"].([]any)
	if status != http.StatusOK || len(guestAccounts) != 1 {
		t.Fatalf("guest account list mismatch: %d %#v", status, guestList)
	}
	guestID := guestAccounts[0].(map[string]any)["id"]
	status, sendFailure := apiJSON(t, guest, http.MethodPost, server.URL+"/api/accounts/"+jsonNumber(guestID)+"/send", map[string]any{
		"to": "receiver@example.com", "subject": "test", "text": "hello", "html": "", "attachments": []any{},
	})
	if status != http.StatusForbidden || sendFailure["code"] != "GUEST_SEND_DISABLED" {
		t.Fatalf("guest send was not blocked: %d %#v", status, sendFailure)
	}
	_, adminBeforeTransfer := apiJSON(t, user, http.MethodGet, server.URL+"/api/accounts", nil)
	if len(adminBeforeTransfer["accounts"].([]any)) != 1 {
		t.Fatal("guest account leaked into authenticated owner")
	}
	status, transferred := apiJSON(t, guest, http.MethodPost, server.URL+"/api/auth/login", map[string]any{
		"email": "admin@example.com", "password": "AdminPassword!123",
	})
	if status != http.StatusOK || transferred["transferred"] != float64(1) {
		t.Fatalf("guest transfer failed: %d %#v", status, transferred)
	}
	_, afterTransfer := apiJSON(t, guest, http.MethodGet, server.URL+"/api/accounts", nil)
	if len(afterTransfer["accounts"].([]any)) != 2 {
		t.Fatalf("transferred account count mismatch: %#v", afterTransfer)
	}

	response, err := user.Get(server.URL + "/accounts")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	spa, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !bytes.Contains(spa, []byte("Mail test")) {
		t.Fatalf("SPA route failed: %d %s", response.StatusCode, spa)
	}
}

func jsonNumber(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
