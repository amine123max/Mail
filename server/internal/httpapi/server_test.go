package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/amine123max/Mail/server/internal/auth"
	"github.com/amine123max/Mail/server/internal/config"
	"github.com/amine123max/Mail/server/internal/mailservice"
	"github.com/amine123max/Mail/server/internal/secure"
	"github.com/amine123max/Mail/server/internal/store"
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
	for _, path := range []string{"/mail/", "/mail/oauth", "/mail/accounts", "/mail/microsoft-oauth"} {
		response, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		page, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("Mail test")) {
			t.Fatalf("base-path SPA failed for %s: %d %s", path, response.StatusCode, page)
		}
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

func TestCompletedMailOperationReplaysWithoutCallingUpstream(t *testing.T) {
	server, storage := newAPITestServer(t)
	user := newCookieClient(t)
	status, _ := apiJSON(t, user, http.MethodPost, server.URL+"/api/auth/login", map[string]any{
		"email": "admin@example.com", "password": "AdminPassword!123",
	})
	if status != http.StatusOK {
		t.Fatalf("login status = %d", status)
	}
	status, imported := apiJSON(t, user, http.MethodPost, server.URL+"/api/accounts/import", map[string]any{
		"raw": "operation@example.invalid----password----client-id-operation----refresh-token-operation-long", "mode": "skip",
	})
	if status != http.StatusCreated {
		t.Fatalf("import status = %d %#v", status, imported)
	}
	accountID := int64(imported["accounts"].([]any)[0].(map[string]any)["id"].(float64))
	operationID := "operation-replay-12345678"
	uid, folder := "graph:message-id", "graph:inbox"
	digest := sha256.Sum256([]byte(strings.Join([]string{"flag", strconv.FormatInt(accountID, 10), uid, folder, "true"}, "\x00")))
	claim, err := storage.ClaimMailOperation(context.Background(), "user:1", operationID, "flag", hex.EncodeToString(digest[:]))
	if err != nil || claim != store.MailOperationClaimed {
		t.Fatalf("operation seed failed: %q %v", claim, err)
	}
	if err := storage.CompleteMailOperation(context.Background(), "user:1", operationID); err != nil {
		t.Fatal(err)
	}
	status, replay := apiJSON(t, user, http.MethodPatch, server.URL+"/api/accounts/"+strconv.FormatInt(accountID, 10)+"/messages/"+uid+"/flag", map[string]any{
		"folder": folder, "flagged": true, "operationId": operationID,
	})
	if status != http.StatusOK || replay["flagged"] != true {
		t.Fatalf("completed operation did not replay: %d %#v", status, replay)
	}
	status, conflict := apiJSON(t, user, http.MethodPatch, server.URL+"/api/accounts/"+strconv.FormatInt(accountID, 10)+"/messages/"+uid+"/flag", map[string]any{
		"folder": folder, "flagged": false, "operationId": operationID,
	})
	if status != http.StatusConflict || conflict["code"] != "OPERATION_ID_REUSED" {
		t.Fatalf("operation id reuse was not rejected: %d %#v", status, conflict)
	}
}

func TestCompletedSendOperationReplaysStoredResult(t *testing.T) {
	server, storage := newAPITestServer(t)
	user := newCookieClient(t)
	status, _ := apiJSON(t, user, http.MethodPost, server.URL+"/api/auth/login", map[string]any{
		"email": "admin@example.com", "password": "AdminPassword!123",
	})
	if status != http.StatusOK {
		t.Fatalf("login status = %d", status)
	}
	status, imported := apiJSON(t, user, http.MethodPost, server.URL+"/api/accounts/import", map[string]any{
		"raw": "send-replay@example.invalid----password----client-id-send-replay----refresh-token-send-replay-long", "mode": "skip",
	})
	if status != http.StatusCreated {
		t.Fatalf("import status = %d %#v", status, imported)
	}
	accountID := int64(imported["accounts"].([]any)[0].(map[string]any)["id"].(float64))
	operationID := "send-replay-operation-12345678"
	body := mailservice.SendRequest{
		To: "receiver@example.com", Subject: "idempotent send", Text: "hello",
		Attachments: []mailservice.AttachmentInput{{
			Filename: "report.pdf", ContentType: "application/pdf", UploadID: "995955c8-b4c0-4fde-bfc8-81f37cf976bb", Size: 1024,
		}},
		OperationID: operationID,
	}
	fingerprint, err := sendOperationFingerprint(body)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{"send", strconv.FormatInt(accountID, 10), fingerprint}, "\x00")))
	claim, err := storage.ClaimMailOperation(context.Background(), "user:1", operationID, "send", hex.EncodeToString(digest[:]))
	if err != nil || claim != store.MailOperationClaimed {
		t.Fatalf("send operation seed failed: %q %v", claim, err)
	}
	stored := sendMessageResponse{Status: "sent", MessageID: "stored-message-id", Accepted: []string{"receiver@example.com"}, Transport: "graph"}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.CompleteMailOperationWithResult(context.Background(), "user:1", operationID, string(encoded)); err != nil {
		t.Fatal(err)
	}
	status, replay := apiJSON(t, user, http.MethodPost, server.URL+"/api/accounts/"+strconv.FormatInt(accountID, 10)+"/send", body)
	if status != http.StatusCreated || replay["messageId"] != "stored-message-id" || replay["transport"] != "graph" {
		t.Fatalf("stored send result was not replayed: %d %#v", status, replay)
	}
	body.Subject = "changed subject"
	status, conflict := apiJSON(t, user, http.MethodPost, server.URL+"/api/accounts/"+strconv.FormatInt(accountID, 10)+"/send", body)
	if status != http.StatusConflict || conflict["code"] != "OPERATION_ID_REUSED" {
		t.Fatalf("send operation id reuse was not rejected: %d %#v", status, conflict)
	}
	body.OperationID = "send-validation-operation-12345678"
	body.To = "not-an-address"
	status, invalid := apiJSON(t, user, http.MethodPost, server.URL+"/api/accounts/"+strconv.FormatInt(accountID, 10)+"/send", body)
	details, _ := invalid["details"].([]any)
	if status != http.StatusBadRequest || len(details) == 0 {
		t.Fatalf("send validation did not identify the field: %d %#v", status, invalid)
	}
	first, _ := details[0].(map[string]any)
	if first["field"] != "to" {
		t.Fatalf("send validation field mismatch: %#v", invalid)
	}
}

func TestAuthenticatedAccountImportsAndBatchActionsHaveNoFixedLimit(t *testing.T) {
	server, _ := newAPITestServer(t)
	user := newCookieClient(t)
	status, _ := apiJSON(t, user, http.MethodPost, server.URL+"/api/auth/login", map[string]any{
		"email": "admin@example.com", "password": "AdminPassword!123",
	})
	if status != http.StatusOK {
		t.Fatalf("login status = %d", status)
	}

	lines := make([]string, 101)
	for index := range lines {
		lines[index] = fmt.Sprintf("bulk-%03d@example.invalid----password----client-id-%03d----refresh-token-%03d-long", index, index, index)
	}
	status, imported := apiJSON(t, user, http.MethodPost, server.URL+"/api/accounts/import", map[string]any{
		"raw": strings.Join(lines, "\n"), "mode": "skip",
	})
	if status != http.StatusCreated || imported["inserted"] != float64(len(lines)) {
		t.Fatalf("unlimited user import failed: %d %#v", status, imported)
	}

	status, listed := apiJSON(t, user, http.MethodGet, server.URL+"/api/accounts", nil)
	if status != http.StatusOK {
		t.Fatalf("account list status = %d: %#v", status, listed)
	}
	accounts, ok := listed["accounts"].([]any)
	if !ok || len(accounts) != len(lines) {
		t.Fatalf("account count mismatch: %#v", listed)
	}
	ids := make([]any, len(accounts))
	for index, value := range accounts {
		ids[index] = value.(map[string]any)["id"]
	}

	if status, _ := apiJSON(t, user, http.MethodPut, server.URL+"/api/accounts/order", map[string]any{"ids": ids}); status != http.StatusOK {
		t.Fatalf("unlimited reorder status = %d", status)
	}
	if status, _ := apiJSON(t, user, http.MethodPost, server.URL+"/api/accounts/export", map[string]any{"ids": ids}); status != http.StatusOK {
		t.Fatalf("unlimited export status = %d", status)
	}
	if status, _ := apiJSON(t, user, http.MethodPatch, server.URL+"/api/accounts/batch/group", map[string]any{"ids": ids, "group": "bulk"}); status != http.StatusOK {
		t.Fatalf("unlimited group status = %d", status)
	}
	if status, result := apiJSON(t, user, http.MethodPost, server.URL+"/api/accounts/batch/delete", map[string]any{"ids": ids}); status != http.StatusOK || result["deleted"] != float64(len(ids)) {
		t.Fatalf("unlimited batch delete failed: %d %#v", status, result)
	}

	guest := newCookieClient(t)
	if status, _ := apiJSON(t, guest, http.MethodPost, server.URL+"/api/auth/guest", map[string]any{}); status != http.StatusCreated {
		t.Fatalf("guest creation status = %d", status)
	}
	status, limited := apiJSON(t, guest, http.MethodPost, server.URL+"/api/accounts/import", map[string]any{
		"raw": strings.Join(lines[:4], "\n"), "mode": "skip",
	})
	if status != http.StatusForbidden || limited["code"] != "ACCOUNT_LIMIT_REACHED" {
		t.Fatalf("guest limit was not enforced: %d %#v", status, limited)
	}
}

func jsonNumber(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
