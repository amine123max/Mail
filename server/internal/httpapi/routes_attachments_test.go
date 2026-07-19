package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachmentResponseMetadataIsSafe(t *testing.T) {
	values := map[string]string{
		`../../CON.txt`:      "_CON.txt",
		`..\\..\\report.pdf`: "report.pdf",
		"bad\r\nname.exe":    "badname.exe",
		"":                   "attachment.bin",
	}
	for input, expected := range values {
		if actual := safeDownloadFilename(input); actual != expected {
			t.Fatalf("safe filename %q = %q, want %q", input, actual, expected)
		}
	}
	if actual := safeAttachmentContentType("text/plain; charset=utf-8"); actual != "text/plain" {
		t.Fatalf("content type = %q", actual)
	}
	if actual := safeAttachmentContentType("text/plain\r\nX-Test: unsafe"); actual != "application/octet-stream" {
		t.Fatalf("unsafe content type = %q", actual)
	}
}

func TestAttachmentRouteRejectsAnonymousAndCrossOwnerAccess(t *testing.T) {
	server, _ := newAPITestServer(t)
	attachmentID := "mime:" + strings.Repeat("a", 64)
	endpoint := func(accountID string) string {
		return server.URL + "/api/accounts/" + accountID + "/messages/1/attachments/" + attachmentID + "?folder=INBOX"
	}
	anonymous := newCookieClient(t)
	if status, _ := apiJSON(t, anonymous, http.MethodGet, endpoint("1"), nil); status != http.StatusUnauthorized {
		t.Fatalf("anonymous attachment status = %d", status)
	}
	owner := newCookieClient(t)
	if status, _ := apiJSON(t, owner, http.MethodPost, server.URL+"/api/auth/login", map[string]any{"email": "admin@example.com", "password": "AdminPassword!123"}); status != http.StatusOK {
		t.Fatalf("owner login status = %d", status)
	}
	guest := newCookieClient(t)
	if status, _ := apiJSON(t, guest, http.MethodPost, server.URL+"/api/auth/guest", map[string]any{}); status != http.StatusCreated {
		t.Fatalf("guest creation status = %d", status)
	}
	status, imported := apiJSON(t, guest, http.MethodPost, server.URL+"/api/accounts/import", map[string]any{
		"raw": "attachment-owner@example.invalid----password----client-id-attachment----refresh-token-attachment-long", "mode": "skip",
	})
	if status != http.StatusCreated {
		t.Fatalf("guest import status = %d %#v", status, imported)
	}
	accountID := jsonNumber(imported["accounts"].([]any)[0].(map[string]any)["id"])
	status, failure := apiJSON(t, owner, http.MethodGet, endpoint(accountID), nil)
	if status != http.StatusNotFound || failure["code"] != "ACCOUNT_NOT_FOUND" {
		t.Fatalf("cross-owner attachment access = %d %#v", status, failure)
	}
}

func TestDesktopAttachmentUploadStreamsAndIsOwnerScoped(t *testing.T) {
	server, storage := newAPITestServer(t)
	owner := newCookieClient(t)
	if status, _ := apiJSON(t, owner, http.MethodPost, server.URL+"/api/auth/login", map[string]any{"email": "admin@example.com", "password": "AdminPassword!123"}); status != http.StatusOK {
		t.Fatalf("owner login status = %d", status)
	}
	payload := []byte("streamed attachment payload")
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/desktop/attachments", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("X-Aillive-Attachment-Name", base64.RawURLEncoding.EncodeToString([]byte("report.txt")))
	response, err := owner.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d %s", response.StatusCode, data)
	}
	var upload desktopAttachmentUploadResponse
	if err := json.Unmarshal(data, &upload); err != nil {
		t.Fatal(err)
	}
	if upload.UploadID == "" || upload.Filename != "report.txt" || upload.ContentType != "text/plain" || upload.Size != int64(len(payload)) {
		t.Fatalf("upload response mismatch: %#v", upload)
	}
	filePath := filepath.Join(filepath.Dir(storage.Path), "desktop-attachments", upload.UploadID+".upload")
	stored, err := os.ReadFile(filePath)
	if err != nil || !bytes.Equal(stored, payload) {
		t.Fatalf("streamed file mismatch: %q %v", stored, err)
	}
	var encryptedFilename string
	if err := storage.DB().QueryRow("SELECT filename_encrypted FROM desktop_attachment_uploads WHERE id = ?", upload.UploadID).Scan(&encryptedFilename); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encryptedFilename, "v1:") || strings.Contains(encryptedFilename, "report.txt") {
		t.Fatalf("attachment filename was not encrypted: %q", encryptedFilename)
	}
	guest := newCookieClient(t)
	if status, _ := apiJSON(t, guest, http.MethodPost, server.URL+"/api/auth/guest", map[string]any{}); status != http.StatusCreated {
		t.Fatalf("guest creation status = %d", status)
	}
	if status, failure := apiJSON(t, guest, http.MethodDelete, server.URL+"/api/v1/desktop/attachments/"+upload.UploadID, nil); status != http.StatusNotFound || failure["code"] != "ATTACHMENT_UPLOAD_NOT_FOUND" {
		t.Fatalf("cross-owner upload delete = %d %#v", status, failure)
	}
	if status, _ := apiJSON(t, owner, http.MethodDelete, server.URL+"/api/v1/desktop/attachments/"+upload.UploadID, nil); status != http.StatusNoContent {
		t.Fatalf("owner upload delete status = %d", status)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("uploaded file still exists: %v", err)
	}
}
