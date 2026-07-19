package httpapi

import (
	"net/http"
	"strings"

	"github.com/amine123max/Mail/server/internal/desktopcontract"
)

const (
	desktopAPIVersion            = "1.0"
	defaultDesktopMinimumVersion = "1.0.0"
	defaultDesktopLatestVersion  = "1.0.0"
	maxDesktopJSONRequestBytes   = 5 << 20
	maxDesktopJSONResponseBytes  = 10 << 20
	maxDesktopAttachmentUpload   = 3 << 20
	maxDesktopAttachmentDownload = 100 << 20
)

func (s *Server) desktopCapabilities(response http.ResponseWriter, _ *http.Request) error {
	minimumVersion := strings.TrimSpace(s.cfg.DesktopMinimumVersion)
	if minimumVersion == "" {
		minimumVersion = defaultDesktopMinimumVersion
	}
	latestVersion := strings.TrimSpace(s.cfg.DesktopLatestVersion)
	if latestVersion == "" {
		latestVersion = defaultDesktopLatestVersion
	}
	var maintenanceMessage *string
	var maintenanceRetry *int
	if s.cfg.DesktopMaintenance {
		message := strings.TrimSpace(s.cfg.DesktopMaintenanceNotice)
		if message == "" {
			message = "服务正在维护，请稍后重试"
		}
		retryAfter := s.cfg.DesktopMaintenanceRetry
		maintenanceMessage = &message
		maintenanceRetry = &retryAfter
	}
	writeJSON(response, http.StatusOK, desktopcontract.DesktopCapabilities{
		ApiVersion:           desktopAPIVersion,
		MinimumClientVersion: minimumVersion,
		LatestClientVersion:  latestVersion,
		Features: map[string]bool{
			"deviceSessions":     true,
			"incrementalSync":    true,
			"attachmentDownload": true,
			"maintenanceMode":    true,
		},
		Limits: desktopcontract.DesktopCapabilitiesLimits{
			MaxJsonRequestBytes:        maxDesktopJSONRequestBytes,
			MaxJsonResponseBytes:       maxDesktopJSONResponseBytes,
			MaxAttachmentUploadBytes:   maxDesktopAttachmentUpload,
			MaxAttachmentDownloadBytes: maxDesktopAttachmentDownload,
		},
		Sync: desktopcontract.DesktopSyncCapabilities{
			ProtocolVersion: 1,
			Providers:       []string{"graph", "imap"},
			Incremental:     true,
			CursorReset:     true,
		},
		Maintenance: desktopcontract.DesktopMaintenanceState{
			Active:     s.cfg.DesktopMaintenance,
			Message:    maintenanceMessage,
			RetryAfter: maintenanceRetry,
		},
	})
	return nil
}
