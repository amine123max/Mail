package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/amine123max/Mail/server/internal/desktopcontract"
	"github.com/amine123max/Mail/server/internal/model"
)

func desktopSyncGET(t *testing.T, client *http.Client, endpoint, bearer string, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Aillive-Client-Version", "1.0.0")
	request.Header.Set("X-Aillive-Client-Platform", "windows")
	request.Header.Set("X-Aillive-Installation-Id", "7f09f81d-442c-4be7-8b25-3d597d7ae9af")
	request.Header.Set("X-Request-Id", "desktop-sync-test")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header.Clone(), body
}

func decodeDesktopSyncBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	result := make(map[string]any)
	if len(bytes.TrimSpace(body)) == 0 {
		return result
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid desktop sync JSON: %s", body)
	}
	return result
}

func TestDesktopSyncRoutesAuthenticateIsolateAndValidate(t *testing.T) {
	server, storage := newAPITestServer(t)
	client := newCookieClient(t)
	status, _, unauthorizedBody := desktopSyncGET(t, client, server.URL+"/api/v1/desktop/unread-summary", "", nil)
	unauthorized := decodeDesktopSyncBody(t, unauthorizedBody)
	if status != http.StatusUnauthorized || unauthorized["code"] != "DESKTOP_ACCESS_REQUIRED" {
		t.Fatalf("desktop sync route allowed anonymous access: %d %#v", status, unauthorized)
	}

	status, _, created := desktopSessionJSON(t, client, http.MethodPost, server.URL+"/api/v1/desktop/sessions", "", map[string]any{
		"email": "admin@example.com", "password": "AdminPassword!123", "deviceId": "desktop-sync-device", "deviceName": "Sync test device",
	})
	if status != http.StatusCreated {
		t.Fatalf("desktop sync login failed: %d %#v", status, created)
	}
	accessToken := created["accessToken"].(string)
	userID := int64(created["user"].(map[string]any)["id"].(float64))
	ownerKey := "user:" + strconv.FormatInt(userID, 10)

	status, headers, unreadBody := desktopSyncGET(t, client, server.URL+"/api/v1/desktop/unread-summary", accessToken, nil)
	unread := decodeDesktopSyncBody(t, unreadBody)
	accounts, _ := unread["accounts"].([]any)
	if status != http.StatusOK || len(accounts) != 0 || headers.Get("ETag") == "" || headers.Get("Cache-Control") != "private, no-cache, max-age=0" {
		t.Fatalf("empty unread summary contract mismatch: %d %#v headers=%v", status, unread, headers)
	}
	status, _, notModifiedBody := desktopSyncGET(t, client, server.URL+"/api/v1/desktop/unread-summary", accessToken, map[string]string{"If-None-Match": headers.Get("ETag")})
	if status != http.StatusNotModified || len(notModifiedBody) != 0 {
		t.Fatalf("unread summary conditional request mismatch: %d %q", status, notModifiedBody)
	}

	ctx := context.Background()
	account := model.ImportedAccount{Email: "owned@example.invalid", Password: "password", ClientID: "client", RefreshToken: "refresh-token-long"}
	if _, err := storage.ImportAccounts(ctx, ownerKey, []model.ImportedAccount{account}, "skip"); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.ImportAccounts(ctx, "user:999999", []model.ImportedAccount{{Email: "foreign@example.invalid", Password: "password", ClientID: "client", RefreshToken: "refresh-token-long"}}, "skip"); err != nil {
		t.Fatal(err)
	}
	owned, _ := storage.ListAccounts(ctx, ownerKey)
	foreign, _ := storage.ListAccounts(ctx, "user:999999")

	status, _, foreignBody := desktopSyncGET(t, client, server.URL+"/api/v1/desktop/accounts/"+strconv.FormatInt(foreign[0].ID, 10)+"/folders/INBOX/changes", accessToken, nil)
	foreignResult := decodeDesktopSyncBody(t, foreignBody)
	if status != http.StatusNotFound || foreignResult["code"] != "ACCOUNT_NOT_FOUND" {
		t.Fatalf("desktop sync account ownership failed: %d %#v", status, foreignResult)
	}

	status, _, limitBody := desktopSyncGET(t, client, server.URL+"/api/v1/desktop/accounts/"+strconv.FormatInt(owned[0].ID, 10)+"/folders/INBOX/changes?limit=201", accessToken, nil)
	limitResult := decodeDesktopSyncBody(t, limitBody)
	if status != http.StatusBadRequest || limitResult["code"] != "VALIDATION_ERROR" {
		t.Fatalf("desktop sync limit validation failed: %d %#v", status, limitResult)
	}

	status, _, cursorBody := desktopSyncGET(t, client, server.URL+"/api/v1/desktop/accounts/"+strconv.FormatInt(owned[0].ID, 10)+"/folders/INBOX/changes?cursor="+strings.Repeat("x", 129), accessToken, nil)
	cursorResult := decodeDesktopSyncBody(t, cursorBody)
	if status != http.StatusBadRequest || cursorResult["code"] != "VALIDATION_ERROR" {
		t.Fatalf("desktop sync cursor validation failed: %d %#v", status, cursorResult)
	}
}

func TestDesktopConditionalJSONGzipETagAndCursorHeaders(t *testing.T) {
	accounts := make([]desktopcontract.DesktopUnreadAccountSummary, 0, 40)
	for index := 0; index < 40; index++ {
		accounts = append(accounts, desktopcontract.DesktopUnreadAccountSummary{
			AccountId: int64(index + 1), Provider: "imap", UnreadCount: index,
			Folders: []desktopcontract.DesktopUnreadFolderSummary{{Folder: "INBOX", UnreadCount: index, TotalCount: index + 10}},
		})
	}
	payload := desktopcontract.DesktopUnreadSummaryResponse{Accounts: accounts, ServerTime: "2026-07-18T00:00:00Z"}
	etag := unreadResponseETag(payload)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/desktop/unread-summary", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	if err := writeDesktopConditionalJSON(response, request, payload, etag, true); err != nil {
		t.Fatal(err)
	}
	result := response.Result()
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK || result.Header.Get("Content-Encoding") != "gzip" || result.Header.Get("ETag") != etag || result.Header.Get("Vary") != "Accept-Encoding" {
		t.Fatalf("conditional gzip headers mismatch: %d %v", result.StatusCode, result.Header)
	}
	reader, err := gzip.NewReader(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	uncompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	var decoded desktopcontract.DesktopUnreadSummaryResponse
	if err := json.Unmarshal(uncompressed, &decoded); err != nil || len(decoded.Accounts) != len(accounts) {
		t.Fatalf("gzip response body mismatch: accounts=%d err=%v", len(decoded.Accounts), err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/desktop/unread-summary", nil)
	request.Header.Set("If-None-Match", etag)
	response = httptest.NewRecorder()
	if err := writeDesktopConditionalJSON(response, request, payload, etag, true); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusNotModified || response.Body.Len() != 0 {
		t.Fatalf("conditional 304 mismatch: %d %q", response.Code, response.Body.String())
	}

	lastSyncAt := "2026-07-18T00:01:00Z"
	syncResult := desktopcontract.DesktopSyncChangesResponse{NextCursor: "opaque-next", LastSyncAt: &lastSyncAt}
	response = httptest.NewRecorder()
	setDesktopSyncHeaders(response, syncResult)
	if response.Header().Get("X-Aillive-Next-Cursor") != "opaque-next" || response.Header().Get("X-Aillive-Last-Sync-At") != lastSyncAt {
		t.Fatalf("sync cursor headers mismatch: %v", response.Header())
	}

	first := desktopcontract.DesktopSyncChangesResponse{Upserts: []desktopcontract.DesktopSyncChange{}, DeletedIds: []string{}, NextCursor: "cursor-one", Provider: "graph", ServerTime: "one", LastSyncAt: &lastSyncAt}
	second := first
	second.NextCursor = "cursor-two"
	second.ServerTime = "two"
	if syncResponseETag(first) != syncResponseETag(second) {
		t.Fatal("sync ETag changed for cursor-only metadata")
	}
	second.UnreadCount = 1
	if syncResponseETag(first) == syncResponseETag(second) {
		t.Fatal("sync ETag ignored unread state change")
	}
}
