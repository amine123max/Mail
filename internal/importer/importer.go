package importer

import (
	"net/mail"
	"strings"

	"github.com/amine123max/Mail/internal/model"
)

type ParseError struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

type Result struct {
	Accounts []model.ImportedAccount
	Errors   []ParseError
}

func Parse(raw string) Result {
	result := Result{Accounts: make([]model.ImportedAccount, 0), Errors: make([]ParseError, 0)}
	raw = strings.TrimPrefix(raw, "\ufeff")
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for index, original := range lines {
		lineNumber := index + 1
		line := strings.TrimSpace(original)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		separator := "----"
		parts := strings.Split(line, separator)
		if strings.Contains(line, "\t") {
			separator = "\t"
			parts = strings.FieldsFunc(line, func(character rune) bool { return character == '\t' })
		}
		if len(parts) < 4 {
			result.Errors = append(result.Errors, ParseError{Line: lineNumber, Message: "必须包含邮箱、密码、Client ID、Refresh Token 四个字段"})
			continue
		}
		email := strings.TrimSpace(parts[0])
		password := strings.TrimSpace(parts[1])
		clientID := strings.TrimSpace(parts[2])
		refreshToken := strings.TrimSpace(strings.Join(parts[3:], separator))
		parsed, err := mail.ParseAddress(email)
		if err != nil || parsed.Address != email || !strings.Contains(email, "@") {
			result.Errors = append(result.Errors, ParseError{Line: lineNumber, Message: "邮箱地址格式无效"})
			continue
		}
		if password == "" || clientID == "" || refreshToken == "" {
			result.Errors = append(result.Errors, ParseError{Line: lineNumber, Message: "四个字段都不能为空"})
			continue
		}
		result.Accounts = append(result.Accounts, model.ImportedAccount{Email: email, Password: password, ClientID: clientID, RefreshToken: refreshToken})
	}
	return result
}

func Serialize(accounts []model.AccountCredentials) string {
	lines := make([]string, len(accounts))
	for index, account := range accounts {
		lines[index] = strings.Join([]string{account.Email, account.Password, account.ClientID, account.RefreshToken}, "----")
	}
	return strings.Join(lines, "\n")
}
