package httpapi

import (
	"encoding/base64"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/amine123max/Mail/server/internal/mailservice"
)

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

func (s *Server) moveMessage(response http.ResponseWriter, request *http.Request) error {
	account, err := s.account(request)
	if err != nil {
		return err
	}
	uid := request.PathValue("uid")
	var body struct {
		Folder       string `json:"folder"`
		TargetFolder string `json:"targetFolder"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if uid == "" || len(uid) > 1000 || body.Folder == "" || len(body.Folder) > 1000 || body.TargetFolder == "" || len(body.TargetFolder) > 1000 {
		return validation("邮件移动参数无效")
	}
	if err := s.mail.MoveMessage(request.Context(), account, body.Folder, uid, body.TargetFolder); err != nil {
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
		Folder  string `json:"folder"`
		Flagged *bool  `json:"flagged"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if uid == "" || len(uid) > 1000 || body.Folder == "" || len(body.Folder) > 1000 || body.Flagged == nil {
		return validation("邮件标记参数无效")
	}
	if err := s.mail.SetMessageFlag(request.Context(), account, body.Folder, uid, *body.Flagged); err != nil {
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
	body.Transport = strings.ToLower(strings.TrimSpace(body.Transport))
	if len(body.To) < 3 || len(body.To) > 2000 || len(body.CC) > 2000 || len(body.BCC) > 2000 || len([]rune(body.Subject)) > 500 || strings.ContainsAny(body.Subject, "\r\n") || len(body.Text) > 2_000_000 || len(body.HTML) > 2_000_000 || strings.TrimSpace(body.Text) == "" && strings.TrimSpace(body.HTML) == "" || len(body.Attachments) > 5 || body.Transport != "" && body.Transport != "auto" && body.Transport != "smtp" && body.Transport != "graph" {
		return validation("邮件内容或收件人参数无效")
	}
	filenameInvalid := regexp.MustCompile(`[\x00-\x1f\x7f]`)
	contentTypePattern := regexp.MustCompile(`^[\w.+-]+/[\w.+-]+$`)
	totalBytes := 0
	for _, attachment := range body.Attachments {
		if strings.TrimSpace(attachment.Filename) == "" || len([]rune(attachment.Filename)) > 255 || filenameInvalid.MatchString(attachment.Filename) || !contentTypePattern.MatchString(attachment.ContentType) || attachment.Size < 1 || attachment.Size > 3*1024*1024 || len(attachment.ContentBase64) < 1 || len(attachment.ContentBase64) > 4_194_304 {
			return validation("附件参数不正确")
		}
		decoded, err := base64.StdEncoding.DecodeString(attachment.ContentBase64)
		if err != nil || abs(len(decoded)-attachment.Size) > 2 {
			return validation("附件大小与内容不一致")
		}
		totalBytes += len(decoded)
	}
	if totalBytes > 3*1024*1024 {
		return validation("附件总大小不能超过 3 MB")
	}
	result, err := s.mail.SendMessage(request.Context(), account, body)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusCreated, map[string]any{"status": "sent", "messageId": result.MessageID, "accepted": result.Accepted, "transport": result.Transport})
	return nil
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

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
