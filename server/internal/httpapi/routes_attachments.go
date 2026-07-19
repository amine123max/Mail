package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/amine123max/Mail/server/internal/mailservice"
	"github.com/amine123max/Mail/server/internal/store"
)

const (
	maxDesktopAttachmentUploadBytes = 3 << 20
	desktopAttachmentUploadTTL      = 7 * 24 * time.Hour
)

type desktopAttachmentUploadResponse struct {
	UploadID    string `json:"uploadId"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
}

func (s *Server) uploadDesktopAttachment(response http.ResponseWriter, request *http.Request) error {
	s.cleanupExpiredDesktopAttachmentUploads(request.Context())
	if request.ContentLength < 1 || request.ContentLength > maxDesktopAttachmentUploadBytes {
		return apiFailure(http.StatusRequestEntityTooLarge, "ATTACHMENT_UPLOAD_TOO_LARGE", "附件必须小于 3 MB", nil)
	}
	encodedFilename := strings.TrimSpace(request.Header.Get("X-Aillive-Attachment-Name"))
	decodedFilename, err := base64.RawURLEncoding.DecodeString(encodedFilename)
	if err != nil || len(decodedFilename) == 0 || len(decodedFilename) > 1000 {
		return validation("附件文件名无效")
	}
	filename := safeDownloadFilename(string(decodedFilename))
	if len([]rune(filename)) > 255 {
		return validation("附件文件名过长")
	}
	contentType, ok := validatedAttachmentContentType(request.Header.Get("Content-Type"))
	if !ok {
		return validation("附件 Content-Type 无效")
	}
	uploadID := newRequestID()
	directory := filepath.Join(s.cfg.DataDir, "desktop-attachments")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	filePath := s.desktopAttachmentPath(uploadID)
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cleanup := func() {
		file.Close()
		os.Remove(filePath)
	}
	hash := sha256.New()
	request.Body = http.MaxBytesReader(response, request.Body, maxDesktopAttachmentUploadBytes+1)
	written, err := io.Copy(io.MultiWriter(file, hash), request.Body)
	if err != nil {
		cleanup()
		return validation("附件上传内容无效")
	}
	if written != request.ContentLength || written < 1 || written > maxDesktopAttachmentUploadBytes {
		cleanup()
		return validation("附件上传大小不一致")
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(filePath)
		return err
	}
	now := time.Now().UTC()
	upload := store.DesktopAttachmentUpload{
		ID:          uploadID,
		OwnerKey:    identityFrom(request).OwnerKey,
		Filename:    filename,
		ContentType: contentType,
		Size:        written,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		CreatedAt:   now.Format(time.RFC3339Nano),
		ExpiresAt:   now.Add(desktopAttachmentUploadTTL).Format(time.RFC3339Nano),
	}
	if err := s.store.CreateDesktopAttachmentUpload(request.Context(), upload); err != nil {
		os.Remove(filePath)
		return err
	}
	writeJSON(response, http.StatusCreated, desktopAttachmentUploadResponse{
		UploadID: upload.ID, Filename: upload.Filename, ContentType: upload.ContentType, Size: upload.Size,
	})
	return nil
}

func (s *Server) deleteDesktopAttachmentUpload(response http.ResponseWriter, request *http.Request) error {
	uploadID := strings.TrimSpace(request.PathValue("uploadId"))
	if !validRequestID(uploadID) {
		return validation("附件上传标识无效")
	}
	deleted, err := s.store.DeleteDesktopAttachmentUpload(request.Context(), identityFrom(request).OwnerKey, uploadID)
	if err != nil {
		return err
	}
	if !deleted {
		return apiFailure(http.StatusNotFound, "ATTACHMENT_UPLOAD_NOT_FOUND", "附件上传不存在或已过期", nil)
	}
	if err := os.Remove(s.desktopAttachmentPath(uploadID)); err != nil && !os.IsNotExist(err) {
		log.Printf("desktop attachment cleanup failed requestId=%s uploadId=%s: %v", requestIDFrom(request), uploadID, err)
	}
	response.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) desktopAttachmentUpload(request *http.Request, uploadID string) (*store.DesktopAttachmentUpload, string, error) {
	if !validRequestID(uploadID) {
		return nil, "", validation("附件上传标识无效")
	}
	s.cleanupExpiredDesktopAttachmentUploads(request.Context())
	upload, err := s.store.DesktopAttachmentUpload(request.Context(), identityFrom(request).OwnerKey, uploadID)
	if err != nil {
		return nil, "", err
	}
	if upload == nil {
		return nil, "", apiFailure(http.StatusNotFound, "ATTACHMENT_UPLOAD_NOT_FOUND", "附件上传不存在或已过期，请重新选择文件", nil)
	}
	filePath := s.desktopAttachmentPath(upload.ID)
	file, err := os.Open(filePath)
	if err != nil {
		return nil, "", apiFailure(http.StatusGone, "ATTACHMENT_UPLOAD_MISSING", "附件临时文件已丢失，请重新选择文件", nil)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != upload.Size || upload.Size < 1 || upload.Size > maxDesktopAttachmentUploadBytes {
		return nil, "", apiFailure(http.StatusGone, "ATTACHMENT_UPLOAD_CORRUPT", "附件临时文件已损坏，请重新选择文件", nil)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, maxDesktopAttachmentUploadBytes+1)); err != nil || hex.EncodeToString(hash.Sum(nil)) != upload.SHA256 {
		return nil, "", apiFailure(http.StatusGone, "ATTACHMENT_UPLOAD_CORRUPT", "附件临时文件已损坏，请重新选择文件", nil)
	}
	return upload, filePath, nil
}

func (s *Server) consumeDesktopAttachmentUploads(ctx context.Context, ownerKey string, uploadIDs []string) {
	for _, uploadID := range uploadIDs {
		deleted, err := s.store.DeleteDesktopAttachmentUpload(ctx, ownerKey, uploadID)
		if err != nil {
			log.Printf("desktop attachment metadata cleanup failed uploadId=%s: %v", uploadID, err)
			continue
		}
		if deleted {
			if err := os.Remove(s.desktopAttachmentPath(uploadID)); err != nil && !os.IsNotExist(err) {
				log.Printf("desktop attachment file cleanup failed uploadId=%s: %v", uploadID, err)
			}
		}
	}
}

func (s *Server) cleanupExpiredDesktopAttachmentUploads(ctx context.Context) {
	ids, err := s.store.DeleteExpiredDesktopAttachmentUploads(ctx, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		log.Printf("desktop attachment expiry cleanup failed: %v", err)
		return
	}
	for _, id := range ids {
		if err := os.Remove(s.desktopAttachmentPath(id)); err != nil && !os.IsNotExist(err) {
			log.Printf("expired desktop attachment cleanup failed uploadId=%s: %v", id, err)
		}
	}
}

func (s *Server) desktopAttachmentPath(uploadID string) string {
	return filepath.Join(s.cfg.DataDir, "desktop-attachments", uploadID+".upload")
}

func (s *Server) downloadAttachment(response http.ResponseWriter, request *http.Request) error {
	account, err := s.account(request)
	if err != nil {
		return err
	}
	uid := strings.TrimSpace(request.PathValue("uid"))
	attachmentID := strings.TrimSpace(request.PathValue("attachmentId"))
	folder := strings.TrimSpace(request.URL.Query().Get("folder"))
	if uid == "" || len(uid) > 1000 || attachmentID == "" || len(attachmentID) > 1500 || folder == "" || len(folder) > 1000 {
		return validation("邮件或附件标识无效")
	}
	attachment, err := s.mail.GetAttachment(request.Context(), account, folder, uid, attachmentID)
	if err != nil {
		return err
	}
	defer attachment.Body.Close()
	if attachment.Size < 0 || attachment.Size > mailservice.MaxAttachmentDownloadBytes {
		return apiFailure(http.StatusRequestEntityTooLarge, "ATTACHMENT_TOO_LARGE", "附件超过桌面端下载上限", nil)
	}
	filename := safeDownloadFilename(attachment.Filename)
	response.Header().Set("Cache-Control", "private, no-store, max-age=0")
	response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	response.Header().Set("Content-Type", safeAttachmentContentType(attachment.ContentType))
	response.Header().Set("Content-Length", strconv.FormatInt(attachment.Size, 10))
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Content-Security-Policy", "sandbox")
	response.WriteHeader(http.StatusOK)
	written, copyErr := io.Copy(response, io.LimitReader(attachment.Body, mailservice.MaxAttachmentDownloadBytes+1))
	if copyErr != nil {
		log.Printf("attachment download interrupted requestId=%s accountId=%d bytes=%d: %v", requestIDFrom(request), account.ID, written, copyErr)
	} else if written != attachment.Size {
		log.Printf("attachment download size mismatch requestId=%s accountId=%d expected=%d actual=%d", requestIDFrom(request), account.ID, attachment.Size, written)
	}
	return nil
}

func safeDownloadFilename(value string) string {
	value = path.Base(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
	filtered := strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || strings.ContainsRune(`<>:"/\\|?*`, character) {
			return -1
		}
		return character
	}, value)
	filtered = strings.Trim(strings.TrimSpace(filtered), ".")
	if filtered == "" {
		filtered = "attachment.bin"
	}
	runes := []rune(filtered)
	if len(runes) > 180 {
		filtered = string(runes[:180])
	}
	stem := strings.ToUpper(strings.TrimSuffix(filtered, path.Ext(filtered)))
	if isWindowsReservedFilename(stem) {
		filtered = "_" + filtered
	}
	return filtered
}

func isWindowsReservedFilename(value string) bool {
	if matches := map[string]struct{}{"CON": {}, "PRN": {}, "AUX": {}, "NUL": {}}; value != "" {
		if _, exists := matches[value]; exists {
			return true
		}
	}
	for _, prefix := range []string{"COM", "LPT"} {
		if strings.HasPrefix(value, prefix) {
			number, err := strconv.Atoi(strings.TrimPrefix(value, prefix))
			if err == nil && number >= 1 && number <= 9 {
				return true
			}
		}
	}
	return false
}

func safeAttachmentContentType(value string) string {
	mediaType, ok := validatedAttachmentContentType(value)
	if !ok {
		return "application/octet-stream"
	}
	return mediaType
}

func validatedAttachmentContentType(value string) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil || !strings.Contains(mediaType, "/") || len(mediaType) > 127 {
		return "", false
	}
	return strings.ToLower(mediaType), true
}
