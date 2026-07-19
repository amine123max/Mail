package mailservice

import (
	"io"
	"net/http"
	"time"

	"github.com/amine123max/Mail/server/internal/config"
	"github.com/amine123max/Mail/server/internal/store"
)

type Error struct {
	Message    string
	Code       string
	Status     int
	RetryAfter *int
}

func (e *Error) Error() string { return e.Message }

type Service struct {
	cfg        config.Config
	store      *store.Store
	httpClient *http.Client
}

func New(cfg config.Config, storage *store.Store) *Service {
	return &Service{cfg: cfg, store: storage, httpClient: &http.Client{Timeout: 30 * time.Second}}
}

type Folder struct {
	Path       string  `json:"path"`
	Name       string  `json:"name"`
	SpecialUse *string `json:"specialUse"`
	Delimiter  string  `json:"delimiter"`
}

type MessageSummary struct {
	UID       any    `json:"uid"`
	Subject   string `json:"subject"`
	From      string `json:"from"`
	FromEmail string `json:"fromEmail"`
	To        string `json:"to"`
	Date      string `json:"date"`
	Unread    bool   `json:"unread"`
	Flagged   bool   `json:"flagged"`
	Preview   string `json:"preview"`
}

type Attachment struct {
	ID          string `json:"id"`
	Index       int    `json:"index"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
}

type AttachmentContent struct {
	ID          string
	Filename    string
	ContentType string
	Size        int64
	Body        io.ReadCloser
}

type MessageDetail struct {
	UID         any          `json:"uid"`
	Subject     string       `json:"subject"`
	From        string       `json:"from"`
	To          string       `json:"to"`
	CC          string       `json:"cc"`
	Date        string       `json:"date"`
	HTML        string       `json:"html"`
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments"`
}

type AttachmentInput struct {
	Filename      string `json:"filename"`
	ContentType   string `json:"contentType"`
	ContentBase64 string `json:"contentBase64,omitempty"`
	UploadID      string `json:"uploadId,omitempty"`
	Size          int    `json:"size"`
	ContentPath   string `json:"-"`
}

type SendRequest struct {
	To          string            `json:"to"`
	CC          string            `json:"cc"`
	BCC         string            `json:"bcc"`
	Subject     string            `json:"subject"`
	Text        string            `json:"text"`
	HTML        string            `json:"html"`
	Attachments []AttachmentInput `json:"attachments"`
	Transport   string            `json:"transport,omitempty"`
	OperationID string            `json:"operationId"`
}

type SendResult struct {
	MessageID string   `json:"messageId"`
	Accepted  []string `json:"accepted"`
	Transport string   `json:"transport,omitempty"`
}

type TokenResult struct {
	AccessToken string
	Scope       string
}

func serviceError(message, code string, status int) *Error {
	return &Error{Message: message, Code: code, Status: status}
}

func serviceErrorWithRetry(message, code string, status int, retryAfter *int) *Error {
	return &Error{Message: message, Code: code, Status: status, RetryAfter: retryAfter}
}
