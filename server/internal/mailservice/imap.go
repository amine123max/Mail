package mailservice

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/amine123max/Mail/server/internal/model"
	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

type xoauth2SASL struct {
	username string
	token    string
}

func (client *xoauth2SASL) Start() (string, []byte, error) {
	return "XOAUTH2", []byte("user=" + client.username + "\x01auth=Bearer " + client.token + "\x01\x01"), nil
}

func (client *xoauth2SASL) Next(_ []byte) ([]byte, error) { return []byte{}, nil }

func (s *Service) connectIMAP(ctx context.Context, account *model.AccountCredentials, accessToken string) (*client.Client, error) {
	var lastError error
	authenticationFailed := false
	for _, host := range s.cfg.IMAPHosts {
		dialer := &net.Dialer{Timeout: 15 * time.Second}
		connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, "993"))
		if err != nil {
			lastError = err
			continue
		}
		deadline := time.Now().Add(30 * time.Second)
		if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
			deadline = contextDeadline
		}
		if err := connection.SetDeadline(deadline); err != nil {
			_ = connection.Close()
			lastError = err
			continue
		}
		tlsConnection := tls.Client(connection, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			_ = connection.Close()
			lastError = err
			continue
		}
		imapClient, err := client.New(tlsConnection)
		if err != nil {
			_ = connection.Close()
			lastError = err
			continue
		}
		imapClient.Timeout = 30 * time.Second
		authClient := &xoauth2SASL{username: account.Email, token: accessToken}
		if err := imapClient.Authenticate(authClient); err != nil {
			lastError = err
			authenticationFailed = true
			_ = imapClient.Terminate()
			continue
		}
		return imapClient, nil
	}
	if authenticationFailed {
		return nil, serviceError("Outlook IMAP 授权已失效", "MAIL_AUTH_REQUIRED", http.StatusUnauthorized)
	}
	return nil, serviceError("无法连接 Outlook IMAP："+errorMessage(lastError), "IMAP_CONNECTION_FAILED", http.StatusBadGateway)
}

func (s *Service) imapFolders(ctx context.Context, account *model.AccountCredentials, token string) ([]Folder, error) {
	connection, err := s.connectIMAP(ctx, account, token)
	if err != nil {
		return nil, err
	}
	defer connection.Logout()
	mailboxes := make(chan *imap.MailboxInfo, 64)
	errChannel := make(chan error, 1)
	go func() { errChannel <- connection.List("", "*", mailboxes) }()
	result := make([]Folder, 0)
	for mailbox := range mailboxes {
		var specialUse *string
		for _, attribute := range mailbox.Attributes {
			if strings.HasPrefix(attribute, "\\") {
				value := attribute
				specialUse = &value
				break
			}
		}
		result = append(result, Folder{Path: mailbox.Name, Name: mailbox.Name, SpecialUse: specialUse, Delimiter: mailbox.Delimiter})
	}
	if err := <-errChannel; err != nil {
		return nil, err
	}
	_ = s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
	return result, nil
}

func (s *Service) imapListMessages(ctx context.Context, account *model.AccountCredentials, token, folder string, page, pageSize int, query string) (map[string]any, error) {
	connection, err := s.connectIMAP(ctx, account, token)
	if err != nil {
		return nil, err
	}
	defer connection.Logout()
	mailbox, err := connection.Select(folder, true)
	if err != nil {
		return nil, err
	}
	if mailbox.Messages == 0 {
		return map[string]any{"messages": []MessageSummary{}, "total": 0, "page": page, "transport": "imap"}, nil
	}
	var uids []uint32
	if strings.TrimSpace(query) != "" {
		criteria := imap.NewSearchCriteria()
		criteria.Text = []string{strings.TrimSpace(query)}
		uids, err = connection.UidSearch(criteria)
		if err != nil {
			return nil, err
		}
		sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
	} else {
		end := int(mailbox.Messages) - (page-1)*pageSize
		if end < 1 {
			return map[string]any{"messages": []MessageSummary{}, "total": int(mailbox.Messages), "page": page, "transport": "imap"}, nil
		}
		start := end - pageSize + 1
		if start < 1 {
			start = 1
		}
		sequenceSet := new(imap.SeqSet)
		sequenceSet.AddRange(uint32(start), uint32(end))
		return s.fetchIMAPSummaries(ctx, connection, account, sequenceSet, false, int(mailbox.Messages), page)
	}
	total := len(uids)
	start, end := (page-1)*pageSize, page*pageSize
	if start >= total {
		return map[string]any{"messages": []MessageSummary{}, "total": total, "page": page, "transport": "imap"}, nil
	}
	if end > total {
		end = total
	}
	sequenceSet := new(imap.SeqSet)
	for _, uid := range uids[start:end] {
		sequenceSet.AddNum(uid)
	}
	return s.fetchIMAPSummaries(ctx, connection, account, sequenceSet, true, total, page)
}

func (s *Service) fetchIMAPSummaries(ctx context.Context, connection *client.Client, account *model.AccountCredentials, sequenceSet *imap.SeqSet, uidMode bool, total, page int) (map[string]any, error) {
	section := &imap.BodySectionName{Peek: true, Partial: []int{0, 20000}}
	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchFlags, imap.FetchInternalDate, section.FetchItem()}
	messages := make(chan *imap.Message, 64)
	errChannel := make(chan error, 1)
	go func() {
		if uidMode {
			errChannel <- connection.UidFetch(sequenceSet, items, messages)
		} else {
			errChannel <- connection.Fetch(sequenceSet, items, messages)
		}
	}()
	result := make([]MessageSummary, 0)
	for message := range messages {
		if message == nil {
			continue
		}
		source := []byte(nil)
		if body := message.GetBody(section); body != nil {
			source, _ = io.ReadAll(io.LimitReader(body, 20000))
		}
		envelope := message.Envelope
		subject, from, fromEmail, to, date := "（无主题）", "", "", "", message.InternalDate
		if envelope != nil {
			subject = fallbackSubject(envelope.Subject)
			if len(envelope.From) > 0 {
				from, fromEmail = formatIMAPAddress(envelope.From[0]), envelope.From[0].Address()
			}
			to = formatIMAPAddresses(envelope.To)
			if !envelope.Date.IsZero() {
				date = envelope.Date
			}
		}
		result = append(result, MessageSummary{UID: message.Uid, Subject: subject, From: from, FromEmail: fromEmail, To: to, Date: date.UTC().Format(time.RFC3339Nano), Unread: !hasFlag(message.Flags, imap.SeenFlag), Flagged: hasFlag(message.Flags, imap.FlaggedFlag), Preview: sourcePreview(source)})
	}
	if err := <-errChannel; err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UID.(uint32) > result[j].UID.(uint32) })
	_ = s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
	return map[string]any{"messages": result, "total": total, "page": page, "transport": "imap"}, nil
}

func (s *Service) imapGetMessage(ctx context.Context, account *model.AccountCredentials, token, folder, uid string) (MessageDetail, error) {
	numericUID, err := strconv.ParseUint(uid, 10, 32)
	if err != nil || numericUID < 1 {
		return MessageDetail{}, serviceError("邮件 UID 无效", "INVALID_MESSAGE_UID", http.StatusBadRequest)
	}
	connection, err := s.connectIMAP(ctx, account, token)
	if err != nil {
		return MessageDetail{}, err
	}
	defer connection.Logout()
	if _, err := connection.Select(folder, false); err != nil {
		return MessageDetail{}, err
	}
	section := &imap.BodySectionName{Peek: true}
	sequenceSet := new(imap.SeqSet)
	sequenceSet.AddNum(uint32(numericUID))
	messages := make(chan *imap.Message, 1)
	errChannel := make(chan error, 1)
	go func() {
		errChannel <- connection.UidFetch(sequenceSet, []imap.FetchItem{imap.FetchUid, section.FetchItem()}, messages)
	}()
	message := <-messages
	if fetchErr := <-errChannel; fetchErr != nil {
		return MessageDetail{}, fetchErr
	}
	if message == nil {
		return MessageDetail{}, serviceError("邮件不存在或已被删除", "MESSAGE_NOT_FOUND", http.StatusNotFound)
	}
	body := message.GetBody(section)
	if body == nil {
		return MessageDetail{}, serviceError("邮件正文不可用", "MESSAGE_NOT_FOUND", http.StatusNotFound)
	}
	source, err := io.ReadAll(io.LimitReader(body, 32<<20))
	if err != nil {
		return MessageDetail{}, err
	}
	parsed, err := parseMIMEMessage(source)
	if err != nil {
		return MessageDetail{}, err
	}
	_ = s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
	return s.parsedDetail(ctx, uint32(numericUID), parsed), nil
}

func (s *Service) imapSetRead(ctx context.Context, account *model.AccountCredentials, token, folder, uid string, read bool) error {
	numericUID, err := strconv.ParseUint(uid, 10, 32)
	if err != nil || numericUID < 1 {
		return serviceError("邮件 UID 无效", "INVALID_MESSAGE_UID", http.StatusBadRequest)
	}
	connection, err := s.connectIMAP(ctx, account, token)
	if err != nil {
		return err
	}
	defer connection.Logout()
	if _, err := connection.Select(folder, false); err != nil {
		return err
	}
	sequenceSet := new(imap.SeqSet)
	sequenceSet.AddNum(uint32(numericUID))
	operation := imap.FormatFlagsOp(imap.RemoveFlags, false)
	if read {
		operation = imap.FormatFlagsOp(imap.AddFlags, false)
	}
	if err := connection.UidStore(sequenceSet, operation, []interface{}{imap.SeenFlag}, nil); err != nil {
		return err
	}
	return s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
}

func (s *Service) imapMoveMessage(ctx context.Context, account *model.AccountCredentials, token, folder, uid, target string) error {
	if strings.EqualFold(folder, target) {
		return s.imapDeleteMessage(ctx, account, token, folder, uid)
	}
	numericUID, err := strconv.ParseUint(uid, 10, 32)
	if err != nil || numericUID < 1 {
		return serviceError("邮件 UID 无效", "INVALID_MESSAGE_UID", http.StatusBadRequest)
	}
	connection, err := s.connectIMAP(ctx, account, token)
	if err != nil {
		return err
	}
	defer connection.Logout()
	if _, err := connection.Select(folder, false); err != nil {
		return err
	}
	sequenceSet := new(imap.SeqSet)
	sequenceSet.AddNum(uint32(numericUID))
	supportsMove, capabilityErr := connection.Support("MOVE")
	if capabilityErr == nil && supportsMove {
		if err := connection.UidMove(sequenceSet, target); err != nil {
			return err
		}
	} else {
		if err := connection.UidCopy(sequenceSet, target); err != nil {
			return err
		}
		if err := connection.UidStore(sequenceSet, imap.AddFlags, []interface{}{imap.DeletedFlag}, nil); err != nil {
			return err
		}
		if err := connection.Expunge(nil); err != nil {
			return err
		}
	}
	return s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
}

func (s *Service) imapDeleteMessage(ctx context.Context, account *model.AccountCredentials, token, folder, uid string) error {
	numericUID, err := strconv.ParseUint(uid, 10, 32)
	if err != nil || numericUID < 1 {
		return serviceError("邮件 UID 无效", "INVALID_MESSAGE_UID", http.StatusBadRequest)
	}
	connection, err := s.connectIMAP(ctx, account, token)
	if err != nil {
		return err
	}
	defer connection.Logout()
	if _, err := connection.Select(folder, false); err != nil {
		return err
	}
	sequenceSet := new(imap.SeqSet)
	sequenceSet.AddNum(uint32(numericUID))
	if err := connection.UidStore(sequenceSet, imap.AddFlags, []interface{}{imap.DeletedFlag}, nil); err != nil {
		return err
	}
	if err := connection.Expunge(nil); err != nil {
		return err
	}
	return s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
}

func (s *Service) imapSetFlag(ctx context.Context, account *model.AccountCredentials, token, folder, uid string, flagged bool) error {
	numericUID, err := strconv.ParseUint(uid, 10, 32)
	if err != nil || numericUID < 1 {
		return serviceError("邮件 UID 无效", "INVALID_MESSAGE_UID", http.StatusBadRequest)
	}
	connection, err := s.connectIMAP(ctx, account, token)
	if err != nil {
		return err
	}
	defer connection.Logout()
	if _, err := connection.Select(folder, false); err != nil {
		return err
	}
	sequenceSet := new(imap.SeqSet)
	sequenceSet.AddNum(uint32(numericUID))
	operation := imap.FormatFlagsOp(imap.RemoveFlags, false)
	if flagged {
		operation = imap.FormatFlagsOp(imap.AddFlags, false)
	}
	if err := connection.UidStore(sequenceSet, operation, []interface{}{imap.FlaggedFlag}, nil); err != nil {
		return err
	}
	return s.store.MarkAccountSynced(ctx, account.OwnerKey, account.ID)
}

func hasFlag(flags []string, target string) bool {
	for _, flag := range flags {
		if strings.EqualFold(flag, target) {
			return true
		}
	}
	return false
}

func formatIMAPAddress(address *imap.Address) string {
	if address == nil {
		return ""
	}
	email := address.Address()
	if address.PersonalName != "" && email != "" {
		return address.PersonalName + " <" + email + ">"
	}
	return email
}

func formatIMAPAddresses(addresses []*imap.Address) string {
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if value := formatIMAPAddress(address); value != "" {
			result = append(result, value)
		}
	}
	return strings.Join(result, ", ")
}

func errorMessage(err error) string {
	if err == nil {
		return "未知错误"
	}
	return err.Error()
}
