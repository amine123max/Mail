package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/amine123max/Mail/server/internal/desktopcontract"
)

func (s *Server) desktopFolderChanges(response http.ResponseWriter, request *http.Request) error {
	account, err := s.account(request)
	if err != nil {
		return err
	}
	folder := strings.TrimSpace(request.PathValue("folder"))
	cursor := strings.TrimSpace(request.URL.Query().Get("cursor"))
	limit, err := queryInteger(request.URL.Query().Get("limit"), 100, 1, 200)
	if err != nil || folder == "" || len(folder) > 1000 || len(cursor) > 128 {
		return validation("同步文件夹、游标或分页参数无效")
	}
	ctx, cancel := context.WithTimeout(request.Context(), 45*time.Second)
	defer cancel()
	result, err := s.mail.SyncChanges(ctx, account, folder, cursor, limit)
	if err != nil {
		return err
	}
	setDesktopSyncHeaders(response, result)
	return writeDesktopConditionalJSON(response, request, result, syncResponseETag(result), len(result.Upserts) == 0 && len(result.DeletedIds) == 0 && !result.HasMore && !result.CursorResetRequired)
}

func (s *Server) desktopUnreadSummary(response http.ResponseWriter, request *http.Request) error {
	ctx, cancel := context.WithTimeout(request.Context(), 60*time.Second)
	defer cancel()
	identity := identityFrom(request)
	accounts, err := s.store.ListAccounts(ctx, identity.OwnerKey)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.ID)
	}
	credentials, err := s.store.GetAccountCredentialsBatch(ctx, identity.OwnerKey, ids)
	if err != nil {
		return err
	}
	result := s.mail.UnreadSummary(ctx, credentials)
	return writeDesktopConditionalJSON(response, request, result, unreadResponseETag(result), true)
}

func writeDesktopConditionalJSON(response http.ResponseWriter, request *http.Request, value any, etag string, allowNotModified bool) error {
	response.Header().Set("ETag", etag)
	response.Header().Set("Vary", "Accept-Encoding")
	response.Header().Set("Cache-Control", "private, no-cache, max-age=0")
	if allowNotModified && strings.TrimSpace(request.Header.Get("If-None-Match")) == etag {
		response.WriteHeader(http.StatusNotModified)
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	if strings.Contains(strings.ToLower(request.Header.Get("Accept-Encoding")), "gzip") && len(encoded) >= 256 {
		var compressed bytes.Buffer
		writer, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
		if err != nil {
			return err
		}
		if _, err := writer.Write(encoded); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
		response.Header().Set("Content-Encoding", "gzip")
		response.Header().Set("Content-Length", strconv.Itoa(compressed.Len()))
		response.WriteHeader(http.StatusOK)
		_, err = response.Write(compressed.Bytes())
		return err
	}
	response.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	response.WriteHeader(http.StatusOK)
	_, err = response.Write(encoded)
	return err
}

func setDesktopSyncHeaders(response http.ResponseWriter, result desktopcontract.DesktopSyncChangesResponse) {
	response.Header().Set("X-Aillive-Next-Cursor", result.NextCursor)
	if result.LastSyncAt != nil {
		response.Header().Set("X-Aillive-Last-Sync-At", *result.LastSyncAt)
	}
}

func syncResponseETag(result desktopcontract.DesktopSyncChangesResponse) string {
	canonical := struct {
		Upserts             []desktopcontract.DesktopSyncChange `json:"upserts"`
		DeletedIds          []string                            `json:"deletedIds"`
		HasMore             bool                                `json:"hasMore"`
		UnreadCount         int                                 `json:"unreadCount"`
		CursorResetRequired bool                                `json:"cursorResetRequired"`
		Provider            string                              `json:"provider"`
	}{result.Upserts, result.DeletedIds, result.HasMore, result.UnreadCount, result.CursorResetRequired, result.Provider}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return `"` + hex.EncodeToString(digest[:]) + `"`
}

func unreadResponseETag(result desktopcontract.DesktopUnreadSummaryResponse) string {
	encoded, _ := json.Marshal(result.Accounts)
	digest := sha256.Sum256(encoded)
	return `"` + hex.EncodeToString(digest[:]) + `"`
}
