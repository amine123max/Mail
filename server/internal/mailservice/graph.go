package mailservice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/amine123max/Mail/server/internal/model"
	"github.com/microcosm-cc/bluemonday"
)

type graphAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type graphRecipient struct {
	EmailAddress graphAddress `json:"emailAddress"`
}

type graphMessage struct {
	ID               string           `json:"id"`
	Subject          string           `json:"subject"`
	Sender           graphRecipient   `json:"sender"`
	From             graphRecipient   `json:"from"`
	ToRecipients     []graphRecipient `json:"toRecipients"`
	CCRecipients     []graphRecipient `json:"ccRecipients"`
	ReceivedDateTime string           `json:"receivedDateTime"`
	SentDateTime     string           `json:"sentDateTime"`
	IsRead           bool             `json:"isRead"`
	HasAttachments   bool             `json:"hasAttachments"`
	BodyPreview      string           `json:"bodyPreview"`
	Body             struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
	Flag struct {
		FlagStatus string `json:"flagStatus"`
	} `json:"flag"`
}

type graphCollection[T any] struct {
	Value    []T    `json:"value"`
	Count    int    `json:"@odata.count"`
	NextLink string `json:"@odata.nextLink"`
}

type graphAttachment struct {
	Name         string `json:"name"`
	ContentType  string `json:"contentType"`
	Size         int    `json:"size"`
	IsInline     bool   `json:"isInline"`
	ContentID    string `json:"contentId"`
	ContentBytes string `json:"contentBytes"`
}

var graphFolders = []Folder{
	{Path: "graph:inbox", Name: "Inbox", SpecialUse: stringPtr("\\Inbox"), Delimiter: "/"},
	{Path: "graph:sentitems", Name: "Sent", SpecialUse: stringPtr("\\Sent"), Delimiter: "/"},
	{Path: "graph:drafts", Name: "Drafts", SpecialUse: stringPtr("\\Drafts"), Delimiter: "/"},
	{Path: "graph:archive", Name: "Archive", SpecialUse: stringPtr("\\Archive"), Delimiter: "/"},
	{Path: "graph:deleteditems", Name: "Deleted", SpecialUse: stringPtr("\\Trash"), Delimiter: "/"},
}

func (s *Service) graphRequest(ctx context.Context, accessToken, method, resource string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, "https://graph.microsoft.com/v1.0/me/"+resource, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Prefer", `outlook.body-content-type="html"`)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return serviceError("Microsoft Graph 请求失败："+err.Error(), "GRAPH_REQUEST_FAILED", http.StatusBadGateway)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		var graphError struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(payload, &graphError)
		detail := graphError.Error.Message
		if detail == "" {
			detail = graphError.Error.Code
		}
		if detail == "" {
			detail = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		status := http.StatusBadGateway
		switch response.StatusCode {
		case http.StatusBadRequest:
			status = http.StatusBadRequest
		case http.StatusUnauthorized:
			status = http.StatusUnauthorized
		case http.StatusForbidden:
			status = http.StatusForbidden
		case http.StatusNotFound:
			status = http.StatusNotFound
		case http.StatusTooManyRequests:
			status = http.StatusTooManyRequests
		}
		return serviceError("Microsoft Graph 请求失败："+detail, "GRAPH_REQUEST_FAILED", status)
	}
	if output != nil && response.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(io.LimitReader(response.Body, 40<<20)).Decode(output); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) graphFolders(ctx context.Context, account *model.AccountCredentials, accessToken string) ([]Folder, error) {
	var result graphCollection[struct {
		ID string `json:"id"`
	}]
	if err := s.graphRequest(ctx, accessToken, http.MethodGet, "mailFolders?$top=1&$select=id", nil, &result); err != nil {
		return nil, err
	}
	_ = s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
	return graphFolders, nil
}

func (s *Service) graphListMessages(ctx context.Context, account *model.AccountCredentials, accessToken string, folder string, page, pageSize int, query string) (map[string]any, error) {
	folder = graphFolder(folder)
	parameters := url.Values{
		"$top":     {fmt.Sprint(pageSize)},
		"$skip":    {fmt.Sprint((page - 1) * pageSize)},
		"$count":   {"true"},
		"$orderby": {"receivedDateTime desc"},
		"$select":  {"id,subject,sender,from,toRecipients,receivedDateTime,sentDateTime,isRead,flag,bodyPreview"},
	}
	if strings.TrimSpace(query) != "" {
		escaped := strings.ReplaceAll(strings.TrimSpace(query), "'", "''")
		parameters.Set("$filter", fmt.Sprintf("contains(subject,'%s') or contains(bodyPreview,'%s')", escaped, escaped))
	}
	var collection graphCollection[graphMessage]
	resource := "mailFolders/" + url.PathEscape(folder) + "/messages?" + parameters.Encode()
	if err := s.graphRequest(ctx, accessToken, http.MethodGet, resource, nil, &collection); err != nil {
		return nil, err
	}
	messages := make([]MessageSummary, 0, len(collection.Value))
	for _, message := range collection.Value {
		from := message.Sender.EmailAddress
		if from.Address == "" {
			from = message.From.EmailAddress
		}
		date := message.ReceivedDateTime
		if date == "" {
			date = message.SentDateTime
		}
		messages = append(messages, MessageSummary{
			UID: "graph:" + message.ID, Subject: fallbackSubject(message.Subject), From: formatGraphAddress(from),
			FromEmail: from.Address, To: formatGraphRecipients(message.ToRecipients), Date: isoDate(date),
			Unread: !message.IsRead, Flagged: strings.EqualFold(message.Flag.FlagStatus, "flagged"), Preview: previewText(message.BodyPreview),
		})
	}
	total := collection.Count
	if total == 0 {
		total = (page-1)*pageSize + len(messages)
		if collection.NextLink != "" {
			total++
		}
	}
	_ = s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
	return map[string]any{"messages": messages, "total": total, "page": page, "transport": "graph"}, nil
}

func (s *Service) graphGetMessage(ctx context.Context, account *model.AccountCredentials, accessToken, uid string) (MessageDetail, error) {
	id := strings.TrimPrefix(uid, "graph:")
	var message graphMessage
	if err := s.graphRequest(ctx, accessToken, http.MethodGet, "messages/"+url.PathEscape(id)+"?$select=id,subject,sender,from,toRecipients,ccRecipients,receivedDateTime,sentDateTime,body,isRead,hasAttachments", nil, &message); err != nil {
		return MessageDetail{}, err
	}
	inlineImages := make(map[string]string)
	attachments := make([]Attachment, 0)
	if message.HasAttachments || strings.Contains(strings.ToLower(message.Body.Content), "cid:") {
		var collection graphCollection[graphAttachment]
		if err := s.graphRequest(ctx, accessToken, http.MethodGet, "messages/"+url.PathEscape(id)+"/attachments?$select=name,contentType,size,isInline,contentId,contentBytes", nil, &collection); err == nil {
			for _, attachment := range collection.Value {
				if attachment.IsInline && attachment.ContentID != "" && attachment.ContentBytes != "" && safeImageContentType(attachment.ContentType) && attachment.Size <= 2_000_000 {
					inlineImages[normalizeContentID(attachment.ContentID)] = "data:" + attachment.ContentType + ";base64," + attachment.ContentBytes
					continue
				}
				if !attachment.IsInline {
					attachments = append(attachments, Attachment{Index: len(attachments), Filename: fallbackFilename(attachment.Name, len(attachments)), ContentType: fallbackContentType(attachment.ContentType), Size: attachment.Size})
				}
			}
		}
	}
	if !message.IsRead {
		_ = s.graphRequest(ctx, accessToken, http.MethodPatch, "messages/"+url.PathEscape(id), map[string]any{"isRead": true}, nil)
	}
	htmlBody, textBody := "", message.Body.Content
	if strings.EqualFold(message.Body.ContentType, "html") {
		htmlBody = s.renderMessageHTML(ctx, message.Body.Content, inlineImages)
		textBody = stripHTML(message.Body.Content)
	}
	from := message.Sender.EmailAddress
	if from.Address == "" {
		from = message.From.EmailAddress
	}
	date := message.ReceivedDateTime
	if date == "" {
		date = message.SentDateTime
	}
	_ = s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
	return MessageDetail{UID: "graph:" + message.ID, Subject: fallbackSubject(message.Subject), From: formatGraphAddress(from), To: formatGraphRecipients(message.ToRecipients), CC: formatGraphRecipients(message.CCRecipients), Date: isoDate(date), HTML: htmlBody, Text: textBody, Attachments: attachments}, nil
}

func (s *Service) graphMoveMessage(ctx context.Context, account *model.AccountCredentials, accessToken, folder, uid, target string) error {
	id := strings.TrimPrefix(uid, "graph:")
	if graphFolder(folder) == graphFolder(target) {
		if err := s.graphRequest(ctx, accessToken, http.MethodDelete, "messages/"+url.PathEscape(id), nil, nil); err != nil {
			return err
		}
	} else {
		if err := s.graphRequest(ctx, accessToken, http.MethodPost, "messages/"+url.PathEscape(id)+"/move", map[string]any{"destinationId": graphFolder(target)}, nil); err != nil {
			return err
		}
	}
	return s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
}

func (s *Service) graphSetFlag(ctx context.Context, account *model.AccountCredentials, accessToken, uid string, flagged bool) error {
	id := strings.TrimPrefix(uid, "graph:")
	status := "notFlagged"
	if flagged {
		status = "flagged"
	}
	if err := s.graphRequest(ctx, accessToken, http.MethodPatch, "messages/"+url.PathEscape(id), map[string]any{"flag": map[string]string{"flagStatus": status}}, nil); err != nil {
		return err
	}
	return s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
}

func (s *Service) graphSend(ctx context.Context, account *model.AccountCredentials, token string, message SendRequest) (SendResult, error) {
	to, err := recipientAddresses(message.To)
	if err != nil || len(to) == 0 {
		if err != nil {
			return SendResult{}, err
		}
		return SendResult{}, serviceError("至少需要一个有效收件人", "INVALID_RECIPIENT_ADDRESS", http.StatusBadRequest)
	}
	cc, err := recipientAddresses(message.CC)
	if err != nil {
		return SendResult{}, err
	}
	bcc, err := recipientAddresses(message.BCC)
	if err != nil {
		return SendResult{}, err
	}
	attachments := make([]map[string]any, 0, len(message.Attachments))
	for _, attachment := range message.Attachments {
		attachments = append(attachments, map[string]any{"@odata.type": "#microsoft.graph.fileAttachment", "name": safeFilename(attachment.Filename), "contentType": attachment.ContentType, "contentBytes": attachment.ContentBase64})
	}
	contentType, content := "Text", message.Text
	if strings.TrimSpace(message.HTML) != "" {
		contentType, content = "HTML", message.HTML
	}
	payload := map[string]any{"message": map[string]any{
		"subject": message.Subject, "body": map[string]string{"contentType": contentType, "content": content},
		"toRecipients": graphRecipients(to), "ccRecipients": graphRecipients(cc), "bccRecipients": graphRecipients(bcc), "attachments": attachments,
	}, "saveToSentItems": true}
	if err := s.graphRequest(ctx, token, http.MethodPost, "sendMail", payload, nil); err != nil {
		return SendResult{}, err
	}
	return SendResult{MessageID: "graph-accepted", Accepted: to, Transport: "graph"}, nil
}

func graphFolder(folder string) string {
	folder = strings.TrimPrefix(folder, "graph:")
	normalized := strings.ToLower(folder)
	switch {
	case normalized == "inbox":
		return "inbox"
	case strings.Contains(normalized, "sent"):
		return "sentitems"
	case strings.Contains(normalized, "draft"):
		return "drafts"
	case strings.Contains(normalized, "archive"):
		return "archive"
	case strings.Contains(normalized, "deleted") || strings.Contains(normalized, "trash"):
		return "deleteditems"
	default:
		return folder
	}
}

func formatGraphAddress(address graphAddress) string {
	if address.Name != "" && address.Address != "" {
		return address.Name + " <" + address.Address + ">"
	}
	if address.Address != "" {
		return address.Address
	}
	return address.Name
}

func formatGraphRecipients(values []graphRecipient) string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if formatted := formatGraphAddress(value.EmailAddress); formatted != "" {
			result = append(result, formatted)
		}
	}
	return strings.Join(result, ", ")
}

func graphRecipients(addresses []string) []map[string]map[string]string {
	result := make([]map[string]map[string]string, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, map[string]map[string]string{"emailAddress": {"address": address}})
	}
	return result
}

func sanitizeMessageHTML(source string, inlineImages map[string]string) string {
	for contentID, data := range inlineImages {
		pattern := regexp.MustCompile(`(?i)cid:` + regexp.QuoteMeta(contentID))
		source = pattern.ReplaceAllString(source, data)
	}
	source = cssURLPattern.ReplaceAllString(cssImportPattern.ReplaceAllString(source, ""), "none")
	source = imageSourcePattern.ReplaceAllStringFunc(source, func(tag string) string {
		match := imageSourcePattern.FindStringSubmatch(tag)
		if len(match) < 6 {
			return `<span class="mail-image-unavailable">[图片无法安全加载]</span>`
		}
		value := strings.TrimSpace(firstNonEmpty(match[2], match[3], match[4]))
		if !safeDataImage.MatchString(value) {
			return `<span class="mail-image-unavailable">[图片无法安全加载]</span>`
		}
		return "<img" + match[1] + ` src="` + value + `"` + match[5] + ">"
	})
	policy := bluemonday.UGCPolicy()
	policy.AllowElements("table", "tbody", "thead", "tfoot", "tr", "td", "th", "style")
	policy.AllowAttrs("style", "class", "title", "dir", "align").Globally()
	policy.AllowAttrs("src", "alt", "width", "height").OnElements("img")
	policy.AllowDataURIImages()
	policy.RequireNoReferrerOnLinks(true)
	return policy.Sanitize(source)
}

func stripHTML(source string) string {
	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(bluemonday.StrictPolicy().Sanitize(source), " "))
}

func normalizeContentID(value string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.ToLower(value), "cid:")), "<>"))
}

func stringPtr(value string) *string { return &value }

func fallbackFilename(value string, index int) string {
	if value != "" {
		return value
	}
	return fmt.Sprintf("附件-%d", index+1)
}

func fallbackContentType(value string) string {
	if value != "" {
		return value
	}
	return "application/octet-stream"
}

func safeFilename(value string) string {
	value = strings.NewReplacer("/", "_", "\\", "_").Replace(value)
	runes := []rune(value)
	if len(runes) > 255 {
		runes = runes[:255]
	}
	return string(runes)
}

func isoDate(value string) string {
	if value == "" {
		return time.Now().UTC().Format(time.RFC3339Nano)
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC().Format(time.RFC3339Nano)
	}
	return value
}

func previewText(value string) string {
	value = regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(value), " ")
	runes := []rune(value)
	if len(runes) > 220 {
		return string(runes[:220])
	}
	return value
}

func fallbackSubject(value string) string {
	if strings.TrimSpace(value) == "" {
		return "（无主题）"
	}
	return value
}

func decodeAttachment(input AttachmentInput) ([]byte, error) {
	return base64.StdEncoding.DecodeString(input.ContentBase64)
}
