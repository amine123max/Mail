package mailservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/amine123max/Mail/server/internal/desktopcontract"
	"github.com/amine123max/Mail/server/internal/model"
	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-imap/commands"
	"github.com/emersion/go-imap/responses"
)

const imapFetchModSequence imap.FetchItem = "MODSEQ"

type imapChangedSinceFetch struct {
	SequenceSet *imap.SeqSet
	Items       []imap.FetchItem
	ModSequence uint64
	Vanished    bool
}

func (command *imapChangedSinceFetch) Command() *imap.Command {
	items := make([]interface{}, len(command.Items))
	for index, item := range command.Items {
		items[index] = imap.RawString(item)
	}
	modifiers := []interface{}{imap.RawString("CHANGEDSINCE"), imap.RawString(strconv.FormatUint(command.ModSequence, 10))}
	if command.Vanished {
		modifiers = append(modifiers, imap.RawString("VANISHED"))
	}
	return &imap.Command{
		Name:      "FETCH",
		Arguments: []interface{}{command.SequenceSet, items, modifiers},
	}
}

type imapSyncFetchHandler struct {
	fetch    *responses.Fetch
	vanished *[]uint32
}

func (handler *imapSyncFetchHandler) Handle(response imap.Resp) error {
	if err := handler.fetch.Handle(response); err != responses.ErrUnhandled {
		return err
	}
	name, fields, ok := imap.ParseNamedResp(response)
	if !ok || name != "VANISHED" || len(fields) == 0 {
		return responses.ErrUnhandled
	}
	value, ok := fields[len(fields)-1].(string)
	if !ok {
		return responses.ErrUnhandled
	}
	sequenceSet, err := imap.ParseSeqSet(value)
	if err != nil {
		return err
	}
	for _, sequence := range sequenceSet.Set {
		if sequence.Stop == 0 || sequence.Stop-sequence.Start > 100_000 {
			continue
		}
		for uid := sequence.Start; uid <= sequence.Stop; uid++ {
			*handler.vanished = append(*handler.vanished, uid)
		}
	}
	return nil
}

func (s *Service) syncIMAPChanges(ctx context.Context, account *model.AccountCredentials, folder string, state imapSyncState, limit int) (syncProviderBatch, imapSyncState, error) {
	if len(state.PendingUpserts) > 0 || len(state.PendingDeleted) > 0 {
		return takeIMAPPending(state, limit)
	}
	token, err := s.RefreshAccessToken(ctx, account, imapScope)
	if err != nil {
		return syncProviderBatch{}, state, err
	}
	connection, err := s.connectIMAP(ctx, account, token.AccessToken)
	if err != nil {
		return syncProviderBatch{}, state, err
	}
	defer connection.Logout()
	condstore, _ := connection.Support("CONDSTORE")
	qresync, _ := connection.Support("QRESYNC")
	if qresync {
		if enabled, enableErr := connection.Enable([]string{"QRESYNC"}); enableErr != nil || !containsFold(enabled, "QRESYNC") {
			qresync = false
		}
	}
	if !qresync && condstore {
		if enabled, enableErr := connection.Enable([]string{"CONDSTORE"}); enableErr != nil || !containsFold(enabled, "CONDSTORE") {
			condstore = false
		}
	}
	status, err := connection.Status(folder, []imap.StatusItem{imap.StatusMessages, imap.StatusUnseen, imap.StatusUidNext, imap.StatusUidValidity})
	if err != nil {
		return syncProviderBatch{}, state, imapFolderError(err)
	}
	mailbox, err := connection.Select(folder, true)
	if err != nil {
		return syncProviderBatch{}, state, imapFolderError(err)
	}
	uidValidity := mailbox.UidValidity
	if uidValidity == 0 {
		uidValidity = status.UidValidity
	}
	if err := validateIMAPUIDValidity(state.UIDValidity, uidValidity); err != nil {
		return syncProviderBatch{}, state, err
	}
	currentUIDs, err := connection.UidSearch(imap.NewSearchCriteria())
	if err != nil {
		return syncProviderBatch{}, state, serviceError("IMAP UID 扫描失败", "MAIL_TEMPORARY_NETWORK", http.StatusBadGateway)
	}
	sort.Slice(currentUIDs, func(i, j int) bool { return currentUIDs[i] > currentUIDs[j] })
	section := &imap.BodySectionName{Peek: true, Partial: []int{0, 20_000}}
	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchFlags, imap.FetchInternalDate, section.FetchItem()}
	if condstore || qresync {
		items = append(items, imapFetchModSequence)
	}
	fullScan := state.UIDValidity == 0 || state.HighestModSeq == 0 || !condstore
	var messages []*imap.Message
	var vanished []uint32
	if !fullScan {
		messages, vanished, err = fetchIMAPChangedSince(connection, items, state.HighestModSeq, qresync)
		if err != nil {
			condstore, qresync, fullScan = false, false, true
		}
	}
	if fullScan {
		messages, err = fetchIMAPByUIDs(connection, currentUIDs, items)
		if err != nil {
			return syncProviderBatch{}, state, serviceError("IMAP 邮件扫描失败", "MAIL_TEMPORARY_NETWORK", http.StatusBadGateway)
		}
	}
	if state.Known == nil {
		state.Known = make(map[string]string, len(currentUIDs))
	}
	upserts := make([]desktopcontract.DesktopSyncChange, 0, len(messages)+1)
	folderChange := imapFolderChange(folder, uidValidity, status)
	folderFingerprint := syncChangeFingerprint(folderChange)
	if state.FolderFingerprint != folderFingerprint {
		upserts = append(upserts, folderChange)
	}
	state.FolderFingerprint = folderFingerprint
	for _, message := range messages {
		if message == nil || message.Uid == 0 {
			continue
		}
		summary, modSequence := imapSyncSummary(message, section)
		fingerprint := imapSummaryFingerprint(summary, modSequence)
		uidKey := strconv.FormatUint(uint64(message.Uid), 10)
		if previous, exists := state.Known[uidKey]; !exists || previous != fingerprint {
			upserts = append(upserts, imapMessageChange(folder, uidValidity, modSequence, summary))
		}
		state.Known[uidKey] = fingerprint
		if modSequence > state.HighestModSeq {
			state.HighestModSeq = modSequence
		}
	}
	deleted := reconcileIMAPKnownUIDs(state.Known, currentUIDs, vanished, uidValidity)
	state.UIDValidity = uidValidity
	state.UIDNext = status.UidNext
	state.UnreadCount = int(status.Unseen)
	state.Condstore = condstore
	state.Qresync = qresync
	state.PendingUpserts = upserts
	state.PendingDeleted = deleted
	return takeIMAPPending(state, limit)
}

func takeIMAPPending(state imapSyncState, limit int) (syncProviderBatch, imapSyncState, error) {
	batch := syncProviderBatch{UnreadCount: state.UnreadCount}
	remaining := limit
	if len(state.PendingUpserts) > 0 {
		count := len(state.PendingUpserts)
		if count > remaining {
			count = remaining
		}
		batch.Upserts = append(batch.Upserts, state.PendingUpserts[:count]...)
		state.PendingUpserts = append([]desktopcontract.DesktopSyncChange(nil), state.PendingUpserts[count:]...)
		remaining -= count
	}
	if remaining > 0 && len(state.PendingDeleted) > 0 {
		count := len(state.PendingDeleted)
		if count > remaining {
			count = remaining
		}
		batch.DeletedIDs = append(batch.DeletedIDs, state.PendingDeleted[:count]...)
		state.PendingDeleted = append([]string(nil), state.PendingDeleted[count:]...)
	}
	batch.HasMore = len(state.PendingUpserts) > 0 || len(state.PendingDeleted) > 0
	return batch, state, nil
}

func validateIMAPUIDValidity(previous, current uint32) error {
	if current == 0 {
		return serviceError("IMAP 未返回 UIDVALIDITY", "CURSOR_RESET_REQUIRED", http.StatusConflict)
	}
	if previous != 0 && previous != current {
		return serviceError("IMAP UIDVALIDITY 已变化", "CURSOR_RESET_REQUIRED", http.StatusConflict)
	}
	return nil
}

func reconcileIMAPKnownUIDs(known map[string]string, currentUIDs, vanished []uint32, uidValidity uint32) []string {
	current := make(map[string]struct{}, len(currentUIDs))
	for _, uid := range currentUIDs {
		current[strconv.FormatUint(uint64(uid), 10)] = struct{}{}
	}
	deleted := make(map[string]struct{})
	for uid := range known {
		if _, exists := current[uid]; exists {
			continue
		}
		deleted[imapStableMessageID(uidValidity, uid)] = struct{}{}
		delete(known, uid)
	}
	for _, uid := range vanished {
		uidKey := strconv.FormatUint(uint64(uid), 10)
		if _, exists := known[uidKey]; !exists {
			continue
		}
		deleted[imapStableMessageID(uidValidity, uidKey)] = struct{}{}
		delete(known, uidKey)
	}
	result := make([]string, 0, len(deleted))
	for id := range deleted {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func fetchIMAPByUIDs(connection *client.Client, uids []uint32, items []imap.FetchItem) ([]*imap.Message, error) {
	if len(uids) == 0 {
		return []*imap.Message{}, nil
	}
	sequenceSet := new(imap.SeqSet)
	for _, uid := range uids {
		sequenceSet.AddNum(uid)
	}
	messages := make(chan *imap.Message, 256)
	done := make(chan error, 1)
	go func() {
		done <- connection.UidFetch(sequenceSet, items, messages)
	}()
	result := make([]*imap.Message, 0, len(uids))
	for message := range messages {
		result = append(result, message)
	}
	return result, <-done
}

func fetchIMAPChangedSince(connection *client.Client, items []imap.FetchItem, modSequence uint64, vanishedEnabled bool) ([]*imap.Message, []uint32, error) {
	sequenceSet := new(imap.SeqSet)
	sequenceSet.AddRange(1, 0)
	messages := make(chan *imap.Message, 256)
	vanished := make([]uint32, 0)
	fetchHandler := &responses.Fetch{Messages: messages, SeqSet: sequenceSet, Uid: true}
	handler := &imapSyncFetchHandler{fetch: fetchHandler, vanished: &vanished}
	command := &commands.Uid{Cmd: &imapChangedSinceFetch{SequenceSet: sequenceSet, Items: items, ModSequence: modSequence, Vanished: vanishedEnabled}}
	done := make(chan error, 1)
	go func() {
		status, err := connection.Execute(command, handler)
		if err == nil {
			err = status.Err()
		}
		close(messages)
		done <- err
	}()
	result := make([]*imap.Message, 0)
	for message := range messages {
		result = append(result, message)
	}
	return result, vanished, <-done
}

func imapSyncSummary(message *imap.Message, section *imap.BodySectionName) (MessageSummary, uint64) {
	source := []byte(nil)
	if body := message.GetBody(section); body != nil {
		source, _ = io.ReadAll(io.LimitReader(body, 20_000))
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
	return MessageSummary{
		UID:       message.Uid,
		Subject:   subject,
		From:      from,
		FromEmail: fromEmail,
		To:        to,
		Date:      date.UTC().Format(time.RFC3339Nano),
		Unread:    !hasFlag(message.Flags, imap.SeenFlag),
		Flagged:   hasFlag(message.Flags, imap.FlaggedFlag),
		Preview:   sourcePreview(source),
	}, imapMessageModSequence(message)
}

func imapMessageModSequence(message *imap.Message) uint64 {
	value, exists := message.Items[imapFetchModSequence]
	if !exists {
		return 0
	}
	if values, ok := value.([]interface{}); ok && len(values) > 0 {
		value = values[0]
	}
	parsed, _ := strconv.ParseUint(strings.TrimSpace(fmt.Sprint(value)), 10, 64)
	return parsed
}

func imapSummaryFingerprint(summary MessageSummary, modSequence uint64) string {
	encoded, _ := json.Marshal(struct {
		Summary     MessageSummary `json:"summary"`
		ModSequence uint64         `json:"modSequence"`
	}{Summary: summary, ModSequence: modSequence})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func imapMessageChange(folder string, uidValidity uint32, modSequence uint64, summary MessageSummary) desktopcontract.DesktopSyncChange {
	uid := int64(summary.UID.(uint32))
	validity := int64(uidValidity)
	var modSequenceValue *int64
	var bodyVersion *string
	if modSequence > 0 && modSequence <= uint64(^uint64(0)>>1) {
		value := int64(modSequence)
		modSequenceValue = &value
		version := strconv.FormatUint(modSequence, 10)
		bodyVersion = &version
	}
	payload := map[string]any{
		"uid":         summary.UID,
		"uidValidity": uidValidity,
		"subject":     summary.Subject,
		"from":        summary.From,
		"fromEmail":   summary.FromEmail,
		"to":          summary.To,
		"date":        summary.Date,
		"unread":      summary.Unread,
		"flagged":     summary.Flagged,
		"preview":     summary.Preview,
	}
	return desktopcontract.DesktopSyncChange{
		Id:         imapStableMessageID(uidValidity, strconv.FormatInt(uid, 10)),
		Kind:       "message",
		ChangeType: "upsert",
		Provider:   "imap",
		Folder:     folder,
		ProviderRef: desktopcontract.DesktopSyncProviderRef{
			Folder:      &folder,
			UidValidity: &validity,
			Uid:         &uid,
			ModSequence: modSequenceValue,
		},
		BodyVersion: bodyVersion,
		Payload:     payload,
	}
}

func imapFolderChange(folder string, uidValidity uint32, status *imap.MailboxStatus) desktopcontract.DesktopSyncChange {
	validity := int64(uidValidity)
	return desktopcontract.DesktopSyncChange{
		Id:         "imap:folder:" + folder,
		Kind:       "folder",
		ChangeType: "upsert",
		Provider:   "imap",
		Folder:     folder,
		ProviderRef: desktopcontract.DesktopSyncProviderRef{
			Folder:      &folder,
			UidValidity: &validity,
		},
		Payload: map[string]any{
			"path":        folder,
			"unreadCount": status.Unseen,
			"totalCount":  status.Messages,
			"uidValidity": uidValidity,
			"uidNext":     status.UidNext,
		},
	}
}

func imapStableMessageID(uidValidity uint32, uid string) string {
	return fmt.Sprintf("imap:%d:%s", uidValidity, uid)
}

func imapFolderError(err error) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "not found") || strings.Contains(message, "doesn't exist") || strings.Contains(message, "nonexistent") || strings.Contains(message, "no such mailbox") {
		return serviceError("IMAP 文件夹不存在", "MAIL_FOLDER_NOT_FOUND", http.StatusNotFound)
	}
	return serviceError("IMAP 文件夹暂时不可用", "MAIL_TEMPORARY_NETWORK", http.StatusBadGateway)
}

func containsFold(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(value, expected) {
			return true
		}
	}
	return false
}

func (s *Service) imapUnreadSummary(ctx context.Context, account *model.AccountCredentials) ([]desktopcontract.DesktopUnreadFolderSummary, error) {
	token, err := s.RefreshAccessToken(ctx, account, imapScope)
	if err != nil {
		return nil, err
	}
	connection, err := s.connectIMAP(ctx, account, token.AccessToken)
	if err != nil {
		return nil, err
	}
	defer connection.Logout()
	mailboxes := make(chan *imap.MailboxInfo, 64)
	done := make(chan error, 1)
	go func() { done <- connection.List("", "*", mailboxes) }()
	paths := make([]string, 0)
	for mailbox := range mailboxes {
		if mailbox != nil {
			paths = append(paths, mailbox.Name)
		}
	}
	if err := <-done; err != nil {
		return nil, serviceError("IMAP 文件夹列表暂时不可用", "MAIL_TEMPORARY_NETWORK", http.StatusBadGateway)
	}
	sort.Strings(paths)
	folders := make([]desktopcontract.DesktopUnreadFolderSummary, 0, len(paths))
	for _, path := range paths {
		status, err := connection.Status(path, []imap.StatusItem{imap.StatusMessages, imap.StatusUnseen})
		if err != nil {
			return nil, imapFolderError(err)
		}
		folders = append(folders, desktopcontract.DesktopUnreadFolderSummary{
			Folder:      path,
			UnreadCount: int(status.Unseen),
			TotalCount:  int(status.Messages),
		})
	}
	return folders, nil
}
