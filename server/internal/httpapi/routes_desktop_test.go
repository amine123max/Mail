package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/amine123max/Mail/server/internal/desktopcontract"
)

func TestDesktopCapabilitiesLegacyAndVersionedRoutes(t *testing.T) {
	server, _ := newAPITestServer(t)
	client := newCookieClient(t)
	for _, path := range []string{"/api/desktop/capabilities", "/api/v1/desktop/capabilities"} {
		request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Request-Id", "desktop-capabilities-test")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var body desktopcontract.DesktopCapabilities
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s returned %d", path, response.StatusCode)
		}
		if response.Header.Get("X-Request-Id") != "desktop-capabilities-test" {
			t.Fatalf("%s did not echo request ID", path)
		}
		if body.ApiVersion != desktopAPIVersion || body.MinimumClientVersion == "" {
			t.Fatalf("%s returned invalid capability versions: %#v", path, body)
		}
		if body.Features == nil || body.Limits.MaxJsonRequestBytes == 0 || len(body.Sync.Providers) == 0 {
			t.Fatalf("%s omitted generated capability fields: %#v", path, body)
		}
	}
}

func TestDesktopErrorEnvelopeIncludesStableMetadata(t *testing.T) {
	server, _ := newAPITestServer(t)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/auth/login", bytes.NewBufferString(`{"email":"invalid","password":""}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-Id", "desktop-error-test")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body desktopcontract.DesktopApiError
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || body.Code != "VALIDATION_ERROR" {
		t.Fatalf("unexpected desktop error: %d %#v", response.StatusCode, body)
	}
	if body.Message == "" || body.RequestId != "desktop-error-test" || body.Retryable {
		t.Fatalf("desktop error metadata missing: %#v", body)
	}
	if body.Details == nil || body.RetryAfter != nil {
		t.Fatalf("desktop error nullable fields invalid: %#v", body)
	}
}
