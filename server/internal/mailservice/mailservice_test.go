package mailservice

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/amine123max/Mail/server/internal/model"
	"github.com/amine123max/Mail/server/internal/secure"
	"github.com/amine123max/Mail/server/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestRecipientParsingAndMIMEEnvelope(t *testing.T) {
	addresses, err := recipientAddresses(`Alice Example <alice@example.com>; bob@example.com, ALICE@example.com`)
	if err != nil || len(addresses) != 2 || addresses[0] != "alice@example.com" || addresses[1] != "bob@example.com" {
		t.Fatalf("recipient parsing mismatch: %#v %v", addresses, err)
	}
	if _, err := recipientAddresses("not-an-email"); err == nil {
		t.Fatal("invalid recipient was accepted")
	}
	raw, accepted, messageID, err := buildMIMEMessage("sender@example.com", SendRequest{
		To: "Alice <alice@example.com>", CC: "bob@example.com", BCC: "hidden@example.com",
		Subject: "测试主题", Text: "plain body", HTML: "<p>HTML body</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted) != 3 || messageID == "" || !bytes.Contains(raw, []byte("Message-Id: "+messageID)) {
		t.Fatalf("MIME envelope mismatch: %#v %q", accepted, messageID)
	}
	if bytes.Contains(bytes.ToLower(raw), []byte("bcc:")) || !bytes.Contains(raw, []byte("multipart/alternative")) {
		t.Fatal("MIME message exposed Bcc or omitted multipart body")
	}
}

func TestMIMEInlineImagesAndSanitization(t *testing.T) {
	raw := strings.Join([]string{
		"From: Sender <sender@example.com>",
		"To: receiver@example.com",
		"Subject: Inline image",
		"MIME-Version: 1.0",
		`Content-Type: multipart/related; boundary="mail-boundary"`,
		"",
		"--mail-boundary",
		"Content-Type: text/html; charset=UTF-8",
		"",
		`<div style="background:url(https://tracker.example/pixel)"><img src="cid:logo"><img src="http://127.0.0.1/private"></div>`,
		"--mail-boundary",
		"Content-Type: image/png",
		"Content-ID: <logo>",
		"Content-Disposition: inline",
		"Content-Transfer-Encoding: base64",
		"",
		"iVBORw0KGgo=",
		"--mail-boundary",
		"Content-Type: text/plain; name=note.txt",
		"Content-Disposition: attachment; filename=note.txt",
		"",
		"attachment",
		"--mail-boundary--",
		"",
	}, "\r\n")
	parsed, err := parseMIMEMessage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	detail := (&Service{}).parsedDetail(context.Background(), uint32(7), parsed)
	if !strings.Contains(detail.HTML, "data:image/png;base64,") || strings.Contains(detail.HTML, "127.0.0.1") || strings.Contains(detail.HTML, "tracker.example") {
		t.Fatalf("unsafe or missing inline HTML: %s", detail.HTML)
	}
	if len(detail.Attachments) != 1 || detail.Attachments[0].Filename != "note.txt" {
		t.Fatalf("attachment mapping mismatch: %#v", detail.Attachments)
	}
	unsafe := sanitizeMessageHTML(`<img src="https://tracker.example/a.png"><style>.x{background:url(https://tracker.example/x)}</style>`, nil)
	if strings.Contains(unsafe, "tracker.example") || strings.Contains(unsafe, "https://") {
		t.Fatalf("sanitizer preserved a remote resource: %s", unsafe)
	}
}

func TestRemoteImageURLRejectsPrivateNetworks(t *testing.T) {
	for _, source := range []string{
		"http://127.0.0.1/a.png",
		"http://10.0.0.1/a.png",
		"http://169.254.169.254/latest/meta-data",
		"http://192.168.1.1/a.png",
		"http://100.64.0.1/a.png",
		"http://[::1]/a.png",
		"http://localhost/a.png",
	} {
		if _, err := ValidateRemoteImageURL(context.Background(), source); err == nil {
			t.Fatalf("private remote image URL was accepted: %s", source)
		}
	}
	if parsed, err := ValidateRemoteImageURL(context.Background(), "https://93.184.216.34/image.png"); err != nil || parsed.Hostname() != "93.184.216.34" {
		t.Fatalf("public image URL rejected: %v", err)
	}
}

func TestGraphSendPayloadAndRecipientMapping(t *testing.T) {
	var captured map[string]any
	service := &Service{httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://graph.microsoft.com/v1.0/me/sendMail" || request.Header.Get("Authorization") != "Bearer graph-token" {
			t.Fatalf("unexpected Graph request: %s", request.URL)
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})}}
	result, err := service.graphSend(context.Background(), &model.AccountCredentials{Email: "sender@example.com"}, "graph-token", SendRequest{
		To: "Alice <alice@example.com>; bob@example.com", CC: "copy@example.com", Subject: "Graph subject", HTML: "<p>Body</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Transport != "graph" || len(result.Accepted) != 2 || result.Accepted[0] != "alice@example.com" {
		t.Fatalf("Graph result mismatch: %#v", result)
	}
	message := captured["message"].(map[string]any)
	to := message["toRecipients"].([]any)
	if len(to) != 2 || to[0].(map[string]any)["emailAddress"].(map[string]any)["address"] != "alice@example.com" {
		t.Fatalf("Graph recipient payload mismatch: %#v", message["toRecipients"])
	}
}

func TestGraphFolderAndMessageMapping(t *testing.T) {
	dataDir := t.TempDir()
	box, err := secure.New(dataDir, strings.Repeat("06", 32), false)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := store.Open(dataDir, box)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	service := &Service{store: storage, httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		switch {
		case strings.HasSuffix(request.URL.Path, "/mailFolders"):
			body = `{"value":[{"id":"folder-id"}]}`
		case strings.Contains(request.URL.Path, "/mailFolders/inbox/messages"):
			body = `{"@odata.count":1,"value":[{"id":"message-id","subject":"Hello","sender":{"emailAddress":{"name":"Sender","address":"sender@example.com"}},"toRecipients":[{"emailAddress":{"address":"receiver@example.com"}}],"receivedDateTime":"2026-07-18T01:02:03Z","isRead":false,"flag":{"flagStatus":"flagged"},"bodyPreview":"Preview text"}]}`
		default:
			t.Fatalf("unexpected Graph path: %s", request.URL.String())
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	account := &model.AccountCredentials{ID: 99, OwnerKey: "user:1"}
	folders, err := service.graphFolders(context.Background(), account, "token")
	if err != nil || len(folders) != 5 || folders[0].Path != "graph:inbox" {
		t.Fatalf("Graph folders mismatch: %#v %v", folders, err)
	}
	result, err := service.graphListMessages(context.Background(), account, "token", "graph:inbox", 1, 30, "")
	if err != nil {
		t.Fatal(err)
	}
	messages := result["messages"].([]MessageSummary)
	if len(messages) != 1 || messages[0].UID != "graph:message-id" || messages[0].FromEmail != "sender@example.com" || !messages[0].Unread || !messages[0].Flagged {
		t.Fatalf("Graph message mapping mismatch: %#v", messages)
	}
}

func TestGraphMessageReadStateIsExplicit(t *testing.T) {
	dataDir := t.TempDir()
	box, err := secure.New(dataDir, strings.Repeat("07", 32), false)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := store.Open(dataDir, box)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	requests := make([]string, 0, 2)
	var readValue any
	service := &Service{store: storage, httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if request.Method == http.MethodDelete {
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil))}, nil
		}
		if request.Method == http.MethodPatch {
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			readValue = payload["isRead"]
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		body := `{"id":"message-id","subject":"Explicit","sender":{"emailAddress":{"address":"sender@example.com"}},"receivedDateTime":"2026-07-19T01:02:03Z","isRead":false,"hasAttachments":false,"body":{"contentType":"text","content":"body"}}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	account := &model.AccountCredentials{ID: 99, OwnerKey: "user:1"}
	if _, err := service.graphGetMessage(context.Background(), account, "token", "graph:message-id"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || !strings.HasPrefix(requests[0], "GET ") {
		t.Fatalf("opening a message changed read state implicitly: %#v", requests)
	}
	if err := service.graphSetRead(context.Background(), account, "token", "graph:message-id", false); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || !strings.HasPrefix(requests[1], "PATCH ") || readValue != false {
		t.Fatalf("explicit read-state request mismatch: requests=%#v isRead=%#v", requests, readValue)
	}
	if err := service.graphDeleteMessage(context.Background(), account, "token", "graph:message-id"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 || !strings.HasPrefix(requests[2], "DELETE ") {
		t.Fatalf("explicit permanent-delete request mismatch: %#v", requests)
	}
}

func TestOAuthRefreshRequestMapping(t *testing.T) {
	service := &Service{httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "login.microsoftonline.com" {
			t.Fatalf("unexpected token host: %s", request.URL.Host)
		}
		payload, _ := io.ReadAll(request.Body)
		values, err := url.ParseQuery(string(payload))
		if err != nil {
			t.Fatal(err)
		}
		if values.Get("client_id") != "client-id" || values.Get("refresh_token") != "refresh-token" || values.Get("scope") != graphReadScope {
			t.Fatalf("token request mismatch: %s", payload)
		}
		body := `{"access_token":"access-token","scope":"https://graph.microsoft.com/Mail.ReadWrite"}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	result, err := service.RefreshAccessToken(context.Background(), &model.AccountCredentials{ClientID: "client-id", RefreshToken: "refresh-token"}, graphReadScope)
	if err != nil || result.AccessToken != "access-token" {
		t.Fatalf("token refresh mismatch: %#v %v", result, err)
	}
}

func TestExplicitGraphSendSkipsSMTP(t *testing.T) {
	requests := 0
	service := &Service{httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch request.URL.Host {
		case "login.microsoftonline.com":
			payload, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(payload))
			if values.Get("scope") != graphSendScope {
				t.Fatalf("explicit Graph send requested unexpected scope: %s", values.Get("scope"))
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"graph-token","scope":"https://graph.microsoft.com/Mail.Send"}`))}, nil
		case "graph.microsoft.com":
			return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil))}, nil
		default:
			t.Fatalf("unexpected explicit Graph host: %s", request.URL.Host)
			return nil, nil
		}
	})}}
	result, err := service.SendMessage(context.Background(), &model.AccountCredentials{ClientID: "client-id", RefreshToken: "refresh-token", Email: "sender@example.com"}, SendRequest{
		To: "receiver@example.com", Subject: "Graph only", Text: "body", Transport: "graph",
	})
	if err != nil || result.Transport != "graph" || requests != 2 {
		t.Fatalf("explicit Graph send mismatch: %#v requests=%d err=%v", result, requests, err)
	}
}

func TestAutomaticSendFallsBackToGraph(t *testing.T) {
	tokenScopes := make([]string, 0, 2)
	service := &Service{httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "login.microsoftonline.com":
			payload, _ := io.ReadAll(request.Body)
			values, _ := url.ParseQuery(string(payload))
			scope := values.Get("scope")
			tokenScopes = append(tokenScopes, scope)
			responseScope := "https://outlook.office.com/SMTP.Send"
			if scope == graphSendScope {
				responseScope = "https://graph.microsoft.com/Mail.Send"
			}
			body := `{"access_token":"access-token","scope":"` + responseScope + `"}`
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		case "graph.microsoft.com":
			return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil))}, nil
		default:
			t.Fatalf("unexpected fallback host: %s", request.URL.Host)
			return nil, nil
		}
	})}}
	result, err := service.SendMessage(context.Background(), &model.AccountCredentials{ClientID: "client-id", RefreshToken: "refresh-token", Email: "sender@example.com"}, SendRequest{
		To: "receiver@example.com", Subject: "Automatic fallback", Text: "body",
	})
	if err != nil || result.Transport != "graph" || len(tokenScopes) != 2 || tokenScopes[0] != smtpScope || tokenScopes[1] != graphSendScope {
		t.Fatalf("automatic Graph fallback mismatch: %#v scopes=%#v err=%v", result, tokenScopes, err)
	}
}

func TestIMAPXOAUTH2Payload(t *testing.T) {
	mechanism, payload, err := (&xoauth2SASL{username: "user@example.com", token: "access-token"}).Start()
	if err != nil || mechanism != "XOAUTH2" || string(payload) != "user=user@example.com\x01auth=Bearer access-token\x01\x01" {
		t.Fatalf("XOAUTH2 payload mismatch: %s %q %v", mechanism, payload, err)
	}
}
