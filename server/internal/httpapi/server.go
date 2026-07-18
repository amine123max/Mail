package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amine123max/Mail/server/internal/auth"
	"github.com/amine123max/Mail/server/internal/config"
	"github.com/amine123max/Mail/server/internal/desktopcontract"
	"github.com/amine123max/Mail/server/internal/mailservice"
	"github.com/amine123max/Mail/server/internal/model"
	"github.com/amine123max/Mail/server/internal/store"
)

type handler func(http.ResponseWriter, *http.Request) error

type identityKey struct{}
type requestIDKey struct{}
type desktopSessionKey struct{}

type rateRecord struct {
	Count   int
	ResetAt time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	records map[string]rateRecord
	limit   int
	window  time.Duration
}

type Server struct {
	cfg       config.Config
	store     *store.Store
	auth      *auth.Service
	mail      *mailservice.Service
	mux       *http.ServeMux
	authLimit *rateLimiter
	sendLimit *rateLimiter
}

type ValidationError struct {
	Message string
	Details any
}

func (e *ValidationError) Error() string { return e.Message }

type APIError struct {
	Message   string
	Code      string
	Status    int
	Details   any
	Retryable bool
}

func (e *APIError) Error() string { return e.Message }

func New(cfg config.Config, storage *store.Store, authentication *auth.Service, mail *mailservice.Service) *Server {
	server := &Server{
		cfg: cfg, store: storage, auth: authentication, mail: mail, mux: http.NewServeMux(),
		authLimit: &rateLimiter{records: make(map[string]rateRecord), limit: 20, window: 15 * time.Minute},
		sendLimit: &rateLimiter{records: make(map[string]rateRecord), limit: 60, window: time.Hour},
	}
	server.routes()
	return server
}

func (s *Server) Handler() http.Handler {
	var handler http.Handler = s.mux
	if s.cfg.CookiePath != "/" {
		handler = s.stripBasePath(handler)
	}
	handler = s.requestMetadata(handler)
	return s.securityHeaders(handler)
}

func (s *Server) stripBasePath(next http.Handler) http.Handler {
	base := strings.TrimSuffix(s.cfg.CookiePath, "/")
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == base || strings.HasPrefix(request.URL.Path, base+"/") {
			cloned := request.Clone(request.Context())
			cloned.URL.Path = strings.TrimPrefix(request.URL.Path, base)
			cloned.URL.RawPath = ""
			if cloned.URL.Path == "" {
				cloned.URL.Path = "/"
			}
			next.ServeHTTP(response, cloned)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (s *Server) routes() {
	s.handle("GET /api/health", s.health)
	s.handle("GET /api/desktop/capabilities", s.desktopCapabilities)
	s.handle("GET /api/v1/desktop/capabilities", s.desktopCapabilities)
	s.handle("POST /api/v1/desktop/sessions", s.withRateLimit(s.authLimit, s.createDesktopSession))
	s.handle("POST /api/v1/desktop/sessions/migrate", s.withRateLimit(s.authLimit, s.migrateDesktopSession))
	s.handle("POST /api/v1/desktop/sessions/refresh", s.withRateLimit(s.authLimit, s.refreshDesktopSession))
	s.handleDesktopIdentity("DELETE /api/v1/desktop/sessions/current", s.withRateLimit(s.authLimit, s.deleteCurrentDesktopSession))
	s.handleDesktopIdentity("GET /api/v1/desktop/devices", s.listDesktopDevices)
	s.handleDesktopIdentity("DELETE /api/v1/desktop/devices/{deviceId}", s.withRateLimit(s.authLimit, s.deleteDesktopDevice))
	s.handleDesktopIdentity("DELETE /api/v1/desktop/devices", s.withRateLimit(s.authLimit, s.deleteAllDesktopDevices))
	s.handleDesktopIdentity("GET /api/v1/desktop/accounts/{id}/folders/{folder}/changes", s.desktopFolderChanges)
	s.handleDesktopIdentity("GET /api/v1/desktop/unread-summary", s.desktopUnreadSummary)
	s.handle("GET /api/auth/status", s.authStatus)
	s.handle("POST /api/auth/verification/request", s.withRateLimit(s.authLimit, s.requestVerification))
	s.handle("POST /api/auth/setup", s.withRateLimit(s.authLimit, s.setupAdministrator))
	s.handle("POST /api/auth/login", s.withRateLimit(s.authLimit, s.login))
	s.handle("POST /api/auth/register", s.withRateLimit(s.authLimit, s.register))
	s.handle("POST /api/auth/password/reset", s.withRateLimit(s.authLimit, s.resetPassword))
	s.handle("POST /api/auth/guest", s.withRateLimit(s.authLimit, s.guest))
	s.handle("POST /api/auth/logout", s.logout)

	s.handleIdentity("GET /api/announcements", true, false, s.listAnnouncements)
	s.handleIdentity("POST /api/announcements/read", true, false, s.markAnnouncementsRead)
	s.handleIdentity("POST /api/announcements", true, true, s.createAnnouncement)
	s.handleIdentity("GET /api/admin/stats", true, true, s.adminStats)
	s.handleIdentity("GET /api/admin/activity", true, true, s.adminActivity)
	s.handleIdentity("GET /api/admin/users", true, true, s.adminUsers)
	s.handleIdentity("PATCH /api/admin/users/{id}/status", true, true, s.withRateLimit(s.authLimit, s.adminUserStatus))

	s.handleIdentity("GET /api/accounts", false, false, s.listAccounts)
	s.handleIdentity("POST /api/accounts/import", false, false, s.importAccounts)
	s.handleIdentity("POST /api/accounts/export", false, false, s.exportAccounts)
	s.handleIdentity("PUT /api/accounts/order", false, false, s.orderAccounts)
	s.handleIdentity("PATCH /api/accounts/{id}", false, false, s.updateAccount)
	s.handleIdentity("PUT /api/accounts/{id}/token", false, false, s.updateAccountToken)
	s.handleIdentity("DELETE /api/accounts/{id}", false, false, s.deleteAccount)
	s.handleIdentity("PATCH /api/accounts/batch/group", false, false, s.groupAccounts)
	s.handleIdentity("POST /api/accounts/batch/delete", false, false, s.deleteAccounts)

	s.handleIdentity("POST /api/accounts/{id}/test", false, false, s.testAccount)
	s.handleIdentity("GET /api/accounts/{id}/folders", false, false, s.listFolders)
	s.handleIdentity("GET /api/accounts/{id}/messages", false, false, s.listMessages)
	s.handleIdentity("GET /api/accounts/{id}/messages/{uid}", false, false, s.getMessage)
	s.handleIdentity("POST /api/accounts/{id}/messages/{uid}/move", false, false, s.moveMessage)
	s.handleIdentity("PATCH /api/accounts/{id}/messages/{uid}/flag", false, false, s.flagMessage)
	s.handleIdentity("POST /api/accounts/{id}/send", true, false, s.withRateLimit(s.sendLimit, s.sendMessage))
	s.handleIdentity("POST /api/oauth/device-code", false, false, s.withRateLimit(s.authLimit, s.deviceCode))
	s.handleIdentity("POST /api/oauth/poll", false, false, s.withRateLimit(s.authLimit, s.pollDeviceCode))

	s.mux.HandleFunc("/", s.serveFrontend)
}

func (s *Server) handle(pattern string, next handler) {
	s.mux.HandleFunc(pattern, func(response http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/") {
			response.Header().Set("Cache-Control", "no-store, max-age=0")
			response.Header().Set("Pragma", "no-cache")
		}
		if err := next(response, request); err != nil {
			s.writeError(response, request, err)
		}
	})
}

func (s *Server) requestMetadata(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestID := strings.TrimSpace(request.Header.Get("X-Request-Id"))
		if !validRequestID(requestID) {
			requestID = newRequestID()
		}
		response.Header().Set("X-Request-Id", requestID)
		next.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), requestIDKey{}, requestID)))
	})
}

func (s *Server) handleIdentity(pattern string, userOnly, administrator bool, next handler) {
	s.handle(pattern, func(response http.ResponseWriter, request *http.Request) error {
		identity, err := s.auth.Identity(request.Context(), request)
		if err != nil {
			return err
		}
		var desktopSessionID string
		if identity == nil && strings.HasPrefix(strings.TrimSpace(request.Header.Get("Authorization")), "Bearer ") {
			identity, desktopSessionID, err = s.auth.DesktopIdentity(request.Context(), request)
			if err != nil {
				return err
			}
		}
		if identity == nil {
			return &auth.Error{Message: "请先登录或使用游客模式", Code: "IDENTITY_REQUIRED", Status: http.StatusUnauthorized}
		}
		if userOnly && identity.Kind != "user" {
			return &auth.Error{Message: "游客模式仅支持收件，登录后才能发送邮件", Code: "GUEST_SEND_DISABLED", Status: http.StatusForbidden}
		}
		if administrator && (!identity.IsAdmin || identity.UserID == 0) {
			return &auth.Error{Message: "仅管理员可以执行此操作", Code: "ADMIN_REQUIRED", Status: http.StatusForbidden}
		}
		ctx := context.WithValue(request.Context(), identityKey{}, identity)
		if desktopSessionID != "" {
			ctx = context.WithValue(ctx, desktopSessionKey{}, desktopSessionID)
		}
		request = request.WithContext(ctx)
		return next(response, request)
	})
}

func (s *Server) handleDesktopIdentity(pattern string, next handler) {
	s.handle(pattern, func(response http.ResponseWriter, request *http.Request) error {
		identity, sessionID, err := s.auth.DesktopIdentity(request.Context(), request)
		if err != nil {
			return err
		}
		ctx := context.WithValue(request.Context(), identityKey{}, identity)
		ctx = context.WithValue(ctx, desktopSessionKey{}, sessionID)
		return next(response, request.WithContext(ctx))
	})
}

func (s *Server) withRateLimit(limiter *rateLimiter, next handler) handler {
	return func(response http.ResponseWriter, request *http.Request) error {
		key := s.clientIP(request)
		if identity := identityFrom(request); identity != nil {
			key = identity.OwnerKey
		}
		allowed, retry := limiter.allow(key)
		if !allowed {
			response.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds()+0.5)))
			code, message := "AUTH_RATE_LIMIT", "登录尝试过于频繁，请稍后再试"
			if limiter == s.sendLimit {
				code, message = "SEND_RATE_LIMIT", "发件频率过高，请稍后再试"
			}
			s.auditSecurity(request, "rate_limit", "blocked", code, 0, "")
			return &auth.Error{Message: message, Code: code, Status: http.StatusTooManyRequests}
		}
		return next(response, request)
	}
}

func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	record, exists := l.records[key]
	if !exists || !record.ResetAt.After(now) {
		record = rateRecord{ResetAt: now.Add(l.window)}
	}
	record.Count++
	l.records[key] = record
	if len(l.records) > 5000 {
		for name, value := range l.records {
			if !value.ResetAt.After(now) {
				delete(l.records, name)
			}
		}
	}
	return record.Count <= l.limit, time.Until(record.ResetAt)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		response.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		response.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		if request.TLS != nil || (s.cfg.TrustProxy && request.Header.Get("X-Forwarded-Proto") == "https") {
			response.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(response, request)
	})
}

func (s *Server) serveFrontend(response http.ResponseWriter, request *http.Request) {
	if strings.HasPrefix(request.URL.Path, "/api/") {
		s.writeAPIError(response, request, http.StatusNotFound, "API_NOT_FOUND", fmt.Sprintf("未找到接口 %s %s", request.Method, request.URL.Path), nil, false)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeJSON(response, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("未找到接口 %s %s", request.Method, request.URL.Path)})
		return
	}
	clean := filepath.Clean(strings.TrimPrefix(request.URL.Path, "/"))
	if clean != "." && !strings.HasPrefix(clean, "..") {
		path := filepath.Join(s.cfg.WebRoot, clean)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			if contentType := mime.TypeByExtension(filepath.Ext(path)); contentType != "" {
				response.Header().Set("Content-Type", contentType)
			}
			http.ServeFile(response, request, path)
			return
		}
	}
	index := filepath.Join(s.cfg.WebRoot, "index.html")
	if _, err := os.Stat(index); err != nil {
		writeJSON(response, http.StatusNotFound, map[string]any{"error": "前端构建文件不存在"})
		return
	}
	http.ServeFile(response, request, index)
}

func (s *Server) writeError(response http.ResponseWriter, request *http.Request, err error) {
	var apiError *APIError
	if errors.As(err, &apiError) {
		s.writeAPIError(response, request, apiError.Status, apiError.Code, apiError.Message, apiError.Details, apiError.Retryable)
		return
	}
	var validation *ValidationError
	if errors.As(err, &validation) {
		s.writeAPIError(response, request, http.StatusBadRequest, "VALIDATION_ERROR", "请求参数不正确", validation.Details, false)
		return
	}
	var authError *auth.Error
	if errors.As(err, &authError) {
		s.writeAPIError(response, request, authError.Status, authError.Code, authError.Message, nil, authError.Status == http.StatusTooManyRequests || authError.Status >= 500)
		return
	}
	var mailError *mailservice.Error
	if errors.As(err, &mailError) {
		if mailError.RetryAfter != nil {
			response.Header().Set("Retry-After", strconv.Itoa(*mailError.RetryAfter))
		}
		s.writeAPIError(response, request, mailError.Status, mailError.Code, mailError.Message, nil, mailError.Status == http.StatusTooManyRequests || mailError.Status >= 500)
		return
	}
	log.Printf("Mail API internal error requestId=%s: %v", requestIDFrom(request), err)
	s.writeAPIError(response, request, http.StatusInternalServerError, "INTERNAL_ERROR", "服务器内部错误", nil, true)
}

func apiFailure(status int, code, message string, details any) *APIError {
	return &APIError{Message: message, Code: code, Status: status, Details: details, Retryable: status == http.StatusTooManyRequests || status >= 500}
}

func (s *Server) writeAPIError(response http.ResponseWriter, request *http.Request, status int, code, message string, details any, retryable bool) {
	var retryAfter *int
	if value := strings.TrimSpace(response.Header().Get("Retry-After")); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil {
			retryAfter = &seconds
		}
	}
	writeJSON(response, status, desktopcontract.DesktopApiError{
		Code:       code,
		Message:    message,
		Details:    details,
		RequestId:  requestIDFrom(request),
		Retryable:  retryable,
		RetryAfter: retryAfter,
	})
}

func decodeJSON(response http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(response, request.Body, 5<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &ValidationError{Message: "请求参数不正确", Details: []map[string]any{{"message": err.Error()}}}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return &ValidationError{Message: "请求参数不正确", Details: []map[string]any{{"message": "请求正文只能包含一个 JSON 对象"}}}
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func identityFrom(request *http.Request) *model.Identity {
	identity, _ := request.Context().Value(identityKey{}).(*model.Identity)
	return identity
}

func requestIDFrom(request *http.Request) string {
	requestID, _ := request.Context().Value(requestIDKey{}).(string)
	return requestID
}

func desktopSessionIDFrom(request *http.Request) string {
	sessionID, _ := request.Context().Value(desktopSessionKey{}).(string)
	return sessionID
}

func validRequestID(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func newRequestID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16])
}

func parseID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		return 0, &mailservice.Error{Message: "账号 ID 无效", Code: "INVALID_ACCOUNT_ID", Status: http.StatusBadRequest}
	}
	return id, nil
}

func (s *Server) account(request *http.Request) (*model.AccountCredentials, error) {
	id, err := parseID(request.PathValue("id"))
	if err != nil {
		return nil, err
	}
	account, err := s.store.GetAccountCredentials(request.Context(), identityFrom(request).OwnerKey, id)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, &mailservice.Error{Message: "邮箱账号不存在", Code: "ACCOUNT_NOT_FOUND", Status: http.StatusNotFound}
	}
	return account, nil
}

func netSplitHostPort(value string) (string, string, error) {
	if strings.Count(value, ":") == 0 {
		return value, "", nil
	}
	return net.SplitHostPort(value)
}

func (s *Server) clientIP(request *http.Request) string {
	if s.cfg.TrustProxy {
		for _, candidate := range strings.Split(request.Header.Get("X-Forwarded-For"), ",") {
			candidate = strings.TrimSpace(candidate)
			if net.ParseIP(candidate) != nil {
				return candidate
			}
		}
		if candidate := strings.TrimSpace(request.Header.Get("X-Real-IP")); net.ParseIP(candidate) != nil {
			return candidate
		}
	}
	host, _, err := netSplitHostPort(request.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return request.RemoteAddr
}

func (s *Server) auditSecurity(request *http.Request, event, result, code string, userID int64, deviceID string) {
	log.Printf(
		"Mail security event=%s result=%s code=%s requestId=%s userId=%d deviceId=%s clientIp=%s",
		event,
		result,
		code,
		requestIDFrom(request),
		userID,
		deviceID,
		s.clientIP(request),
	)
}

func securityErrorCode(err error) string {
	var authError *auth.Error
	if errors.As(err, &authError) && authError.Code != "" {
		return authError.Code
	}
	return "INTERNAL_ERROR"
}
