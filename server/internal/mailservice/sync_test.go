package mailservice

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/amine123max/Mail/server/internal/config"
	"github.com/amine123max/Mail/server/internal/desktopcontract"
	"github.com/amine123max/Mail/server/internal/model"
	"github.com/amine123max/Mail/server/internal/secure"
	"github.com/amine123max/Mail/server/internal/store"
	"github.com/emersion/go-imap"
)

func openSyncTestStore(t *testing.T) *store.Store {
	t.Helper()
	dataDir := t.TempDir()
	box, err := secure.New(dataDir, strings.Repeat("07", 32), false)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := store.Open(dataDir, box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return storage
}

func syncJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestGraphChangesFollowsNextAndDeltaLinks(t *testing.T) {
	deltaRequests := 0
	service := &Service{httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "/mailFolders/inbox/messages/delta"):
			deltaRequests++
			switch deltaRequests {
			case 1:
				if request.URL.Query().Get("$top") != "2" || request.URL.Query().Get("$select") == "" {
					t.Fatalf("initial delta query mismatch: %s", request.URL.String())
				}
				return syncJSONResponse(http.StatusOK, `{
					"value":[{"id":"message-1","parentFolderId":"folder-inbox","subject":"Hello","sender":{"emailAddress":{"name":"Sender","address":"sender@example.com"}},"receivedDateTime":"2026-07-18T01:02:03Z","isRead":false,"hasAttachments":true,"flag":{"flagStatus":"flagged"},"bodyPreview":"Preview","lastModifiedDateTime":"2026-07-18T01:03:00Z"}],
					"@odata.nextLink":"https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta?$skiptoken=next"
				}`), nil
			case 2:
				if request.URL.Query().Get("$skiptoken") != "next" {
					t.Fatalf("nextLink was not followed: %s", request.URL.String())
				}
				return syncJSONResponse(http.StatusOK, `{
					"value":[{"id":"message-removed","@removed":{"reason":"deleted"}}],
					"@odata.deltaLink":"https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=stable"
				}`), nil
			case 3:
				if request.URL.Query().Get("$deltatoken") != "stable" {
					t.Fatalf("deltaLink was not followed: %s", request.URL.String())
				}
				return syncJSONResponse(http.StatusOK, `{
					"value":[],
					"@odata.deltaLink":"https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=stable-2"
				}`), nil
			default:
				t.Fatalf("unexpected delta request %d", deltaRequests)
			}
		case strings.HasSuffix(request.URL.Path, "/mailFolders/inbox"):
			return syncJSONResponse(http.StatusOK, `{"id":"folder-inbox","displayName":"Inbox","wellKnownName":"inbox","unreadItemCount":3,"totalItemCount":9}`), nil
		default:
			t.Fatalf("unexpected Graph request: %s", request.URL.String())
		}
		return nil, nil
	})}}

	batch, state, err := service.graphChanges(context.Background(), "graph-token", "graph:inbox", graphSyncState{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !batch.HasMore || len(batch.Upserts) != 2 || len(batch.DeletedIDs) != 0 || state.FolderFingerprint == "" || !strings.Contains(state.Link, "$skiptoken=next") {
		t.Fatalf("initial Graph delta mismatch: batch=%#v state=%#v", batch, state)
	}
	message := batch.Upserts[1]
	if message.Id != "graph:message-1" || message.Provider != "graph" || message.BodyVersion == nil || message.Payload["hasAttachments"] != true || message.Payload["unread"] != true {
		t.Fatalf("Graph message mapping mismatch: %#v", message)
	}

	batch, state, err = service.graphChanges(context.Background(), "graph-token", "graph:inbox", state, 2)
	if err != nil {
		t.Fatal(err)
	}
	if batch.HasMore || len(batch.Upserts) != 0 || len(batch.DeletedIDs) != 1 || batch.DeletedIDs[0] != "graph:message-removed" || !strings.Contains(state.Link, "$deltatoken=stable") {
		t.Fatalf("Graph next page mismatch: batch=%#v state=%#v", batch, state)
	}

	batch, state, err = service.graphChanges(context.Background(), "graph-token", "graph:inbox", state, 2)
	if err != nil {
		t.Fatal(err)
	}
	if batch.HasMore || len(batch.Upserts) != 0 || len(batch.DeletedIDs) != 0 || !strings.Contains(state.Link, "$deltatoken=stable-2") {
		t.Fatalf("unchanged Graph delta was not minimal: batch=%#v state=%#v", batch, state)
	}
}

func TestGraphExpiredCursorReturnsResetResponse(t *testing.T) {
	storage := openSyncTestStore(t)
	ctx := context.Background()
	if _, err := storage.ImportAccounts(ctx, "user:1", []model.ImportedAccount{{
		Email: "graph-sync@example.invalid", Password: "password", ClientID: "client-id", RefreshToken: "refresh-token-long",
	}}, "skip"); err != nil {
		t.Fatal(err)
	}
	accounts, err := storage.ListAccounts(ctx, "user:1")
	if err != nil || len(accounts) != 1 {
		t.Fatalf("sync account missing: %#v %v", accounts, err)
	}
	account, err := storage.GetAccountCredentials(ctx, "user:1", accounts[0].ID)
	if err != nil || account == nil {
		t.Fatalf("sync credentials missing: %#v %v", account, err)
	}
	cursor, _, err := storage.CommitDesktopSyncCursor(ctx, account.OwnerKey, account.ID, "graph:inbox", "graph", `{"link":"https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=expired"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	service := New(config.Config{}, storage)
	service.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "login.microsoftonline.com":
			return syncJSONResponse(http.StatusOK, `{"access_token":"graph-token","scope":"https://graph.microsoft.com/Mail.ReadWrite"}`), nil
		case "graph.microsoft.com":
			return syncJSONResponse(http.StatusGone, `{"error":{"code":"syncStateNotFound","message":"delta token expired"}}`), nil
		default:
			t.Fatalf("unexpected cursor reset host: %s", request.URL.Host)
			return nil, nil
		}
	})}
	result, err := service.SyncChanges(ctx, account, "graph:inbox", cursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !result.CursorResetRequired || result.ErrorCode == nil || *result.ErrorCode != "CURSOR_RESET_REQUIRED" || result.Provider != "graph" || result.NextCursor != "" || result.LastSyncAt != nil {
		t.Fatalf("expired Graph cursor did not request reset: %#v", result)
	}
	stored, err := storage.GetDesktopSyncCursor(ctx, account.OwnerKey, account.ID, "graph:inbox", cursor)
	if err != nil || stored != nil {
		t.Fatalf("expired cursor remained stored: %#v %v", stored, err)
	}
}

func TestUnreadSummaryFallsBackToUnifiedGraphContract(t *testing.T) {
	graphPages := 0
	service := &Service{cfg: config.Config{}, httpClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "login.microsoftonline.com":
			return syncJSONResponse(http.StatusOK, `{"access_token":"access-token","scope":"offline_access"}`), nil
		case "graph.microsoft.com":
			graphPages++
			if graphPages == 1 {
				return syncJSONResponse(http.StatusOK, `{
					"value":[{"id":"inbox-id","displayName":"Inbox","wellKnownName":"inbox","unreadItemCount":4,"totalItemCount":10},{"id":"archive-id","displayName":"Archive","wellKnownName":"archive","unreadItemCount":1,"totalItemCount":3}],
					"@odata.nextLink":"https://graph.microsoft.com/v1.0/me/mailFolders?$skiptoken=next"
				}`), nil
			}
			return syncJSONResponse(http.StatusOK, `{"value":[{"id":"custom-id","displayName":"Custom","unreadItemCount":2,"totalItemCount":5}]}`), nil
		default:
			t.Fatalf("unexpected unread summary host: %s", request.URL.Host)
			return nil, nil
		}
	})}}
	result := service.UnreadSummary(context.Background(), []model.AccountCredentials{{ID: 42, Email: "mail@example.invalid", ClientID: "client", RefreshToken: "refresh"}})
	if len(result.Accounts) != 1 || result.Accounts[0].Provider != "graph" || result.Accounts[0].ErrorCode != nil || result.Accounts[0].UnreadCount != 7 || len(result.Accounts[0].Folders) != 3 {
		t.Fatalf("unified unread summary mismatch: %#v", result)
	}
	if result.Accounts[0].Folders[0].Folder != "graph:archive" || result.Accounts[0].Folders[2].Folder != "graph:inbox" {
		t.Fatalf("unread folders were not normalized and sorted: %#v", result.Accounts[0].Folders)
	}
}

func TestIMAPUIDValidityPaginationAndFingerprintDiff(t *testing.T) {
	if err := validateIMAPUIDValidity(0, 123); err != nil {
		t.Fatal(err)
	}
	for _, values := range [][2]uint32{{0, 0}, {123, 456}} {
		err := validateIMAPUIDValidity(values[0], values[1])
		if syncErrorCode(err) != "CURSOR_RESET_REQUIRED" {
			t.Fatalf("UIDVALIDITY reset mismatch for %v: %v", values, err)
		}
	}
	changes := []desktopcontract.DesktopSyncChange{{Id: "one"}, {Id: "two"}, {Id: "three"}}
	state := imapSyncState{PendingUpserts: changes, PendingDeleted: []string{"four", "five"}, UnreadCount: 6}
	first, state, _ := takeIMAPPending(state, 2)
	second, state, _ := takeIMAPPending(state, 2)
	third, state, _ := takeIMAPPending(state, 2)
	if len(first.Upserts) != 2 || !first.HasMore || len(second.Upserts) != 1 || len(second.DeletedIDs) != 1 || !second.HasMore || len(third.DeletedIDs) != 1 || third.HasMore || len(state.PendingUpserts) != 0 || len(state.PendingDeleted) != 0 {
		t.Fatalf("IMAP pending pagination mismatch: %#v %#v %#v state=%#v", first, second, third, state)
	}

	base := MessageSummary{UID: uint32(2), Subject: "Subject", From: "Sender", Date: "2026-07-18T01:00:00Z", Unread: true}
	changed := base
	changed.Unread = false
	if imapSummaryFingerprint(base, 10) == imapSummaryFingerprint(changed, 10) || imapSummaryFingerprint(base, 10) == imapSummaryFingerprint(base, 11) {
		t.Fatal("IMAP fingerprint ignored flags or MODSEQ changes")
	}
	known := map[string]string{"1": "a", "2": "b", "3": "c"}
	deleted := reconcileIMAPKnownUIDs(known, []uint32{2, 4}, []uint32{3}, 77)
	if fmt.Sprint(deleted) != "[imap:77:1 imap:77:3]" || len(known) != 1 || known["2"] != "b" {
		t.Fatalf("IMAP UID deletion reconciliation mismatch: deleted=%v known=%v", deleted, known)
	}
}

func TestIMAPChangedSinceCommandIncludesQRESYNCModifiers(t *testing.T) {
	sequenceSet := new(imap.SeqSet)
	sequenceSet.AddRange(1, 0)
	command := (&imapChangedSinceFetch{
		SequenceSet: sequenceSet,
		Items:       []imap.FetchItem{imap.FetchUid, imap.FetchFlags, imapFetchModSequence},
		ModSequence: 987,
		Vanished:    true,
	}).Command()
	encoded := fmt.Sprint(command.Arguments)
	if command.Name != "FETCH" || !strings.Contains(encoded, "CHANGEDSINCE") || !strings.Contains(encoded, "987") || !strings.Contains(encoded, "VANISHED") || !strings.Contains(encoded, "MODSEQ") {
		t.Fatalf("IMAP changed-since command mismatch: %#v", command)
	}
}

func TestStableSyncErrorMapping(t *testing.T) {
	tests := []struct {
		status int
		code   string
		want   string
	}{
		{http.StatusUnauthorized, "InvalidAuthenticationToken", "MAIL_AUTH_REQUIRED"},
		{http.StatusTooManyRequests, "TooManyRequests", "MAIL_RATE_LIMITED"},
		{http.StatusNotFound, "ErrorItemNotFound", "MAIL_FOLDER_NOT_FOUND"},
		{http.StatusGone, "syncStateNotFound", "CURSOR_RESET_REQUIRED"},
		{http.StatusServiceUnavailable, "ServiceUnavailable", "MAIL_TEMPORARY_NETWORK"},
	}
	for _, test := range tests {
		code, mappedStatus := graphFailure(test.status, test.code)
		mapped := stableSyncError(serviceError("upstream", code, mappedStatus))
		if syncErrorCode(mapped) != test.want {
			t.Fatalf("Graph error %d/%s mapped to %s", test.status, test.code, syncErrorCode(mapped))
		}
	}
}
