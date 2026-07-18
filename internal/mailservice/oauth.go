package mailservice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/amine123max/Mail/internal/model"
)

const (
	imapScope      = "https://outlook.office.com/IMAP.AccessAsUser.All offline_access"
	smtpScope      = "https://outlook.office.com/SMTP.Send offline_access"
	graphReadScope = "https://graph.microsoft.com/Mail.ReadWrite offline_access"
	graphSendScope = "https://graph.microsoft.com/Mail.Send offline_access"
)

var deviceScopes = strings.Join([]string{
	"https://outlook.office.com/IMAP.AccessAsUser.All",
	"https://outlook.office.com/Mail.ReadWrite",
	"https://outlook.office.com/SMTP.Send",
	"https://graph.microsoft.com/Mail.ReadWrite",
	"https://graph.microsoft.com/Mail.Send",
	"offline_access",
}, " ")

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (s *Service) RefreshAccessToken(ctx context.Context, account *model.AccountCredentials, requestedScope string) (TokenResult, error) {
	values := url.Values{"client_id": {account.ClientID}, "grant_type": {"refresh_token"}, "refresh_token": {account.RefreshToken}}
	if requestedScope != "" {
		values.Set("scope", requestedScope)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://login.microsoftonline.com/consumers/oauth2/v2.0/token", strings.NewReader(values.Encode()))
	if err != nil {
		return TokenResult{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return TokenResult{}, serviceError("微软令牌刷新失败："+err.Error(), "TOKEN_REFRESH_FAILED", http.StatusUnauthorized)
	}
	defer response.Body.Close()
	var data tokenResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&data); err != nil {
		return TokenResult{}, serviceError("微软令牌刷新失败：响应格式无效", "TOKEN_REFRESH_FAILED", http.StatusUnauthorized)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || data.AccessToken == "" {
		detail := data.ErrorDescription
		if detail == "" {
			detail = data.Error
		}
		if detail == "" {
			detail = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		return TokenResult{}, serviceError("微软令牌刷新失败："+detail, "TOKEN_REFRESH_FAILED", http.StatusUnauthorized)
	}
	if data.RefreshToken != "" && data.RefreshToken != account.RefreshToken {
		if err := s.store.UpdateRefreshToken(ctx, account.OwnerKey, account.ID, data.RefreshToken); err != nil {
			return TokenResult{}, err
		}
		account.RefreshToken = data.RefreshToken
	}
	return TokenResult{AccessToken: data.AccessToken, Scope: data.Scope}, nil
}

func (s *Service) RequestDeviceCode(ctx context.Context, clientID string) (map[string]any, error) {
	values := url.Values{"client_id": {clientID}, "scope": {deviceScopes}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://login.microsoftonline.com/consumers/oauth2/v2.0/devicecode", strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, serviceError("获取微软设备码失败："+err.Error(), "DEVICE_CODE_FAILED", http.StatusBadRequest)
	}
	defer response.Body.Close()
	data := make(map[string]any)
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&data); err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || data["device_code"] == nil {
		detail := fmt.Sprint(data["error_description"])
		if detail == "<nil>" || detail == "" {
			detail = fmt.Sprint(data["error"])
		}
		return nil, serviceError("获取微软设备码失败："+detail, "DEVICE_CODE_FAILED", http.StatusBadRequest)
	}
	return data, nil
}

func (s *Service) PollDeviceCode(ctx context.Context, clientID, deviceCode string) (map[string]any, int, error) {
	values := url.Values{"client_id": {clientID}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {deviceCode}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://login.microsoftonline.com/consumers/oauth2/v2.0/token", strings.NewReader(values.Encode()))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, 0, serviceError("微软授权失败："+err.Error(), "DEVICE_AUTH_FAILED", http.StatusBadRequest)
	}
	defer response.Body.Close()
	var data tokenResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&data); err != nil {
		return nil, 0, err
	}
	if data.Error == "authorization_pending" || data.Error == "slow_down" {
		return map[string]any{"pending": true, "slowDown": data.Error == "slow_down"}, http.StatusAccepted, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || data.RefreshToken == "" {
		detail := data.ErrorDescription
		if detail == "" {
			detail = data.Error
		}
		return nil, 0, serviceError("微软授权失败："+detail, "DEVICE_AUTH_FAILED", http.StatusBadRequest)
	}
	return map[string]any{"pending": false, "refreshToken": data.RefreshToken, "scope": data.Scope}, http.StatusOK, nil
}
