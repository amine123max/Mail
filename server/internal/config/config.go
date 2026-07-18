package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	Host                     string
	Port                     int
	Production               bool
	TrustProxy               bool
	DataDir                  string
	SessionSecret            string
	EncryptionKey            string
	CookiePath               string
	VerificationSMTPHost     string
	VerificationSMTPPort     int
	VerificationSMTPSecure   bool
	VerificationSMTPUser     string
	VerificationSMTPPassword string
	VerificationFrom         string
	IMAPHosts                []string
	SMTPHosts                []string
	WebRoot                  string
}

var cookiePathPattern = regexp.MustCompile(`^/[A-Za-z0-9/_-]*$`)

func Load() (Config, error) {
	production := strings.EqualFold(strings.TrimSpace(os.Getenv("NODE_ENV")), "production") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("MAIL_ENV")), "production")
	portDefault := 3001
	if production {
		portDefault = 3000
	}
	port, err := integerEnv("PORT", portDefault)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, errors.New("PORT 必须是 1-65535 之间的整数")
	}
	smtpPort, err := integerEnv("MAIL_VERIFICATION_SMTP_PORT", 587)
	if err != nil || smtpPort < 1 || smtpPort > 65535 {
		return Config{}, errors.New("MAIL_VERIFICATION_SMTP_PORT 配置无效")
	}

	cookiePath := strings.TrimSpace(os.Getenv("MAIL_COOKIE_PATH"))
	if cookiePath == "" {
		cookiePath = "/"
	}
	if !cookiePathPattern.MatchString(cookiePath) {
		cookiePath = "/"
	}
	if len(cookiePath) > 1 {
		cookiePath = strings.TrimSuffix(cookiePath, "/")
	}

	sessionSecret := os.Getenv("MAIL_SESSION_SECRET")
	if sessionSecret == "" && !production {
		buffer := make([]byte, 32)
		if _, err := rand.Read(buffer); err != nil {
			return Config{}, err
		}
		sessionSecret = base64.RawURLEncoding.EncodeToString(buffer)
	}

	cfg := Config{
		Host:                     stringEnv("HOST", "127.0.0.1"),
		Port:                     port,
		Production:               production,
		TrustProxy:               os.Getenv("MAIL_TRUST_PROXY") == "1",
		DataDir:                  stringEnv("MAIL_DATA_DIR", "./data"),
		SessionSecret:            sessionSecret,
		EncryptionKey:            strings.TrimSpace(os.Getenv("MAIL_ENCRYPTION_KEY")),
		CookiePath:               cookiePath,
		VerificationSMTPHost:     strings.TrimSpace(os.Getenv("MAIL_VERIFICATION_SMTP_HOST")),
		VerificationSMTPPort:     smtpPort,
		VerificationSMTPSecure:   os.Getenv("MAIL_VERIFICATION_SMTP_SECURE") == "1",
		VerificationSMTPUser:     strings.TrimSpace(os.Getenv("MAIL_VERIFICATION_SMTP_USER")),
		VerificationSMTPPassword: os.Getenv("MAIL_VERIFICATION_SMTP_PASSWORD"),
		VerificationFrom:         strings.TrimSpace(os.Getenv("MAIL_VERIFICATION_FROM")),
		IMAPHosts:                uniqueHosts(stringEnv("OUTLOOK_IMAP_HOST", "outlook.office365.com"), "outlook.live.com"),
		SMTPHosts:                uniqueHosts(stringEnv("OUTLOOK_SMTP_HOST", "smtp-mail.outlook.com"), "smtp.office365.com"),
		WebRoot:                  filepath.Clean(stringEnv("MAIL_WEB_ROOT", "./dist")),
	}
	if cfg.VerificationFrom == "" {
		cfg.VerificationFrom = cfg.VerificationSMTPUser
	}
	if production {
		if len(cfg.SessionSecret) < 32 {
			return Config{}, errors.New("生产环境必须设置至少 32 位的 MAIL_SESSION_SECRET")
		}
		if cfg.EncryptionKey == "" {
			return Config{}, errors.New("生产环境必须设置 MAIL_ENCRYPTION_KEY")
		}
		if cfg.VerificationSMTPHost == "" || cfg.VerificationFrom == "" {
			return Config{}, errors.New("生产环境必须配置注册验证码邮件服务")
		}
	}
	return cfg, nil
}

func integerEnv(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	return strconv.Atoi(value)
}

func stringEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func uniqueHosts(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
