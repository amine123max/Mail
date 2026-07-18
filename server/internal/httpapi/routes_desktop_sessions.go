package httpapi

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/amine123max/Mail/server/internal/auth"
	"github.com/amine123max/Mail/server/internal/desktopcontract"
)

var desktopClientVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)

func (s *Server) createDesktopSession(response http.ResponseWriter, request *http.Request) error {
	var body desktopcontract.DesktopSessionCreateRequest
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	body.Email = strings.TrimSpace(body.Email)
	body.DeviceId = strings.TrimSpace(body.DeviceId)
	body.DeviceName = strings.TrimSpace(body.DeviceName)
	clientVersion := strings.TrimSpace(request.Header.Get("X-Aillive-Client-Version"))
	if !validEmail(body.Email) || body.Password == "" || !validRequestID(body.DeviceId) || len(body.DeviceName) < 1 || len(body.DeviceName) > 120 || !desktopClientVersionPattern.MatchString(clientVersion) {
		s.auditSecurity(request, "desktop_login", "rejected", "VALIDATION_ERROR", 0, body.DeviceId)
		return validation("设备登录信息格式不正确")
	}
	user, err := s.auth.Authenticate(request.Context(), body.Email, body.Password)
	if err != nil {
		s.auditSecurity(request, "desktop_login", "failed", securityErrorCode(err), 0, body.DeviceId)
		return err
	}
	if user == nil {
		s.auditSecurity(request, "desktop_login", "rejected", "DESKTOP_LOGIN_FAILED", 0, body.DeviceId)
		return &auth.Error{Message: "邮箱或密码错误", Code: "DESKTOP_LOGIN_FAILED", Status: http.StatusUnauthorized}
	}
	result, err := s.auth.CreateDesktopSession(request.Context(), user, body.DeviceId, body.DeviceName, clientVersion)
	if err != nil {
		s.auditSecurity(request, "desktop_login", "failed", securityErrorCode(err), user.ID, body.DeviceId)
		return err
	}
	s.auditSecurity(request, "desktop_login", "succeeded", "DESKTOP_SESSION_CREATED", user.ID, body.DeviceId)
	writeJSON(response, http.StatusCreated, result)
	return nil
}

func (s *Server) migrateDesktopSession(response http.ResponseWriter, request *http.Request) error {
	var body desktopcontract.DesktopSessionMigrateRequest
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	body.DeviceId = strings.TrimSpace(body.DeviceId)
	body.DeviceName = strings.TrimSpace(body.DeviceName)
	clientVersion := strings.TrimSpace(request.Header.Get("X-Aillive-Client-Version"))
	if !validRequestID(body.DeviceId) || len(body.DeviceName) < 1 || len(body.DeviceName) > 120 || !desktopClientVersionPattern.MatchString(clientVersion) {
		s.auditSecurity(request, "desktop_cookie_migration", "rejected", "VALIDATION_ERROR", 0, body.DeviceId)
		return validation("设备迁移信息格式不正确")
	}
	identity, err := s.auth.Identity(request.Context(), request)
	if err != nil {
		s.auditSecurity(request, "desktop_cookie_migration", "failed", securityErrorCode(err), 0, body.DeviceId)
		return err
	}
	if identity == nil || identity.Kind != "user" || identity.UserID < 1 {
		s.auditSecurity(request, "desktop_cookie_migration", "rejected", "DESKTOP_MIGRATION_SESSION_REQUIRED", 0, body.DeviceId)
		return &auth.Error{Message: "旧登录状态不可用，请重新登录", Code: "DESKTOP_MIGRATION_SESSION_REQUIRED", Status: http.StatusUnauthorized}
	}
	user, err := s.store.FindUserByID(request.Context(), identity.UserID)
	if err != nil {
		s.auditSecurity(request, "desktop_cookie_migration", "failed", securityErrorCode(err), identity.UserID, body.DeviceId)
		return err
	}
	if user == nil {
		s.auditSecurity(request, "desktop_cookie_migration", "rejected", "DESKTOP_MIGRATION_SESSION_REQUIRED", identity.UserID, body.DeviceId)
		return &auth.Error{Message: "旧登录状态不可用，请重新登录", Code: "DESKTOP_MIGRATION_SESSION_REQUIRED", Status: http.StatusUnauthorized}
	}
	result, err := s.auth.CreateDesktopSession(request.Context(), user, body.DeviceId, body.DeviceName, clientVersion)
	if err != nil {
		s.auditSecurity(request, "desktop_cookie_migration", "failed", securityErrorCode(err), user.ID, body.DeviceId)
		return err
	}
	s.auditSecurity(request, "desktop_cookie_migration", "succeeded", "DESKTOP_SESSION_MIGRATED", user.ID, body.DeviceId)
	writeJSON(response, http.StatusCreated, result)
	return nil
}

func (s *Server) refreshDesktopSession(response http.ResponseWriter, request *http.Request) error {
	var body desktopcontract.DesktopSessionRefreshRequest
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	body.RefreshToken = strings.TrimSpace(body.RefreshToken)
	if len(body.RefreshToken) < 32 || len(body.RefreshToken) > 256 {
		s.auditSecurity(request, "desktop_refresh", "rejected", "DESKTOP_REFRESH_INVALID", 0, "")
		return &auth.Error{Message: "设备登录状态无效，请重新登录", Code: "DESKTOP_REFRESH_INVALID", Status: http.StatusUnauthorized}
	}
	result, err := s.auth.RefreshDesktopSession(request.Context(), body.RefreshToken)
	if err != nil {
		s.auditSecurity(request, "desktop_refresh", "rejected", securityErrorCode(err), 0, "")
		return err
	}
	s.auditSecurity(request, "desktop_refresh", "succeeded", "DESKTOP_SESSION_REFRESHED", result.User.Id, result.DeviceId)
	writeJSON(response, http.StatusOK, result)
	return nil
}

func (s *Server) deleteCurrentDesktopSession(response http.ResponseWriter, request *http.Request) error {
	identity := identityFrom(request)
	if err := s.auth.RevokeDesktopSession(request.Context(), desktopSessionIDFrom(request), identity.UserID); err != nil {
		s.auditSecurity(request, "desktop_device_revoke", "failed", securityErrorCode(err), identity.UserID, "")
		return err
	}
	s.auditSecurity(request, "desktop_device_revoke", "succeeded", "DESKTOP_CURRENT_SESSION_REVOKED", identity.UserID, "")
	response.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listDesktopDevices(response http.ResponseWriter, request *http.Request) error {
	identity := identityFrom(request)
	devices, err := s.auth.ListDesktopDevices(request.Context(), identity.UserID, desktopSessionIDFrom(request))
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, desktopcontract.DesktopDeviceListResponse{Devices: devices})
	return nil
}

func (s *Server) deleteDesktopDevice(response http.ResponseWriter, request *http.Request) error {
	deviceID := strings.TrimSpace(request.PathValue("deviceId"))
	if !validRequestID(deviceID) {
		return validation("设备 ID 无效")
	}
	if err := s.auth.RevokeDesktopDevice(request.Context(), identityFrom(request).UserID, deviceID); err != nil {
		s.auditSecurity(request, "desktop_device_revoke", "failed", securityErrorCode(err), identityFrom(request).UserID, deviceID)
		return err
	}
	s.auditSecurity(request, "desktop_device_revoke", "succeeded", "DESKTOP_DEVICE_REVOKED", identityFrom(request).UserID, deviceID)
	response.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deleteAllDesktopDevices(response http.ResponseWriter, request *http.Request) error {
	if err := s.auth.RevokeAllDesktopSessions(request.Context(), identityFrom(request).UserID); err != nil {
		s.auditSecurity(request, "desktop_device_revoke", "failed", securityErrorCode(err), identityFrom(request).UserID, "all")
		return err
	}
	s.auditSecurity(request, "desktop_device_revoke", "succeeded", "DESKTOP_ALL_SESSIONS_REVOKED", identityFrom(request).UserID, "all")
	response.WriteHeader(http.StatusNoContent)
	return nil
}
