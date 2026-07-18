package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/amine123max/Mail/server/internal/config"
	"github.com/amine123max/Mail/server/internal/desktopcontract"
	"github.com/amine123max/Mail/server/internal/model"
	"github.com/amine123max/Mail/server/internal/store"
	"golang.org/x/crypto/scrypt"
)

const (
	VerificationLifetime   = 5 * time.Minute
	VerificationCooldown   = 60 * time.Second
	userLifetime           = 30 * 24 * time.Hour
	guestLifetime          = 400 * 24 * time.Hour
	desktopAccessLifetime  = 15 * time.Minute
	desktopRefreshIdle     = 14 * 24 * time.Hour
	desktopRefreshAbsolute = 30 * 24 * time.Hour
	dummyPasswordHash      = "scrypt:AAECAwQFBgcICQoLDA0ODw:sW1COcKH7Z2BDFE6mMHuCBgIPw6OwcK45RqKR1FrA7g"
)

type Error struct {
	Message string
	Code    string
	Status  int
}

func (e *Error) Error() string { return e.Message }

type Service struct {
	cfg   config.Config
	store *store.Store
	key   []byte
}

type VerificationMessage struct {
	Subject string
	Text    string
	HTML    string
}

type VerificationDispatchResult struct {
	ExpiresIn  int  `json:"expiresIn"`
	RetryAfter int  `json:"retryAfter"`
	Suppressed bool `json:"-"`
}

type desktopAccessClaims struct {
	SessionID string `json:"sid"`
	UserID    int64  `json:"uid"`
	ExpiresAt int64  `json:"exp"`
}

func New(cfg config.Config, storage *store.Store) *Service {
	return &Service{cfg: cfg, store: storage, key: []byte(cfg.SessionSecret)}
}

func (s *Service) Authenticate(ctx context.Context, email, password string) (*model.User, error) {
	user, err := s.store.FindUserByEmail(ctx, normalizeEmail(email))
	if err != nil {
		return nil, err
	}
	storedHash := dummyPasswordHash
	if user != nil {
		storedHash = user.PasswordHash
	}
	valid, err := verifyPassword(password, storedHash)
	if err != nil || user == nil || !valid {
		return nil, err
	}
	return user, nil
}

func (s *Service) Register(ctx context.Context, username, email, password, code string) (*model.User, error) {
	setup, err := s.store.IsSetupRequired(ctx)
	if err != nil {
		return nil, err
	}
	if setup {
		return nil, authError("请先完成管理员初始化", "SETUP_REQUIRED", http.StatusConflict)
	}
	existing, err := s.store.FindUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, authError("用户名已存在", "USERNAME_EXISTS", http.StatusConflict)
	}
	email = normalizeEmail(email)
	existing, err = s.store.FindUserByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, authError("该邮箱已被注册", "EMAIL_EXISTS", http.StatusConflict)
	}
	if err := s.verifyCode(ctx, email, code); err != nil {
		return nil, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	return s.store.CreateUser(ctx, username, hash, email, false)
}

func (s *Service) ResetPassword(ctx context.Context, email, password, code string) error {
	email = normalizeEmail(email)
	user, err := s.store.FindUserByEmail(ctx, email)
	if err != nil {
		return err
	}
	if user == nil {
		return authError("该邮箱尚未注册", "EMAIL_NOT_FOUND", http.StatusNotFound)
	}
	if err := s.verifyCode(ctx, email, code); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	return s.store.UpdateUserPassword(ctx, user.ID, hash)
}

func (s *Service) InitializeAdministrator(ctx context.Context, username, email, password, code string) (*model.User, error) {
	setup, err := s.store.IsSetupRequired(ctx)
	if err != nil {
		return nil, err
	}
	if !setup {
		return nil, authError("管理员初始化已完成", "SETUP_ALREADY_COMPLETED", http.StatusConflict)
	}
	email = normalizeEmail(email)
	if err := s.verifyCode(ctx, email, code); err != nil {
		return nil, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	user, err := s.store.CreateAdministrator(ctx, username, hash, email)
	if errors.Is(err, store.ErrSetupCompleted) {
		return nil, authError("管理员初始化已完成", "SETUP_ALREADY_COMPLETED", http.StatusConflict)
	}
	return user, err
}

func (s *Service) BootstrapAdministrator(ctx context.Context, username, email, password string) (*model.User, error) {
	username, email = strings.TrimSpace(username), normalizeEmail(email)
	if !regexp.MustCompile(`^[A-Za-z0-9_]{3,32}$`).MatchString(username) {
		return nil, authError("管理员用户名格式不正确", "INVALID_ADMIN_USERNAME", http.StatusBadRequest)
	}
	if !validEmail(email) {
		return nil, authError("管理员邮箱格式不正确", "INVALID_ADMIN_EMAIL", http.StatusBadRequest)
	}
	if len(password) < 12 || len(password) > 128 {
		return nil, authError("管理员密码必须为 12-128 位", "INVALID_ADMIN_PASSWORD", http.StatusBadRequest)
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	user, err := s.store.CreateAdministrator(ctx, username, hash, email)
	if errors.Is(err, store.ErrSetupCompleted) {
		return nil, authError("管理员初始化已完成", "SETUP_ALREADY_COMPLETED", http.StatusConflict)
	}
	return user, err
}

func (s *Service) RequestRegistrationCode(ctx context.Context, email, purpose, language string) (VerificationDispatchResult, error) {
	email = normalizeEmail(email)
	setup, err := s.store.IsSetupRequired(ctx)
	if err != nil {
		return VerificationDispatchResult{}, err
	}
	if purpose == "setup" && !setup {
		return VerificationDispatchResult{}, authError("管理员初始化已完成", "SETUP_ALREADY_COMPLETED", http.StatusConflict)
	}
	if purpose == "register" && setup {
		return VerificationDispatchResult{}, authError("请先完成管理员初始化", "SETUP_REQUIRED", http.StatusConflict)
	}
	if purpose == "reset" && setup {
		return VerificationDispatchResult{}, authError("请先完成管理员初始化", "SETUP_REQUIRED", http.StatusConflict)
	}
	user, err := s.store.FindUserByEmail(ctx, email)
	if err != nil {
		return VerificationDispatchResult{}, err
	}
	result := VerificationDispatchResult{
		ExpiresIn:  int(VerificationLifetime.Seconds()),
		RetryAfter: int(VerificationCooldown.Seconds()),
	}
	if (purpose == "reset" && user == nil) || (purpose != "reset" && user != nil) {
		result.Suppressed = true
		return result, nil
	}
	allowed, err := s.store.CanSendEmailVerification(ctx, email, VerificationCooldown)
	if err != nil {
		return VerificationDispatchResult{}, err
	}
	if !allowed {
		return VerificationDispatchResult{}, authError("验证码发送过于频繁，请在 60 秒后重试", "VERIFICATION_COOLDOWN", http.StatusTooManyRequests)
	}
	code, err := randomDigits(6)
	if err != nil {
		return VerificationDispatchResult{}, err
	}
	expiresAt := time.Now().UTC().Add(VerificationLifetime)
	if err := s.store.SaveEmailVerification(ctx, email, s.verificationHash(email, code), expiresAt); err != nil {
		return VerificationDispatchResult{}, err
	}
	message := BuildVerificationMessage(code, language)
	if err := s.sendVerificationEmail(ctx, email, message); err != nil {
		_ = s.store.DeleteEmailVerification(ctx, email)
		var configured *Error
		if errors.As(err, &configured) {
			return VerificationDispatchResult{}, configured
		}
		return VerificationDispatchResult{}, authError("验证码邮件发送失败，请检查邮件服务配置后重试", "VERIFICATION_DELIVERY_FAILED", http.StatusServiceUnavailable)
	}
	return result, nil
}

func (s *Service) CreateUserSession(ctx context.Context, response http.ResponseWriter, request *http.Request, user *model.User) error {
	id, err := randomToken(24)
	if err != nil {
		return err
	}
	expires := time.Now().UTC().Add(userLifetime)
	if err := s.store.CreateUserSession(ctx, id, user.ID, expires); err != nil {
		return err
	}
	return s.setSignedCookie(response, request, "mail_session", map[string]any{"sessionId": id, "userId": user.ID, "exp": expires.UnixMilli()}, userLifetime)
}

func (s *Service) CreateDesktopSession(ctx context.Context, user *model.User, deviceID, deviceName, clientVersion string) (desktopcontract.DesktopSessionResponse, error) {
	now := time.Now().UTC()
	sessionID, err := randomToken(24)
	if err != nil {
		return desktopcontract.DesktopSessionResponse{}, err
	}
	familyID, err := randomToken(24)
	if err != nil {
		return desktopcontract.DesktopSessionResponse{}, err
	}
	refreshToken, err := randomToken(32)
	if err != nil {
		return desktopcontract.DesktopSessionResponse{}, err
	}
	absoluteExpiresAt := now.Add(desktopRefreshAbsolute)
	idleExpiresAt := now.Add(desktopRefreshIdle)
	session := model.DesktopSession{
		ID:                sessionID,
		FamilyID:          familyID,
		DeviceID:          deviceID,
		UserID:            user.ID,
		DeviceName:        deviceName,
		ClientVersion:     clientVersion,
		CreatedAt:         now.Format(time.RFC3339Nano),
		LastUsedAt:        now.Format(time.RFC3339Nano),
		IdleExpiresAt:     idleExpiresAt.Format(time.RFC3339Nano),
		AbsoluteExpiresAt: absoluteExpiresAt.Format(time.RFC3339Nano),
	}
	if err := s.store.CreateDesktopSession(ctx, session, desktopTokenHash(refreshToken), absoluteExpiresAt); err != nil {
		return desktopcontract.DesktopSessionResponse{}, err
	}
	return s.desktopSessionResult(session, user, refreshToken, now)
}

func (s *Service) RefreshDesktopSession(ctx context.Context, refreshToken string) (desktopcontract.DesktopSessionResponse, error) {
	replacement, err := randomToken(32)
	if err != nil {
		return desktopcontract.DesktopSessionResponse{}, err
	}
	now := time.Now().UTC()
	session, status, err := s.store.RotateDesktopRefreshToken(
		ctx,
		desktopTokenHash(refreshToken),
		desktopTokenHash(replacement),
		now,
		now.Add(desktopRefreshIdle),
		now.Add(desktopRefreshAbsolute),
	)
	if err != nil {
		return desktopcontract.DesktopSessionResponse{}, err
	}
	switch status {
	case store.DesktopRefreshReplayed:
		return desktopcontract.DesktopSessionResponse{}, authError("设备登录状态已失效，请重新登录", "DESKTOP_REFRESH_REPLAYED", http.StatusUnauthorized)
	case store.DesktopRefreshExpired:
		return desktopcontract.DesktopSessionResponse{}, authError("设备登录状态已过期，请重新登录", "DESKTOP_REFRESH_EXPIRED", http.StatusUnauthorized)
	case store.DesktopRefreshInvalid:
		return desktopcontract.DesktopSessionResponse{}, authError("设备登录状态无效，请重新登录", "DESKTOP_REFRESH_INVALID", http.StatusUnauthorized)
	}
	user, err := s.store.FindUserByID(ctx, session.UserID)
	if err != nil {
		return desktopcontract.DesktopSessionResponse{}, err
	}
	if user == nil {
		return desktopcontract.DesktopSessionResponse{}, authError("设备登录状态无效，请重新登录", "DESKTOP_REFRESH_INVALID", http.StatusUnauthorized)
	}
	return s.desktopSessionResult(session, user, replacement, now)
}

func (s *Service) DesktopIdentity(ctx context.Context, request *http.Request) (*model.Identity, string, error) {
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	if !strings.HasPrefix(authorization, "Bearer ") {
		return nil, "", authError("请重新登录", "DESKTOP_ACCESS_REQUIRED", http.StatusUnauthorized)
	}
	claims, valid := s.readDesktopAccessToken(strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")))
	if !valid || claims.ExpiresAt <= time.Now().Unix() {
		return nil, "", authError("登录状态已过期，请刷新会话", "DESKTOP_ACCESS_EXPIRED", http.StatusUnauthorized)
	}
	active, err := s.store.DesktopSessionActive(ctx, claims.SessionID, claims.UserID, time.Now().UTC())
	if err != nil {
		return nil, "", err
	}
	if !active {
		return nil, "", authError("设备登录状态已撤销", "DESKTOP_SESSION_REVOKED", http.StatusUnauthorized)
	}
	user, err := s.store.FindUserByID(ctx, claims.UserID)
	if err != nil {
		return nil, "", err
	}
	if user == nil {
		return nil, "", authError("设备登录状态无效", "DESKTOP_ACCESS_INVALID", http.StatusUnauthorized)
	}
	identity := &model.Identity{Kind: "user", OwnerKey: "user:" + strconv.FormatInt(user.ID, 10), UserID: user.ID, Username: user.Username, IsAdmin: user.IsAdmin}
	return identity, claims.SessionID, nil
}

func (s *Service) RevokeDesktopSession(ctx context.Context, sessionID string, userID int64) error {
	return s.store.RevokeDesktopSession(ctx, sessionID, userID, "USER_LOGOUT")
}

func (s *Service) RevokeDesktopDevice(ctx context.Context, userID int64, deviceID string) error {
	return s.store.RevokeDesktopDevice(ctx, userID, deviceID, "USER_REVOKED_DEVICE")
}

func (s *Service) RevokeAllDesktopSessions(ctx context.Context, userID int64) error {
	return s.store.RevokeAllDesktopSessions(ctx, userID, "USER_REVOKED_ALL_DEVICES")
}

func (s *Service) ListDesktopDevices(ctx context.Context, userID int64, currentSessionID string) ([]model.DesktopDeviceSummary, error) {
	return s.store.ListDesktopDevices(ctx, userID, currentSessionID)
}

func (s *Service) desktopSessionResult(session model.DesktopSession, user *model.User, refreshToken string, now time.Time) (desktopcontract.DesktopSessionResponse, error) {
	accessToken, err := s.issueDesktopAccessToken(session.ID, user.ID, now.Add(desktopAccessLifetime))
	if err != nil {
		return desktopcontract.DesktopSessionResponse{}, err
	}
	return desktopcontract.DesktopSessionResponse{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		TokenType:        "Bearer",
		ExpiresIn:        int(desktopAccessLifetime.Seconds()),
		RefreshExpiresAt: session.AbsoluteExpiresAt,
		DeviceId:         session.DeviceID,
		User: desktopcontract.DesktopSessionUser{
			Id:            user.ID,
			Username:      user.Username,
			Administrator: user.IsAdmin,
		},
	}, nil
}

func (s *Service) issueDesktopAccessToken(sessionID string, userID int64, expiresAt time.Time) (string, error) {
	claims, err := json.Marshal(desktopAccessClaims{SessionID: sessionID, UserID: userID, ExpiresAt: expiresAt.Unix()})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	signature := s.sign("desktop-access:" + payload)
	return "d1." + payload + "." + signature, nil
}

func (s *Service) readDesktopAccessToken(token string) (desktopAccessClaims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "d1" {
		return desktopAccessClaims{}, false
	}
	if !hmac.Equal([]byte(parts[2]), []byte(s.sign("desktop-access:"+parts[1]))) {
		return desktopAccessClaims{}, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return desktopAccessClaims{}, false
	}
	var claims desktopAccessClaims
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return desktopAccessClaims{}, false
	}
	return claims, claims.SessionID != "" && claims.UserID > 0
}

func desktopTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func (s *Service) CreateGuest(ctx context.Context, response http.ResponseWriter, request *http.Request) (model.Identity, error) {
	id, err := randomToken(24)
	if err != nil {
		return model.Identity{}, err
	}
	expires := time.Now().UTC().Add(guestLifetime)
	if err := s.store.CreateGuestSession(ctx, id, expires); err != nil {
		return model.Identity{}, err
	}
	if err := s.setSignedCookie(response, request, "mail_guest", map[string]any{"guestId": id, "exp": expires.UnixMilli()}, guestLifetime); err != nil {
		return model.Identity{}, err
	}
	return model.Identity{Kind: "guest", GuestID: id, OwnerKey: "guest:" + id}, nil
}

func (s *Service) RenewGuest(ctx context.Context, response http.ResponseWriter, request *http.Request, guestID string) error {
	expires := time.Now().UTC().Add(guestLifetime)
	if err := s.store.CreateGuestSession(ctx, guestID, expires); err != nil {
		return err
	}
	return s.setSignedCookie(response, request, "mail_guest", map[string]any{"guestId": guestID, "exp": expires.UnixMilli()}, guestLifetime)
}

func (s *Service) Identity(ctx context.Context, request *http.Request) (*model.Identity, error) {
	var session struct {
		SessionID string `json:"sessionId"`
		UserID    int64  `json:"userId"`
		Expires   int64  `json:"exp"`
	}
	if s.readSignedCookie(request, "mail_session", &session) && session.SessionID != "" && session.UserID > 0 && session.Expires > time.Now().UnixMilli() {
		exists, err := s.store.UserSessionExists(ctx, session.SessionID, session.UserID)
		if err != nil {
			return nil, err
		}
		if exists {
			user, err := s.store.FindUserByID(ctx, session.UserID)
			if err != nil {
				return nil, err
			}
			if user != nil {
				return &model.Identity{Kind: "user", OwnerKey: "user:" + strconv.FormatInt(user.ID, 10), UserID: user.ID, Username: user.Username, IsAdmin: user.IsAdmin}, nil
			}
		}
	}
	guestID, err := s.GuestID(ctx, request)
	if err != nil || guestID == "" {
		return nil, err
	}
	return &model.Identity{Kind: "guest", OwnerKey: "guest:" + guestID, GuestID: guestID}, nil
}

func (s *Service) GuestID(ctx context.Context, request *http.Request) (string, error) {
	var guest struct {
		GuestID string `json:"guestId"`
		Expires int64  `json:"exp"`
	}
	if !s.readSignedCookie(request, "mail_guest", &guest) || guest.GuestID == "" || guest.Expires <= time.Now().UnixMilli() {
		return "", nil
	}
	exists, err := s.store.GuestSessionExists(ctx, guest.GuestID)
	if err != nil || !exists {
		return "", err
	}
	return guest.GuestID, nil
}

func (s *Service) UserSessionID(request *http.Request) string {
	var session struct {
		SessionID string `json:"sessionId"`
		Expires   int64  `json:"exp"`
	}
	if s.readSignedCookie(request, "mail_session", &session) && session.Expires > time.Now().UnixMilli() {
		return session.SessionID
	}
	return ""
}

func (s *Service) ClearCookies(response http.ResponseWriter) {
	for _, name := range []string{"mail_session", "mail_guest"} {
		http.SetCookie(response, &http.Cookie{Name: name, Value: "", Path: s.cfg.CookiePath, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	}
}

func (s *Service) ClearGuestCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{Name: "mail_guest", Value: "", Path: s.cfg.CookiePath, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func (s *Service) verificationHash(email, code string) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("registration:" + normalizeEmail(email) + ":" + code))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) verifyCode(ctx context.Context, email, code string) error {
	result, err := s.store.ConsumeEmailVerification(ctx, email, s.verificationHash(email, code))
	if err != nil {
		return err
	}
	switch result {
	case store.VerificationVerified:
		return nil
	case store.VerificationInvalid:
		return authError("验证码错误", "VERIFICATION_CODE_INVALID", http.StatusBadRequest)
	case store.VerificationAttemptsExceeded:
		return authError("验证码错误次数过多，请重新获取", "VERIFICATION_ATTEMPTS_EXCEEDED", http.StatusTooManyRequests)
	default:
		return authError("验证码已失效，请重新获取", "VERIFICATION_CODE_EXPIRED", http.StatusBadRequest)
	}
}

func (s *Service) setSignedCookie(response http.ResponseWriter, request *http.Request, name string, payload any, lifetime time.Duration) error {
	encodedJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	encoded := base64.RawURLEncoding.EncodeToString(encodedJSON)
	value := encoded + "." + s.sign(encoded)
	http.SetCookie(response, &http.Cookie{Name: name, Value: value, Path: s.cfg.CookiePath, HttpOnly: true, Secure: s.secureRequest(request), SameSite: http.SameSiteStrictMode, MaxAge: int(lifetime.Seconds())})
	return nil
}

func (s *Service) readSignedCookie(request *http.Request, name string, target any) bool {
	cookie, err := request.Cookie(name)
	if err != nil {
		return false
	}
	index := strings.LastIndex(cookie.Value, ".")
	if index < 1 {
		return false
	}
	payload, signature := cookie.Value[:index], cookie.Value[index+1:]
	if !hmac.Equal([]byte(signature), []byte(s.sign(payload))) {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	return err == nil && json.Unmarshal(decoded, target) == nil
}

func (s *Service) sign(value string) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) secureRequest(request *http.Request) bool {
	return request.TLS != nil || (s.cfg.TrustProxy && strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https"))
}

func (s *Service) sendVerificationEmail(ctx context.Context, recipient string, message VerificationMessage) error {
	if s.cfg.VerificationSMTPHost == "" || s.cfg.VerificationFrom == "" || (s.cfg.VerificationSMTPUser == "") != (s.cfg.VerificationSMTPPassword == "") {
		return authError("邮件验证码服务尚未配置，请联系管理员", "VERIFICATION_EMAIL_NOT_CONFIGURED", http.StatusServiceUnavailable)
	}
	fromAddress, err := mail.ParseAddress(s.cfg.VerificationFrom)
	if err != nil {
		return err
	}
	raw, err := s.verificationMIME(fromAddress, recipient, message)
	if err != nil {
		return err
	}
	address := net.JoinHostPort(s.cfg.VerificationSMTPHost, strconv.Itoa(s.cfg.VerificationSMTPPort))
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	var connection net.Conn
	if s.cfg.VerificationSMTPSecure {
		connection, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: s.cfg.VerificationSMTPHost, MinVersion: tls.VersionTLS12})
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return err
	}
	defer connection.Close()
	deadline := time.Now().Add(30 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return err
	}
	client, err := smtp.NewClient(connection, s.cfg.VerificationSMTPHost)
	if err != nil {
		return err
	}
	defer client.Close()
	if !s.cfg.VerificationSMTPSecure {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("验证码 SMTP 服务未提供 STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: s.cfg.VerificationSMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if s.cfg.VerificationSMTPUser != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.VerificationSMTPUser, s.cfg.VerificationSMTPPassword, s.cfg.VerificationSMTPHost)); err != nil {
			return err
		}
	}
	if err := client.Mail(fromAddress.Address); err != nil {
		return err
	}
	if err := client.Rcpt(recipient); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (s *Service) verificationMIME(from *mail.Address, recipient string, message VerificationMessage) ([]byte, error) {
	boundaryRelated, boundaryAlternative := randomBoundary(), randomBoundary()
	var output bytes.Buffer
	writeHeader(&output, "From", from.String())
	writeHeader(&output, "To", recipient)
	writeHeader(&output, "Subject", mime.QEncoding.Encode("UTF-8", message.Subject))
	writeHeader(&output, "MIME-Version", "1.0")
	writeHeader(&output, "Content-Type", `multipart/related; boundary="`+boundaryRelated+`"`)
	output.WriteString("\r\n--" + boundaryRelated + "\r\n")
	output.WriteString(`Content-Type: multipart/alternative; boundary="` + boundaryAlternative + `"` + "\r\n\r\n")
	output.WriteString("--" + boundaryAlternative + "\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + message.Text + "\r\n")
	output.WriteString("--" + boundaryAlternative + "\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + message.HTML + "\r\n")
	output.WriteString("--" + boundaryAlternative + "--\r\n")
	logoPath := filepath.Join(s.cfg.WebRoot, "paper-plane-logo.png")
	if _, err := os.Stat(logoPath); err != nil {
		logoPath = filepath.Join("public", "paper-plane-logo.png")
	}
	if logo, err := os.ReadFile(logoPath); err == nil {
		output.WriteString("--" + boundaryRelated + "\r\nContent-Type: image/png; name=mail-logo.png\r\nContent-Disposition: inline; filename=mail-logo.png\r\nContent-ID: <mail-brand-logo>\r\nContent-Transfer-Encoding: base64\r\n\r\n")
		encoded := base64.StdEncoding.EncodeToString(logo)
		for len(encoded) > 76 {
			output.WriteString(encoded[:76] + "\r\n")
			encoded = encoded[76:]
		}
		output.WriteString(encoded + "\r\n")
	}
	output.WriteString("--" + boundaryRelated + "--\r\n")
	return output.Bytes(), nil
}

func BuildVerificationMessage(code, language string) VerificationMessage {
	english := strings.EqualFold(language, "en")
	subject, title, expiry, open := "Mail 验证码", "输入此临时验证码以继续：", "此验证码将在 5 分钟后失效，请勿向任何人透露。", "打开 Mail"
	lang := "zh-CN"
	if english {
		subject, title, expiry, open, lang = "Mail verification code", "Enter this temporary verification code to continue:", "This code expires in 5 minutes. Do not share it with anyone.", "Open Mail", "en"
	}
	html := fmt.Sprintf(`<!doctype html><html lang="%s"><body style="margin:0;padding:0;background:#ffffff;color:#111827;font-family:Arial,'Microsoft YaHei',sans-serif"><table role="presentation" width="100%%" cellspacing="0" cellpadding="0" style="background:#ffffff"><tr><td align="center" style="padding:36px 18px"><table role="presentation" width="100%%" cellspacing="0" cellpadding="0" style="max-width:660px"><tr><td style="padding-bottom:58px"><img src="cid:mail-brand-logo" width="62" height="62" alt="Mail" style="display:block;border:0;object-fit:contain"></td></tr><tr><td style="font-size:22px;line-height:1.55;color:#111827;padding-bottom:28px">%s</td></tr><tr><td align="center" style="padding:0 0 30px"><div style="width:100%%;padding:30px 18px;border-radius:22px;background:#f3f4f6;font-size:38px;line-height:1;letter-spacing:8px;text-align:center;color:#4b5563;font-weight:500;font-variant-numeric:tabular-nums;box-sizing:border-box"><span style="padding-left:8px">%s</span></div></td></tr><tr><td style="font-size:15px;line-height:1.7;color:#6b7280;padding-bottom:58px">%s</td></tr><tr><td style="border-top:1px solid #e5e7eb;padding-top:30px"><img src="cid:mail-brand-logo" width="34" height="34" alt="" style="display:block;border:0;object-fit:contain"><div style="padding-top:18px;font-size:13px;line-height:1.8;color:#6b7280"><a href="https://www.aillive.xyz/mail" style="color:#4b5563">%s</a></div></td></tr></table></td></tr></table></body></html>`, lang, title, code, expiry, open)
	return VerificationMessage{Subject: subject, Text: strings.Join([]string{title, code, "", expiry, "", "Mail · https://www.aillive.xyz/mail"}, "\n"), HTML: html}
}

func ResolveLanguage(value string) string {
	type candidate struct {
		tag     string
		quality float64
		index   int
	}
	values := make([]candidate, 0)
	for index, part := range strings.Split(value, ",") {
		segments := strings.Split(strings.TrimSpace(part), ";")
		quality := 1.0
		for _, parameter := range segments[1:] {
			if strings.HasPrefix(strings.TrimSpace(parameter), "q=") {
				quality, _ = strconv.ParseFloat(strings.TrimSpace(parameter)[2:], 64)
			}
		}
		tag := strings.ToLower(segments[0])
		if strings.HasPrefix(tag, "zh") || strings.HasPrefix(tag, "en") {
			values = append(values, candidate{tag: tag, quality: quality, index: index})
		}
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].quality > values[j].quality })
	if len(values) > 0 && strings.HasPrefix(values[0].tag, "en") {
		return "en"
	}
	return "zh"
}

func hashPassword(password string) (string, error) {
	rawSalt := make([]byte, 16)
	if _, err := rand.Read(rawSalt); err != nil {
		return "", err
	}
	// The Node implementation passes the Base64URL salt text to scrypt, not the
	// decoded random bytes. Preserve that detail so existing password hashes and
	// hashes created by the Go server remain interchangeable.
	salt := base64.RawURLEncoding.EncodeToString(rawSalt)
	hash, err := scrypt.Key([]byte(password), []byte(salt), 16384, 8, 1, 32)
	if err != nil {
		return "", err
	}
	return "scrypt:" + salt + ":" + base64.RawURLEncoding.EncodeToString(hash), nil
}

func verifyPassword(password, stored string) (bool, error) {
	parts := strings.Split(stored, ":")
	if len(parts) != 3 || parts[0] != "scrypt" {
		return false, nil
	}
	if len(parts[1]) < 16 || len(parts[1]) > 128 {
		return false, nil
	}
	expected, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(expected) != 32 {
		return false, nil
	}
	actual, err := scrypt.Key([]byte(password), []byte(parts[1]), 16384, 8, 1, 32)
	return err == nil && hmac.Equal(actual, expected), err
}

func normalizeEmail(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@")
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func randomDigits(length int) (string, error) {
	value := make([]byte, length)
	buffer := make([]byte, 32)
	for index := 0; index < len(value); {
		if _, err := rand.Read(buffer); err != nil {
			return "", err
		}
		for _, candidate := range buffer {
			if candidate >= 250 {
				continue
			}
			value[index] = '0' + candidate%10
			index++
			if index == len(value) {
				break
			}
		}
	}
	return string(value), nil
}

func randomBoundary() string {
	token, _ := randomToken(18)
	return "mail-" + token
}

func writeHeader(writer io.Writer, name, value string) {
	_, _ = fmt.Fprintf(writer, "%s: %s\r\n", textproto.CanonicalMIMEHeaderKey(name), value)
}

func authError(message, code string, status int) *Error {
	return &Error{Message: message, Code: code, Status: status}
}
