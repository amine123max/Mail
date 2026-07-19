package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"

	"github.com/amine123max/Mail/server/internal/mailservice"
	"github.com/amine123max/Mail/server/internal/store"
)

type claimedMailOperation struct {
	ownerKey    string
	operationID string
}

type sendMessageResponse struct {
	Status    string   `json:"status"`
	MessageID string   `json:"messageId"`
	Accepted  []string `json:"accepted"`
	Transport string   `json:"transport,omitempty"`
}

func (s *Server) testAccount(response http.ResponseWriter, request *http.Request) error {
	account, err := s.account(request)
	if err != nil {
		return err
	}
	result, err := s.mail.TestAccount(request.Context(), account)
	if err != nil {
		return err
	}
	result["status"] = "ok"
	writeJSON(response, http.StatusOK, result)
	return nil
}

func (s *Server) listFolders(response http.ResponseWriter, request *http.Request) error {
	account, err := s.account(request)
	if err != nil {
		return err
	}
	folders, err := s.mail.ListFolders(request.Context(), account)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"folders": folders})
	return nil
}

func (s *Server) listMessages(response http.ResponseWriter, request *http.Request) error {
	account, err := s.account(request)
	if err != nil {
		return err
	}
	query := request.URL.Query()
	folder := query.Get("folder")
	if folder == "" {
		folder = "INBOX"
	}
	page, err := queryInteger(query.Get("page"), 1, 1, 1_000_000)
	if err != nil {
		return validation("邮件页码无效")
	}
	pageSize, err := queryInteger(query.Get("pageSize"), 30, 5, 100)
	if err != nil || len([]rune(query.Get("query"))) > 200 {
		return validation("邮件分页或搜索参数无效")
	}
	result, err := s.mail.ListMessages(request.Context(), account, folder, page, pageSize, query.Get("query"))
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, result)
	return nil
}

func (s *Server) getMessage(response http.ResponseWriter, request *http.Request) error {
	account, err := s.account(request)
	if err != nil {
		return err
	}
	uid := request.PathValue("uid")
	folder := request.URL.Query().Get("folder")
	if folder == "" {
		folder = "INBOX"
	}
	if uid == "" || len(uid) > 1000 || folder == "" || len(folder) > 1000 {
		return validation("邮件 UID 或文件夹无效")
	}
	message, err := s.mail.GetMessage(request.Context(), account, folder, uid)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"message": message})
	return nil
}

func (s *Server) readMessage(response http.ResponseWriter, request *http.Request) error {
	account, err := s.account(request)
	if err != nil {
		return err
	}
	uid := request.PathValue("uid")
	var body struct {
		Folder      string `json:"folder"`
		Read        *bool  `json:"read"`
		OperationID string `json:"operationId"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if uid == "" || len(uid) > 1000 || body.Folder == "" || len(body.Folder) > 1000 || body.Read == nil {
		return validation("邮件已读状态参数无效")
	}
	operation, completed, err := s.claimMailOperation(request, body.OperationID, "read", strconv.FormatInt(account.ID, 10), uid, body.Folder, strconv.FormatBool(*body.Read))
	if err != nil {
		return err
	}
	if completed {
		writeJSON(response, http.StatusOK, map[string]any{"read": *body.Read, "unread": !*body.Read})
		return nil
	}
	if err := s.mail.SetMessageRead(request.Context(), account, body.Folder, uid, *body.Read); err != nil {
		s.releaseMailOperation(request, operation, err)
		return err
	}
	if err := s.completeMailOperation(request, operation); err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"read": *body.Read, "unread": !*body.Read})
	return nil
}

func (s *Server) deleteMessage(response http.ResponseWriter, request *http.Request) error {
	account, err := s.account(request)
	if err != nil {
		return err
	}
	uid := request.PathValue("uid")
	folder := request.URL.Query().Get("folder")
	operationID := request.URL.Query().Get("operationId")
	if uid == "" || len(uid) > 1000 || folder == "" || len(folder) > 1000 {
		return validation("永久删除邮件参数无效")
	}
	operation, completed, err := s.claimMailOperation(request, operationID, "delete", strconv.FormatInt(account.ID, 10), uid, folder)
	if err != nil {
		return err
	}
	if completed {
		response.WriteHeader(http.StatusNoContent)
		return nil
	}
	if err := s.mail.DeleteMessage(request.Context(), account, folder, uid); err != nil {
		s.releaseMailOperation(request, operation, err)
		return err
	}
	if err := s.completeMailOperation(request, operation); err != nil {
		return err
	}
	response.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) moveMessage(response http.ResponseWriter, request *http.Request) error {
	account, err := s.account(request)
	if err != nil {
		return err
	}
	uid := request.PathValue("uid")
	var body struct {
		Folder       string `json:"folder"`
		TargetFolder string `json:"targetFolder"`
		OperationID  string `json:"operationId"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if uid == "" || len(uid) > 1000 || body.Folder == "" || len(body.Folder) > 1000 || body.TargetFolder == "" || len(body.TargetFolder) > 1000 {
		return validation("邮件移动参数无效")
	}
	operation, completed, err := s.claimMailOperation(request, body.OperationID, "move", strconv.FormatInt(account.ID, 10), uid, body.Folder, body.TargetFolder)
	if err != nil {
		return err
	}
	if completed {
		response.WriteHeader(http.StatusNoContent)
		return nil
	}
	if err := s.mail.MoveMessage(request.Context(), account, body.Folder, uid, body.TargetFolder); err != nil {
		s.releaseMailOperation(request, operation, err)
		return err
	}
	if err := s.completeMailOperation(request, operation); err != nil {
		return err
	}
	response.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) flagMessage(response http.ResponseWriter, request *http.Request) error {
	account, err := s.account(request)
	if err != nil {
		return err
	}
	uid := request.PathValue("uid")
	var body struct {
		Folder      string `json:"folder"`
		Flagged     *bool  `json:"flagged"`
		OperationID string `json:"operationId"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if uid == "" || len(uid) > 1000 || body.Folder == "" || len(body.Folder) > 1000 || body.Flagged == nil {
		return validation("邮件标记参数无效")
	}
	operation, completed, err := s.claimMailOperation(request, body.OperationID, "flag", strconv.FormatInt(account.ID, 10), uid, body.Folder, strconv.FormatBool(*body.Flagged))
	if err != nil {
		return err
	}
	if completed {
		writeJSON(response, http.StatusOK, map[string]any{"flagged": *body.Flagged})
		return nil
	}
	if err := s.mail.SetMessageFlag(request.Context(), account, body.Folder, uid, *body.Flagged); err != nil {
		s.releaseMailOperation(request, operation, err)
		return err
	}
	if err := s.completeMailOperation(request, operation); err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"flagged": *body.Flagged})
	return nil
}

func (s *Server) sendMessage(response http.ResponseWriter, request *http.Request) error {
	account, err := s.account(request)
	if err != nil {
		return err
	}
	var body mailservice.SendRequest
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	body.OperationID = strings.TrimSpace(body.OperationID)
	if !validRequestID(body.OperationID) {
		return sendValidation("operationId", "发件请求缺少有效的 operationId")
	}
	body.Transport = strings.ToLower(strings.TrimSpace(body.Transport))
	if err := validateSendRecipients("to", body.To, true); err != nil {
		return err
	}
	if err := validateSendRecipients("cc", body.CC, false); err != nil {
		return err
	}
	if err := validateSendRecipients("bcc", body.BCC, false); err != nil {
		return err
	}
	if len([]rune(body.Subject)) > 500 || strings.ContainsAny(body.Subject, "\r\n") {
		return sendValidation("subject", "主题不能超过 500 个字符或包含换行")
	}
	if len(body.Text) > 2_000_000 {
		return sendValidation("text", "纯文本正文不能超过 2 MB")
	}
	if len(body.HTML) > 2_000_000 {
		return sendValidation("html", "HTML 正文不能超过 2 MB")
	}
	if strings.TrimSpace(body.Text) == "" && strings.TrimSpace(body.HTML) == "" {
		return sendValidation("text", "邮件正文不能为空")
	}
	if len(body.Attachments) > 5 {
		return sendValidation("attachments", "附件数量不能超过 5 个")
	}
	if body.Transport != "" && body.Transport != "auto" && body.Transport != "smtp" && body.Transport != "graph" {
		return sendValidation("transport", "发件通道只能是 auto、smtp 或 graph")
	}
	filenameInvalid := regexp.MustCompile(`[\x00-\x1f\x7f]`)
	contentTypePattern := regexp.MustCompile(`^[\w.+-]+/[\w.+-]+$`)
	totalBytes := 0
	for index, attachment := range body.Attachments {
		if strings.TrimSpace(attachment.Filename) == "" || len([]rune(attachment.Filename)) > 255 || filenameInvalid.MatchString(attachment.Filename) || !contentTypePattern.MatchString(attachment.ContentType) || attachment.Size < 1 || attachment.Size > 3*1024*1024 || len(attachment.ContentBase64) < 1 || len(attachment.ContentBase64) > 4_194_304 {
			return sendValidation(fmt.Sprintf("attachments[%d]", index), "附件名称、类型、大小或内容不正确")
		}
		decoded, err := base64.StdEncoding.DecodeString(attachment.ContentBase64)
		if err != nil || abs(len(decoded)-attachment.Size) > 2 {
			return sendValidation(fmt.Sprintf("attachments[%d].size", index), "附件大小与内容不一致")
		}
		totalBytes += len(decoded)
	}
	if totalBytes > 3*1024*1024 {
		return sendValidation("attachments", "附件总大小不能超过 3 MB")
	}
	fingerprint, err := sendOperationFingerprint(body)
	if err != nil {
		return err
	}
	operation, completed, err := s.claimMailOperation(request, body.OperationID, "send", strconv.FormatInt(account.ID, 10), fingerprint)
	if err != nil {
		return err
	}
	if completed {
		stored, err := s.store.MailOperationResult(request.Context(), operation.ownerKey, operation.operationID)
		if err != nil {
			return err
		}
		var replay sendMessageResponse
		if err := json.Unmarshal([]byte(stored), &replay); err != nil {
			return err
		}
		writeJSON(response, http.StatusCreated, replay)
		return nil
	}
	if err := s.store.StartMailOperation(request.Context(), operation.ownerKey, operation.operationID); err != nil {
		return err
	}
	result, err := s.mail.SendMessage(request.Context(), account, body)
	if err != nil {
		s.releaseMailOperation(request, operation, err)
		return err
	}
	payload := sendMessageResponse{Status: "sent", MessageID: result.MessageID, Accepted: result.Accepted, Transport: result.Transport}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := s.store.CompleteMailOperationWithResult(request.Context(), operation.ownerKey, operation.operationID, string(encoded)); err != nil {
		return err
	}
	writeJSON(response, http.StatusCreated, payload)
	return nil
}

func sendOperationFingerprint(body mailservice.SendRequest) (string, error) {
	body.OperationID = ""
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func validateSendRecipients(field, value string, required bool) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if required {
			return sendValidation(field, "至少需要一个有效收件人")
		}
		return nil
	}
	if len(trimmed) > 2000 {
		return sendValidation(field, "收件人字段不能超过 2000 个字符")
	}
	addresses, err := mail.ParseAddressList(strings.ReplaceAll(trimmed, ";", ","))
	if err != nil || len(addresses) == 0 {
		return sendValidation(field, "收件人地址格式不正确")
	}
	return nil
}

func sendValidation(field, message string) error {
	return &ValidationError{Message: message, Details: []map[string]any{{"field": field, "message": message}}}
}

func (s *Server) deviceCode(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		ClientID string `json:"clientId"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if len(body.ClientID) < 8 || len(body.ClientID) > 200 {
		return validation("Client ID 格式不正确")
	}
	result, err := s.mail.RequestDeviceCode(request.Context(), body.ClientID)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, result)
	return nil
}

func (s *Server) pollDeviceCode(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		ClientID   string `json:"clientId"`
		DeviceCode string `json:"deviceCode"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if len(body.ClientID) < 8 || len(body.ClientID) > 200 || len(body.DeviceCode) < 8 {
		return validation("授权轮询参数不正确")
	}
	result, status, err := s.mail.PollDeviceCode(request.Context(), body.ClientID, body.DeviceCode)
	if err != nil {
		return err
	}
	writeJSON(response, status, result)
	return nil
}

func queryInteger(value string, fallback, minimum, maximum int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, strconv.ErrSyntax
	}
	return parsed, nil
}

func (s *Server) claimMailOperation(request *http.Request, operationID, kind string, fields ...string) (claimedMailOperation, bool, error) {
	if operationID == "" {
		return claimedMailOperation{}, false, nil
	}
	if !validRequestID(operationID) {
		return claimedMailOperation{}, false, validation("operationId 格式无效")
	}
	digest := sha256.Sum256([]byte(strings.Join(append([]string{kind}, fields...), "\x00")))
	ownerKey := identityFrom(request).OwnerKey
	claim, err := s.store.ClaimMailOperation(request.Context(), ownerKey, operationID, kind, hex.EncodeToString(digest[:]))
	if errors.Is(err, store.ErrMailOperationInProgress) {
		return claimedMailOperation{}, false, &APIError{Message: "该邮件操作正在处理中", Code: "OPERATION_IN_PROGRESS", Status: http.StatusConflict, Retryable: true}
	}
	if errors.Is(err, store.ErrMailOperationConflict) {
		return claimedMailOperation{}, false, apiFailure(http.StatusConflict, "OPERATION_ID_REUSED", "operationId 已被其他邮件操作使用", nil)
	}
	if err != nil {
		return claimedMailOperation{}, false, err
	}
	operation := claimedMailOperation{ownerKey: ownerKey, operationID: operationID}
	return operation, claim == store.MailOperationCompleted, nil
}

func (s *Server) completeMailOperation(request *http.Request, operation claimedMailOperation) error {
	if operation.operationID == "" {
		return nil
	}
	return s.store.CompleteMailOperation(request.Context(), operation.ownerKey, operation.operationID)
}

func (s *Server) releaseMailOperation(request *http.Request, operation claimedMailOperation, failure error) {
	if operation.operationID == "" {
		return
	}
	var mailError *mailservice.Error
	if !errors.As(failure, &mailError) || mailError.Status == http.StatusTooManyRequests || mailError.Status >= 500 {
		return
	}
	if err := s.store.ReleaseMailOperation(request.Context(), operation.ownerKey, operation.operationID); err != nil {
		log.Printf("Mail API operation release failed requestId=%s: %v", requestIDFrom(request), err)
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
