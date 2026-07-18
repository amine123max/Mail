package mailservice

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"time"

	"github.com/amine123max/Mail/server/internal/model"
)

type smtpEndpoint struct {
	Address    string
	ServerName string
	TLSConfig  *tls.Config
}

type xoauth2SMTPAuth struct {
	username string
	token    string
}

func (auth *xoauth2SMTPAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("XOAUTH2 requires an encrypted SMTP connection")
	}
	payload := "user=" + auth.username + "\x01auth=Bearer " + auth.token + "\x01\x01"
	return "XOAUTH2", []byte(payload), nil
}

func (auth *xoauth2SMTPAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(string(challenge)); err == nil && len(decoded) > 0 {
		return nil, fmt.Errorf("SMTP OAuth2 authentication rejected: %s", decoded)
	}
	return nil, errors.New("SMTP OAuth2 authentication rejected")
}

func (s *Service) smtpSend(ctx context.Context, account *model.AccountCredentials, accessToken string, message SendRequest) (SendResult, error) {
	raw, accepted, messageID, err := buildMIMEMessage(account.Email, message)
	if err != nil {
		return SendResult{}, err
	}
	var lastError error
	for _, host := range s.cfg.SMTPHosts {
		endpoint := smtpEndpoint{
			Address:    net.JoinHostPort(host, "587"),
			ServerName: host,
			TLSConfig:  &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
		}
		if result, err := sendSMTPXOAUTH2(ctx, endpoint, account.Email, accessToken, accepted, raw, messageID); err == nil {
			result.Transport = "smtp"
			return result, nil
		} else {
			lastError = err
		}
	}
	return SendResult{}, serviceError("SMTP 发件失败："+errorMessage(lastError), "SMTP_SEND_FAILED", http.StatusBadGateway)
}

func sendSMTPXOAUTH2(ctx context.Context, endpoint smtpEndpoint, from, accessToken string, recipients []string, raw []byte, messageID string) (SendResult, error) {
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", endpoint.Address)
	if err != nil {
		return SendResult{}, err
	}
	defer connection.Close()
	deadline := time.Now().Add(45 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return SendResult{}, err
	}
	client, err := smtp.NewClient(connection, endpoint.ServerName)
	if err != nil {
		return SendResult{}, err
	}
	defer client.Close()
	if err := client.Hello("mail.local"); err != nil {
		return SendResult{}, err
	}
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return SendResult{}, errors.New("SMTP server does not support STARTTLS")
	}
	tlsConfig := endpoint.TLSConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{ServerName: endpoint.ServerName, MinVersion: tls.VersionTLS12}
	}
	if err := client.StartTLS(tlsConfig.Clone()); err != nil {
		return SendResult{}, err
	}
	if ok, mechanisms := client.Extension("AUTH"); !ok || !containsSMTPMechanism(mechanisms, "XOAUTH2") {
		return SendResult{}, errors.New("SMTP server does not advertise XOAUTH2")
	}
	if err := client.Auth(&xoauth2SMTPAuth{username: from, token: accessToken}); err != nil {
		return SendResult{}, err
	}
	if err := client.Mail(from); err != nil {
		return SendResult{}, err
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return SendResult{}, err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return SendResult{}, err
	}
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return SendResult{}, err
	}
	if err := writer.Close(); err != nil {
		return SendResult{}, err
	}
	if err := client.Quit(); err != nil {
		return SendResult{}, err
	}
	return SendResult{MessageID: messageID, Accepted: recipients}, nil
}

func containsSMTPMechanism(value, target string) bool {
	for _, mechanism := range fieldsUpper(value) {
		if mechanism == target {
			return true
		}
	}
	return false
}

func fieldsUpper(value string) []string {
	fields := make([]string, 0)
	current := make([]rune, 0, len(value))
	flush := func() {
		if len(current) > 0 {
			for index, character := range current {
				if character >= 'a' && character <= 'z' {
					current[index] = character - ('a' - 'A')
				}
			}
			fields = append(fields, string(current))
			current = current[:0]
		}
	}
	for _, character := range value {
		if character == ' ' || character == '\t' || character == ',' {
			flush()
			continue
		}
		current = append(current, character)
	}
	flush()
	return fields
}
