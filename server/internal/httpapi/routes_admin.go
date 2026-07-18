package httpapi

import (
	"net/http"
	"strconv"
	"strings"
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
