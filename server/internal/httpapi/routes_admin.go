package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/amine123max/Mail/server/internal/store"
)

func (s *Server) listAnnouncements(response http.ResponseWriter, request *http.Request) error {
	identity := identityFrom(request)
	announcements, unread, err := s.store.ListAnnouncements(request.Context(), identity.UserID)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"announcements": announcements, "unreadCount": unread})
	return nil
}

func (s *Server) markAnnouncementsRead(response http.ResponseWriter, request *http.Request) error {
	marked, err := s.store.MarkAnnouncementsRead(request.Context(), identityFrom(request).UserID)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"marked": marked, "unreadCount": 0})
	return nil
}

func (s *Server) createAnnouncement(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	body.Title, body.Content = strings.TrimSpace(body.Title), strings.TrimSpace(body.Content)
	if body.Title == "" || len([]rune(body.Title)) > 120 || body.Content == "" || len([]rune(body.Content)) > 4000 {
		return validation("公告标题或内容格式不正确")
	}
	announcement, err := s.store.CreateAnnouncement(request.Context(), identityFrom(request).UserID, body.Title, body.Content)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusCreated, map[string]any{"announcement": announcement})
	return nil
}

func (s *Server) adminStats(response http.ResponseWriter, request *http.Request) error {
	stats, err := s.store.GetAdminStats(request.Context())
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, stats)
	return nil
}

func (s *Server) adminActivity(response http.ResponseWriter, request *http.Request) error {
	days := 30
	if raw := request.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 7 || parsed > 30 {
			return validation("days 必须为 7-30 的整数")
		}
		days = parsed
	}
	activity, err := s.store.GetAdminActivity(request.Context(), days)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"activity": activity})
	return nil
}

func (s *Server) adminUsers(response http.ResponseWriter, request *http.Request) error {
	users, err := s.store.ListUsersForAdmin(request.Context())
	if err != nil {
		return err
	}
	response.Header().Set("Cache-Control", "no-store, private")
	writeJSON(response, http.StatusOK, map[string]any{"users": users})
	return nil
}

func (s *Server) adminUserStatus(response http.ResponseWriter, request *http.Request) error {
	userID, err := parseID(request.PathValue("id"))
	if err != nil {
		return validation("用户 ID 格式不正确")
	}
	var body struct {
		Disabled *bool `json:"disabled"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if body.Disabled == nil {
		return validation("disabled 必须为布尔值")
	}
	identity := identityFrom(request)
	if *body.Disabled && identity.UserID == userID {
		return apiFailure(http.StatusConflict, "CANNOT_DISABLE_SELF", "不能停用当前管理员账号", nil)
	}
	if err := s.auth.SetUserDisabled(request.Context(), userID, *body.Disabled); err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return apiFailure(http.StatusNotFound, "USER_NOT_FOUND", "用户不存在", nil)
		case errors.Is(err, store.ErrAdministratorCannotBeDisabled):
			return apiFailure(http.StatusConflict, "ADMIN_DISABLE_FORBIDDEN", "管理员账号不能被停用", nil)
		default:
			return err
		}
	}
	result := "enabled"
	if *body.Disabled {
		result = "disabled"
	}
	s.auditSecurity(request, "admin_user_status", result, "USER_STATUS_UPDATED", identity.UserID, strconv.FormatInt(userID, 10))
	writeJSON(response, http.StatusOK, map[string]any{"id": userID, "disabled": *body.Disabled})
	return nil
}
