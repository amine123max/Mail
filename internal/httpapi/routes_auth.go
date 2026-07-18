package httpapi

import (
	"net/http"
	"net/mail"
	"regexp"
	"strings"

	"github.com/amine123max/Mail/internal/auth"
)

func (s *Server) health(response http.ResponseWriter, request *http.Request) error {
	if err := s.store.DB().PingContext(request.Context()); err != nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]any{"status": "error", "storage": "sqlite", "runtime": "go"})
		return nil
	}
	writeJSON(response, http.StatusOK, map[string]any{"status": "ok", "storage": "sqlite", "runtime": "go"})
	return nil
}

func (s *Server) authStatus(response http.ResponseWriter, request *http.Request) error {
	identity, err := s.auth.Identity(request.Context(), request)
	if err != nil {
		return err
	}
	if identity != nil && identity.Kind == "guest" {
		if err := s.auth.RenewGuest(request.Context(), response, request, identity.GuestID); err != nil {
			return err
		}
	}
	setup, err := s.store.IsSetupRequired(request.Context())
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"authenticated": identity != nil && identity.Kind == "user",
		"guest":         identity != nil && identity.Kind == "guest",
		"username": func() any {
			if identity != nil && identity.Username != "" {
				return identity.Username
			}
			return nil
		}(),
		"administrator": identity != nil && identity.Kind == "user" && identity.IsAdmin,
		"setupRequired": setup,
	})
	return nil
}

func (s *Server) requestVerification(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		Email    string `json:"email"`
		Purpose  string `json:"purpose"`
		Language string `json:"language"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	body.Email = strings.TrimSpace(body.Email)
	if !validEmail(body.Email) || len(body.Email) > 254 || (body.Purpose != "setup" && body.Purpose != "register") || (body.Language != "" && body.Language != "zh" && body.Language != "en") {
		return validation("邮箱、验证码用途或语言不正确")
	}
	if body.Language == "" {
		body.Language = auth.ResolveLanguage(request.Header.Get("Accept-Language"))
	}
	result, err := s.auth.RequestRegistrationCode(request.Context(), body.Email, body.Purpose, body.Language)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, result)
	return nil
}

func (s *Server) setupAdministrator(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		Username         string `json:"username"`
		Email            string `json:"email"`
		Password         string `json:"password"`
		VerificationCode string `json:"verificationCode"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	body.Username, body.Email = strings.TrimSpace(body.Username), strings.TrimSpace(body.Email)
	if !validUsername(body.Username) || !validEmail(body.Email) || len(body.Password) < 12 || len(body.Password) > 128 || !regexp.MustCompile(`^\d{6}$`).MatchString(body.VerificationCode) {
		return validation("管理员注册信息格式不正确")
	}
	user, err := s.auth.InitializeAdministrator(request.Context(), body.Username, body.Email, body.Password, body.VerificationCode)
	if err != nil {
		return err
	}
	if err := s.auth.CreateUserSession(request.Context(), response, request, user); err != nil {
		return err
	}
	writeJSON(response, http.StatusCreated, map[string]any{"authenticated": true, "username": user.Username, "administrator": true})
	return nil
}

func (s *Server) login(response http.ResponseWriter, request *http.Request) error {
	setup, err := s.store.IsSetupRequired(request.Context())
	if err != nil {
		return err
	}
	if setup {
		return &auth.Error{Message: "请先完成管理员初始化", Code: "SETUP_REQUIRED", Status: http.StatusConflict}
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if !validEmail(strings.TrimSpace(body.Email)) || body.Password == "" {
		return validation("邮箱地址或密码格式不正确")
	}
	user, err := s.auth.Authenticate(request.Context(), body.Email, body.Password)
	if err != nil {
		return err
	}
	if user == nil {
		writeJSON(response, http.StatusUnauthorized, map[string]any{"error": "邮箱或密码错误", "code": "LOGIN_FAILED"})
		return nil
	}
	guestID, err := s.auth.GuestID(request.Context(), request)
	if err != nil {
		return err
	}
	transferred := 0
	if guestID != "" {
		transferred, err = s.store.TransferGuestAccounts(request.Context(), guestID, user.ID)
		if err != nil {
			return err
		}
	}
	if err := s.auth.CreateUserSession(request.Context(), response, request, user); err != nil {
		return err
	}
	s.auth.ClearGuestCookie(response)
	writeJSON(response, http.StatusOK, map[string]any{"authenticated": true, "username": user.Username, "administrator": user.IsAdmin, "transferred": transferred})
	return nil
}

func (s *Server) register(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		Username         string `json:"username"`
		Email            string `json:"email"`
		Password         string `json:"password"`
		VerificationCode string `json:"verificationCode"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	body.Username, body.Email = strings.TrimSpace(body.Username), strings.TrimSpace(body.Email)
	if !validUsername(body.Username) || !validEmail(body.Email) || len(body.Password) < 8 || len(body.Password) > 128 || !regexp.MustCompile(`^\d{6}$`).MatchString(body.VerificationCode) {
		return validation("注册信息格式不正确")
	}
	user, err := s.auth.Register(request.Context(), body.Username, body.Email, body.Password, body.VerificationCode)
	if err != nil {
		return err
	}
	guestID, err := s.auth.GuestID(request.Context(), request)
	if err != nil {
		return err
	}
	transferred := 0
	if guestID != "" {
		transferred, err = s.store.TransferGuestAccounts(request.Context(), guestID, user.ID)
		if err != nil {
			return err
		}
	}
	if err := s.auth.CreateUserSession(request.Context(), response, request, user); err != nil {
		return err
	}
	s.auth.ClearGuestCookie(response)
	writeJSON(response, http.StatusCreated, map[string]any{"authenticated": true, "username": user.Username, "administrator": false, "transferred": transferred})
	return nil
}

func (s *Server) guest(response http.ResponseWriter, request *http.Request) error {
	setup, err := s.store.IsSetupRequired(request.Context())
	if err != nil {
		return err
	}
	if setup {
		return &auth.Error{Message: "请先完成管理员初始化", Code: "SETUP_REQUIRED", Status: http.StatusConflict}
	}
	existing, err := s.auth.Identity(request.Context(), request)
	if err != nil {
		return err
	}
	if existing != nil && existing.Kind == "guest" {
		if err := s.auth.RenewGuest(request.Context(), response, request, existing.GuestID); err != nil {
			return err
		}
		writeJSON(response, http.StatusOK, map[string]any{"guest": true})
		return nil
	}
	if _, err := s.auth.CreateGuest(request.Context(), response, request); err != nil {
		return err
	}
	writeJSON(response, http.StatusCreated, map[string]any{"guest": true, "expiresIn": 86400})
	return nil
}

func (s *Server) logout(response http.ResponseWriter, request *http.Request) error {
	guestID, err := s.auth.GuestID(request.Context(), request)
	if err != nil {
		return err
	}
	if guestID != "" {
		if err := s.store.DeleteGuestSession(request.Context(), guestID); err != nil {
			return err
		}
	}
	if sessionID := s.auth.UserSessionID(request); sessionID != "" {
		if err := s.store.DeleteUserSession(request.Context(), sessionID); err != nil {
			return err
		}
	}
	s.auth.ClearCookies(response)
	response.WriteHeader(http.StatusNoContent)
	return nil
}

func validUsername(value string) bool {
	return regexp.MustCompile(`^[A-Za-z0-9_]{3,32}$`).MatchString(value)
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@")
}

func validation(message string) error {
	return &ValidationError{Message: message, Details: []map[string]any{{"message": message}}}
}
