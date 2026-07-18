package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func desktopSessionJSON(t *testing.T, client *http.Client, method, endpoint, bearer string, payload any) (int, http.Header, map[string]any) {
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
	request.Header.Set("X-Aillive-Client-Version", "1.0.0")
	request.Header.Set("X-Aillive-Client-Platform", "windows")
	request.Header.Set("X-Aillive-Installation-Id", "7f09f81d-442c-4be7-8b25-3d597d7ae9af")
	request.Header.Set("X-Request-Id", "desktop-session-test")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
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
	return response.StatusCode, response.Header.Clone(), result
}

func TestDesktopSessionRotationReplayAndRevocation(t *testing.T) {
	server, _ := newAPITestServer(t)
	client := newCookieClient(t)
	status, headers, created := desktopSessionJSON(t, client, http.MethodPost, server.URL+"/api/v1/desktop/sessions", "", map[string]any{
		"email":      "admin@example.com",
		"password":   "AdminPassword!123",
		"deviceId":   "7f09f81d-442c-4be7-8b25-3d597d7ae9af",
		"deviceName": "Windows test device",
	})
	if status != http.StatusCreated || headers.Get("X-Request-Id") != "desktop-session-test" {
		t.Fatalf("desktop login failed: %d %#v", status, created)
	}
	accessToken, _ := created["accessToken"].(string)
	refreshToken, _ := created["refreshToken"].(string)
	if accessToken == "" || refreshToken == "" || created["expiresIn"] != float64(900) {
		t.Fatalf("desktop token response invalid: %#v", created)
	}
	status, _, accounts := desktopSessionJSON(t, client, http.MethodGet, server.URL+"/api/accounts", accessToken, nil)
	if status != http.StatusOK {
		t.Fatalf("desktop bearer could not access migration API: %d %#v", status, accounts)
	}

	status, _, listed := desktopSessionJSON(t, client, http.MethodGet, server.URL+"/api/v1/desktop/devices", accessToken, nil)
	devices, _ := listed["devices"].([]any)
	if status != http.StatusOK || len(devices) != 1 || devices[0].(map[string]any)["current"] != true {
		t.Fatalf("desktop device list invalid: %d %#v", status, listed)
	}

	status, _, refreshed := desktopSessionJSON(t, client, http.MethodPost, server.URL+"/api/v1/desktop/sessions/refresh", "", map[string]any{"refreshToken": refreshToken})
	if status != http.StatusOK || refreshed["refreshToken"] == refreshToken || refreshed["accessToken"] == "" {
		t.Fatalf("desktop refresh failed: %d %#v", status, refreshed)
	}
	refreshedAccess := refreshed["accessToken"].(string)

	status, _, replayed := desktopSessionJSON(t, client, http.MethodPost, server.URL+"/api/v1/desktop/sessions/refresh", "", map[string]any{"refreshToken": refreshToken})
	if status != http.StatusUnauthorized || replayed["code"] != "DESKTOP_REFRESH_REPLAYED" {
		t.Fatalf("old refresh token replay was not rejected: %d %#v", status, replayed)
	}
	status, _, revoked := desktopSessionJSON(t, client, http.MethodGet, server.URL+"/api/v1/desktop/devices", refreshedAccess, nil)
	if status != http.StatusUnauthorized || revoked["code"] != "DESKTOP_SESSION_REVOKED" {
		t.Fatalf("replayed family remained active: %d %#v", status, revoked)
	}
}

func TestDesktopSessionMigratesAuthenticatedCookieWithoutPassword(t *testing.T) {
	server, _ := newAPITestServer(t)
	client := newCookieClient(t)
	status, login := apiJSON(t, client, http.MethodPost, server.URL+"/api/auth/login", map[string]any{
		"email": "admin@example.com", "password": "AdminPassword!123",
	})
	if status != http.StatusOK || login["authenticated"] != true {
		t.Fatalf("web login failed before migration: %d %#v", status, login)
	}
	status, _, migrated := desktopSessionJSON(t, client, http.MethodPost, server.URL+"/api/v1/desktop/sessions/migrate", "", map[string]any{
		"deviceId": "desktop-cookie-migration", "deviceName": "Migrated Windows device",
	})
	if status != http.StatusCreated || migrated["accessToken"] == "" || migrated["refreshToken"] == "" {
		t.Fatalf("desktop cookie migration failed: %d %#v", status, migrated)
	}
	accessToken := migrated["accessToken"].(string)
	status, _, devices := desktopSessionJSON(t, client, http.MethodGet, server.URL+"/api/v1/desktop/devices", accessToken, nil)
	if status != http.StatusOK || len(devices["devices"].([]any)) != 1 {
		t.Fatalf("migrated session could not access device API: %d %#v", status, devices)
	}
}

func TestDesktopCurrentAndAllDeviceRevocation(t *testing.T) {
	server, _ := newAPITestServer(t)
	client := newCookieClient(t)
	_, _, created := desktopSessionJSON(t, client, http.MethodPost, server.URL+"/api/v1/desktop/sessions", "", map[string]any{
		"email": "admin@example.com", "password": "AdminPassword!123", "deviceId": "desktop-device-current", "deviceName": "Current device",
	})
	accessToken := created["accessToken"].(string)
	status, _, _ := desktopSessionJSON(t, client, http.MethodDelete, server.URL+"/api/v1/desktop/sessions/current", accessToken, nil)
	if status != http.StatusNoContent {
		t.Fatalf("current desktop logout returned %d", status)
	}
	status, _, body := desktopSessionJSON(t, client, http.MethodGet, server.URL+"/api/v1/desktop/devices", accessToken, nil)
	if status != http.StatusUnauthorized || body["code"] != "DESKTOP_SESSION_REVOKED" {
		t.Fatalf("current desktop session remained active: %d %#v", status, body)
	}

	_, _, replacement := desktopSessionJSON(t, client, http.MethodPost, server.URL+"/api/v1/desktop/sessions", "", map[string]any{
		"email": "admin@example.com", "password": "AdminPassword!123", "deviceId": "desktop-device-replacement", "deviceName": "Replacement device",
	})
	replacementAccess := replacement["accessToken"].(string)
	status, _, _ = desktopSessionJSON(t, client, http.MethodDelete, server.URL+"/api/v1/desktop/devices", replacementAccess, nil)
	if status != http.StatusNoContent {
		t.Fatalf("all-device revocation returned %d", status)
	}
	status, _, body = desktopSessionJSON(t, client, http.MethodGet, server.URL+"/api/v1/desktop/devices", replacementAccess, nil)
	if status != http.StatusUnauthorized || body["code"] != "DESKTOP_SESSION_REVOKED" {
		t.Fatalf("all-device revocation left access active: %d %#v", status, body)
	}
}

func TestDesktopDeviceRevocationIsRateLimited(t *testing.T) {
	server, _ := newAPITestServer(t)
	client := newCookieClient(t)
	_, _, created := desktopSessionJSON(t, client, http.MethodPost, server.URL+"/api/v1/desktop/sessions", "", map[string]any{
		"email": "admin@example.com", "password": "AdminPassword!123", "deviceId": "desktop-rate-limit", "deviceName": "Rate limit device",
	})
	accessToken := created["accessToken"].(string)
	for attempt := 1; attempt <= 21; attempt++ {
		status, headers, body := desktopSessionJSON(t, client, http.MethodDelete, server.URL+"/api/v1/desktop/devices/unused-device", accessToken, nil)
		if attempt <= 20 && status != http.StatusNoContent {
			t.Fatalf("device revoke attempt %d unexpectedly failed: %d %#v", attempt, status, body)
		}
		if attempt == 21 {
			if status != http.StatusTooManyRequests || body["code"] != "AUTH_RATE_LIMIT" || headers.Get("Retry-After") == "" || body["requestId"] == "" {
				t.Fatalf("device revoke rate limit contract invalid: %d %#v headers=%v", status, body, headers)
			}
		}
	}
}
