package mailservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/amine123max/Mail/server/internal/desktopcontract"
	"github.com/amine123max/Mail/server/internal/model"
)

type syncProviderBatch struct {
	Upserts     []desktopcontract.DesktopSyncChange
	DeletedIDs  []string
	UnreadCount int
	HasMore     bool
}

type graphSyncState struct {
	Link              string `json:"link"`
	FolderFingerprint string `json:"folderFingerprint,omitempty"`
}

type imapSyncState struct {
	UIDValidity       uint32                              `json:"uidValidity"`
	UIDNext           uint32                              `json:"uidNext"`
	HighestModSeq     uint64                              `json:"highestModSeq"`
	Known             map[string]string                   `json:"known"`
	PendingUpserts    []desktopcontract.DesktopSyncChange `json:"pendingUpserts,omitempty"`
	PendingDeleted    []string                            `json:"pendingDeleted,omitempty"`
	UnreadCount       int                                 `json:"unreadCount"`
	Condstore         bool                                `json:"condstore"`
	Qresync           bool                                `json:"qresync"`
	FolderFingerprint string                              `json:"folderFingerprint,omitempty"`
}

func (s *Service) SyncChanges(ctx context.Context, account *model.AccountCredentials, folder, cursor string, limit int) (desktopcontract.DesktopSyncChangesResponse, error) {
	if s.store == nil {
		return desktopcontract.DesktopSyncChangesResponse{}, serviceError("同步存储不可用", "MAIL_TEMPORARY_NETWORK", http.StatusServiceUnavailable)
	}
	provider := ""
	stateJSON := ""
	if cursor != "" {
		stored, err := s.store.GetDesktopSyncCursor(ctx, account.OwnerKey, account.ID, folder, cursor)
		if err != nil {
			return desktopcontract.DesktopSyncChangesResponse{}, stableSyncError(err)
		}
		if stored == nil {
			return cursorResetResponse(""), nil
		}
		provider, stateJSON = stored.Provider, stored.StateJSON
	}
	var batch syncProviderBatch
	var nextState any
	var err error
	switch provider {
	case "graph":
		var state graphSyncState
		if stateJSON != "" && json.Unmarshal([]byte(stateJSON), &state) != nil {
			return cursorResetResponse("graph"), nil
		}
		batch, state, err = s.syncGraphChanges(ctx, account, folder, state, limit)
		nextState = state
	case "imap":
		var state imapSyncState
		if stateJSON != "" && json.Unmarshal([]byte(stateJSON), &state) != nil {
			return cursorResetResponse("imap"), nil
		}
		batch, state, err = s.syncIMAPChanges(ctx, account, folder, state, limit)
		nextState = state
	default:
		if strings.HasPrefix(folder, "graph:") {
			provider = "graph"
			var state graphSyncState
			batch, state, err = s.syncGraphChanges(ctx, account, folder, state, limit)
			nextState = state
		} else {
			provider = "imap"
			var state imapSyncState
			batch, state, err = s.syncIMAPChanges(ctx, account, folder, state, limit)
			nextState = state
			if err != nil && syncErrorCode(err) != "CURSOR_RESET_REQUIRED" && syncErrorCode(err) != "MAIL_FOLDER_NOT_FOUND" {
				provider = "graph"
				var graphState graphSyncState
				batch, graphState, err = s.syncGraphChanges(ctx, account, "graph:"+graphFolder(folder), graphState, limit)
				nextState = graphState
			}
		}
	}
	if err != nil {
		stableErr := stableSyncError(err)
		if syncErrorCode(stableErr) == "CURSOR_RESET_REQUIRED" {
			_ = s.store.DeleteDesktopSyncCursor(ctx, account.OwnerKey, cursor)
			return cursorResetResponse(provider), nil
		}
		return desktopcontract.DesktopSyncChangesResponse{}, stableErr
	}
	encodedState, err := json.Marshal(nextState)
	if err != nil {
		return desktopcontract.DesktopSyncChangesResponse{}, err
	}
	nextCursor, lastSyncAt, err := s.store.CommitDesktopSyncCursor(ctx, account.OwnerKey, account.ID, folder, provider, string(encodedState), !batch.HasMore)
	if err != nil {
		return desktopcontract.DesktopSyncChangesResponse{}, stableSyncError(err)
	}
	return desktopcontract.DesktopSyncChangesResponse{
		Upserts:             nonNilSyncChanges(batch.Upserts),
		DeletedIds:          nonNilStrings(batch.DeletedIDs),
		NextCursor:          nextCursor,
		HasMore:             batch.HasMore,
		UnreadCount:         batch.UnreadCount,
		ServerTime:          time.Now().UTC().Format(time.RFC3339Nano),
		CursorResetRequired: false,
		ErrorCode:           nil,
		Provider:            provider,
		LastSyncAt:          lastSyncAt,
	}, nil
}

func (s *Service) UnreadSummary(ctx context.Context, accounts []model.AccountCredentials) desktopcontract.DesktopUnreadSummaryResponse {
	result := desktopcontract.DesktopUnreadSummaryResponse{
		Accounts:   make([]desktopcontract.DesktopUnreadAccountSummary, 0, len(accounts)),
		ServerTime: time.Now().UTC().Format(time.RFC3339Nano),
	}
	for index := range accounts {
		account := &accounts[index]
		provider := "imap"
		folders, err := s.imapUnreadSummary(ctx, account)
		if err != nil {
			provider = "graph"
			folders, err = s.graphUnreadSummary(ctx, account)
		}
		entry := desktopcontract.DesktopUnreadAccountSummary{
			AccountId: account.ID,
			Provider:  provider,
			Folders:   nonNilUnreadFolders(folders),
		}
		if err != nil {
			code := syncErrorCode(stableSyncError(err))
			entry.ErrorCode = &code
		} else {
			for _, folder := range folders {
				entry.UnreadCount += folder.UnreadCount
			}
		}
		result.Accounts = append(result.Accounts, entry)
	}
	return result
}

func cursorResetResponse(provider string) desktopcontract.DesktopSyncChangesResponse {
	code := "CURSOR_RESET_REQUIRED"
	return desktopcontract.DesktopSyncChangesResponse{
		Upserts:             []desktopcontract.DesktopSyncChange{},
		DeletedIds:          []string{},
		NextCursor:          "",
		HasMore:             false,
		UnreadCount:         0,
		ServerTime:          time.Now().UTC().Format(time.RFC3339Nano),
		CursorResetRequired: true,
		ErrorCode:           &code,
		Provider:            provider,
		LastSyncAt:          nil,
	}
}

func stableSyncError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return serviceError("邮件同步暂时超时，请稍后重试", "MAIL_TEMPORARY_NETWORK", http.StatusGatewayTimeout)
	}
	var serviceErr *Error
	if errors.As(err, &serviceErr) {
		switch serviceErr.Code {
		case "TOKEN_REFRESH_FAILED", "MAIL_AUTH_REQUIRED":
			return serviceError("邮箱授权已失效，请重新授权", "MAIL_AUTH_REQUIRED", http.StatusUnauthorized)
		case "GRAPH_RATE_LIMITED", "MAIL_RATE_LIMITED":
			return serviceErrorWithRetry("上游邮箱服务正在限流，请稍后重试", "MAIL_RATE_LIMITED", http.StatusTooManyRequests, serviceErr.RetryAfter)
		case "GRAPH_FOLDER_NOT_FOUND", "MAIL_FOLDER_NOT_FOUND":
			return serviceError("邮箱文件夹不存在或已被移动", "MAIL_FOLDER_NOT_FOUND", http.StatusNotFound)
		case "GRAPH_CURSOR_INVALID", "CURSOR_RESET_REQUIRED":
			return serviceError("同步游标已失效，需要重新建立缓存", "CURSOR_RESET_REQUIRED", http.StatusConflict)
		case "IMAP_CONNECTION_FAILED", "GRAPH_TEMPORARY_NETWORK", "MAIL_TEMPORARY_NETWORK":
			return serviceError("暂时无法连接上游邮箱服务", "MAIL_TEMPORARY_NETWORK", http.StatusBadGateway)
		}
		return serviceErr
	}
	return serviceError("邮件同步暂时失败，请稍后重试", "MAIL_TEMPORARY_NETWORK", http.StatusBadGateway)
}

func syncErrorCode(err error) string {
	var serviceErr *Error
	if errors.As(err, &serviceErr) && serviceErr.Code != "" {
		return serviceErr.Code
	}
	return "MAIL_TEMPORARY_NETWORK"
}

func nonNilSyncChanges(values []desktopcontract.DesktopSyncChange) []desktopcontract.DesktopSyncChange {
	if values == nil {
		return []desktopcontract.DesktopSyncChange{}
	}
	return values
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilUnreadFolders(values []desktopcontract.DesktopUnreadFolderSummary) []desktopcontract.DesktopUnreadFolderSummary {
	if values == nil {
		return []desktopcontract.DesktopUnreadFolderSummary{}
	}
	return values
}

func syncChangeFingerprint(change desktopcontract.DesktopSyncChange) string {
	encoded, _ := json.Marshal(change)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
