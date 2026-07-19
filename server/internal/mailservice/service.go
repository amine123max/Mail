package mailservice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"

	"github.com/amine123max/Mail/server/internal/model"
)

func (s *Service) TestAccount(ctx context.Context, account *model.AccountCredentials) (map[string]any, error) {
	result := map[string]any{"folders": 0, "canSend": false, "scope": "", "receiveTransport": ""}
	var receiveError error
	if token, err := s.RefreshAccessToken(ctx, account, imapScope); err == nil {
		if folders, err := s.imapFolders(ctx, account, token.AccessToken); err == nil {
			result["folders"], result["receiveTransport"] = len(folders), "imap"
		} else {
			receiveError = err
		}
	} else {
		receiveError = err
	}
	if result["receiveTransport"] == "" {
		token, err := s.RefreshAccessToken(ctx, account, graphReadScope)
		if err != nil {
			return nil, combineErrors("收件测试失败", receiveError, err)
		}
		folders, err := s.graphFolders(ctx, account, token.AccessToken)
		if err != nil {
			return nil, combineErrors("收件测试失败", receiveError, err)
		}
		result["folders"], result["receiveTransport"] = len(folders), "graph"
	}
	if token, err := s.RefreshAccessToken(ctx, account, smtpScope); err == nil {
		result["scope"] = token.Scope
		result["canSend"] = token.Scope == "" || strings.Contains(token.Scope, "SMTP.Send")
	} else if token, graphErr := s.RefreshAccessToken(ctx, account, graphSendScope); graphErr == nil {
		result["scope"], result["canSend"] = token.Scope, true
	}
	_ = s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
	return result, nil
}

func (s *Service) ListFolders(ctx context.Context, account *model.AccountCredentials) ([]Folder, error) {
	var imapError error
	if token, err := s.RefreshAccessToken(ctx, account, imapScope); err == nil {
		folders, err := s.imapFolders(ctx, account, token.AccessToken)
		if err == nil {
			return folders, nil
		}
		imapError = err
	} else {
		imapError = err
	}
	token, err := s.RefreshAccessToken(ctx, account, graphReadScope)
	if err != nil {
		return nil, combineErrors("无法读取邮箱文件夹", imapError, err)
	}
	folders, err := s.graphFolders(ctx, account, token.AccessToken)
	if err != nil {
		return nil, combineErrors("无法读取邮箱文件夹", imapError, err)
	}
	return folders, nil
}

func (s *Service) ListMessages(ctx context.Context, account *model.AccountCredentials, folder string, page, pageSize int, query string) (map[string]any, error) {
	if strings.HasPrefix(folder, "graph:") {
		token, err := s.RefreshAccessToken(ctx, account, graphReadScope)
		if err != nil {
			return nil, err
		}
		return s.graphListMessages(ctx, account, token.AccessToken, folder, page, pageSize, query)
	}
	var imapError error
	if token, err := s.RefreshAccessToken(ctx, account, imapScope); err == nil {
		result, err := s.imapListMessages(ctx, account, token.AccessToken, folder, page, pageSize, query)
		if err == nil {
			return result, nil
		}
		imapError = err
	} else {
		imapError = err
	}
	token, err := s.RefreshAccessToken(ctx, account, graphReadScope)
	if err != nil {
		return nil, combineErrors("邮件同步失败", imapError, err)
	}
	result, err := s.graphListMessages(ctx, account, token.AccessToken, folder, page, pageSize, query)
	if err != nil {
		return nil, combineErrors("邮件同步失败", imapError, err)
	}
	return result, nil
}

func (s *Service) GetMessage(ctx context.Context, account *model.AccountCredentials, folder, uid string) (MessageDetail, error) {
	if strings.HasPrefix(folder, "graph:") || strings.HasPrefix(uid, "graph:") {
		token, err := s.RefreshAccessToken(ctx, account, graphReadScope)
		if err != nil {
			return MessageDetail{}, err
		}
		return s.graphGetMessage(ctx, account, token.AccessToken, uid)
	}
	token, err := s.RefreshAccessToken(ctx, account, imapScope)
	if err == nil {
		if detail, imapErr := s.imapGetMessage(ctx, account, token.AccessToken, folder, uid); imapErr == nil {
			return detail, nil
		}
	}
	return MessageDetail{}, serviceError("此邮件来自 IMAP 列表，但 IMAP 当前不可用，请返回列表重新同步以切换 Graph 通道", "IMAP_RELOAD_REQUIRED", http.StatusConflict)
}

func (s *Service) GetAttachment(ctx context.Context, account *model.AccountCredentials, folder, uid, attachmentID string) (AttachmentContent, error) {
	if strings.HasPrefix(folder, "graph:") || strings.HasPrefix(uid, "graph:") {
		token, err := s.RefreshAccessToken(ctx, account, graphReadScope)
		if err != nil {
			return AttachmentContent{}, err
		}
		return s.graphGetAttachment(ctx, account, token.AccessToken, uid, attachmentID)
	}
	token, err := s.RefreshAccessToken(ctx, account, imapScope)
	if err != nil {
		return AttachmentContent{}, err
	}
	return s.imapGetAttachment(ctx, account, token.AccessToken, folder, uid, attachmentID)
}

func (s *Service) SetMessageRead(ctx context.Context, account *model.AccountCredentials, folder, uid string, read bool) error {
	if strings.HasPrefix(folder, "graph:") || strings.HasPrefix(uid, "graph:") {
		token, err := s.RefreshAccessToken(ctx, account, graphReadScope)
		if err != nil {
			return err
		}
		return s.graphSetRead(ctx, account, token.AccessToken, uid, read)
	}
	token, err := s.RefreshAccessToken(ctx, account, imapScope)
	if err != nil {
		return err
	}
	return s.imapSetRead(ctx, account, token.AccessToken, folder, uid, read)
}

func (s *Service) DeleteMessage(ctx context.Context, account *model.AccountCredentials, folder, uid string) error {
	if strings.HasPrefix(folder, "graph:") || strings.HasPrefix(uid, "graph:") {
		token, err := s.RefreshAccessToken(ctx, account, graphReadScope)
		if err != nil {
			return err
		}
		return s.graphDeleteMessage(ctx, account, token.AccessToken, uid)
	}
	token, err := s.RefreshAccessToken(ctx, account, imapScope)
	if err != nil {
		return err
	}
	return s.imapDeleteMessage(ctx, account, token.AccessToken, folder, uid)
}

func (s *Service) MoveMessage(ctx context.Context, account *model.AccountCredentials, folder, uid, target string) error {
	if strings.HasPrefix(folder, "graph:") || strings.HasPrefix(uid, "graph:") {
		token, err := s.RefreshAccessToken(ctx, account, graphReadScope)
		if err != nil {
			return err
		}
		return s.graphMoveMessage(ctx, account, token.AccessToken, folder, uid, target)
	}
	token, err := s.RefreshAccessToken(ctx, account, imapScope)
	if err != nil {
		return err
	}
	return s.imapMoveMessage(ctx, account, token.AccessToken, folder, uid, target)
}

func (s *Service) SetMessageFlag(ctx context.Context, account *model.AccountCredentials, folder, uid string, flagged bool) error {
	if strings.HasPrefix(folder, "graph:") || strings.HasPrefix(uid, "graph:") {
		token, err := s.RefreshAccessToken(ctx, account, graphReadScope)
		if err != nil {
			return err
		}
		return s.graphSetFlag(ctx, account, token.AccessToken, uid, flagged)
	}
	token, err := s.RefreshAccessToken(ctx, account, imapScope)
	if err != nil {
		return err
	}
	return s.imapSetFlag(ctx, account, token.AccessToken, folder, uid, flagged)
}

func (s *Service) SendMessage(ctx context.Context, account *model.AccountCredentials, message SendRequest) (SendResult, error) {
	to, err := recipientAddresses(message.To)
	if err != nil {
		return SendResult{}, err
	}
	if len(to) == 0 {
		return SendResult{}, serviceError("至少需要一个有效收件人", "INVALID_RECIPIENT_ADDRESS", http.StatusBadRequest)
	}
	if _, err := recipientAddresses(message.CC); err != nil {
		return SendResult{}, err
	}
	if _, err := recipientAddresses(message.BCC); err != nil {
		return SendResult{}, err
	}
	switch strings.ToLower(strings.TrimSpace(message.Transport)) {
	case "smtp":
		return s.sendMessageSMTP(ctx, account, message)
	case "graph":
		return s.sendMessageGraph(ctx, account, message)
	}
	var smtpError error
	if result, err := s.sendMessageSMTP(ctx, account, message); err == nil {
		return result, nil
	} else {
		smtpError = err
	}
	result, err := s.sendMessageGraph(ctx, account, message)
	if err != nil {
		return SendResult{}, combineErrors("邮件发送失败", smtpError, err)
	}
	return result, nil
}

func (s *Service) sendMessageSMTP(ctx context.Context, account *model.AccountCredentials, message SendRequest) (SendResult, error) {
	token, err := s.RefreshAccessToken(ctx, account, smtpScope)
	if err != nil {
		return SendResult{}, err
	}
	if token.Scope != "" && !strings.Contains(token.Scope, "SMTP.Send") {
		return SendResult{}, serviceError("微软令牌未授予 SMTP.Send", "SMTP_SCOPE_MISSING", http.StatusForbidden)
	}
	return s.smtpSend(ctx, account, token.AccessToken, message)
}

func (s *Service) sendMessageGraph(ctx context.Context, account *model.AccountCredentials, message SendRequest) (SendResult, error) {
	token, err := s.RefreshAccessToken(ctx, account, graphSendScope)
	if err != nil {
		return SendResult{}, err
	}
	return s.graphSend(ctx, account, token.AccessToken, message)
}

func combineErrors(prefix string, values ...error) error {
	parts := make([]string, 0, len(values))
	status, code := http.StatusBadGateway, "MAIL_SERVICE_ERROR"
	for _, value := range values {
		if value == nil {
			continue
		}
		parts = append(parts, value.Error())
		var serviceErr *Error
		if errors.As(value, &serviceErr) {
			status, code = serviceErr.Status, serviceErr.Code
		}
	}
	return serviceError(fmt.Sprintf("%s：%s", prefix, strings.Join(parts, "；")), code, status)
}

func recipientAddresses(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{}, nil
	}
	addresses, err := mail.ParseAddressList(strings.ReplaceAll(value, ";", ","))
	if err != nil {
		return nil, serviceError("收件人地址格式不正确", "INVALID_RECIPIENT_ADDRESS", http.StatusBadRequest)
	}
	result := make([]string, 0, len(addresses))
	seen := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		email := strings.TrimSpace(address.Address)
		key := strings.ToLower(email)
		if email == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, email)
	}
	return result, nil
}
