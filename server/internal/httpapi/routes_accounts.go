package httpapi

import (
	"net/http"
	"strings"

	"github.com/amine123max/Mail/server/internal/importer"
	"github.com/amine123max/Mail/server/internal/store"
)

const guestAccountLimit = 3

func (s *Server) listAccounts(response http.ResponseWriter, request *http.Request) error {
	accounts, err := s.store.ListAccounts(request.Context(), identityFrom(request).OwnerKey)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"accounts": accounts})
	return nil
}

func (s *Server) importAccounts(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		Raw  string `json:"raw"`
		Mode string `json:"mode"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if body.Mode == "" {
		body.Mode = "skip"
	}
	if strings.TrimSpace(body.Raw) == "" || (body.Mode != "skip" && body.Mode != "overwrite") {
		return validation("请粘贴账号内容并选择正确的导入模式")
	}
	parsed := importer.Parse(body.Raw)
	if len(parsed.Errors) > 0 {
		writeJSON(response, http.StatusBadRequest, map[string]any{"error": "部分导入行格式不正确", "code": "IMPORT_FORMAT_ERROR", "details": parsed.Errors})
		return nil
	}
	if len(parsed.Accounts) == 0 {
		writeJSON(response, http.StatusBadRequest, map[string]any{"error": "没有可导入的账号"})
		return nil
	}
	identity := identityFrom(request)
	if identity.Kind == "guest" {
		existing, err := s.store.ListAccounts(request.Context(), identity.OwnerKey)
		if err != nil {
			return err
		}
		emails := make(map[string]struct{}, len(existing))
		for _, account := range existing {
			emails[strings.ToLower(account.Email)] = struct{}{}
		}
		newEmails := make(map[string]struct{})
		for _, account := range parsed.Accounts {
			email := strings.ToLower(account.Email)
			if _, exists := emails[email]; !exists {
				newEmails[email] = struct{}{}
			}
		}
		if len(existing)+len(newEmails) > guestAccountLimit {
			writeJSON(response, http.StatusForbidden, map[string]any{"error": "游客模式最多可保存 3 个邮箱账号", "code": "ACCOUNT_LIMIT_REACHED"})
			return nil
		}
	}
	result, err := s.store.ImportAccounts(request.Context(), identity.OwnerKey, parsed.Accounts, body.Mode)
	if err != nil {
		return err
	}
	accounts, err := s.store.ListAccounts(request.Context(), identity.OwnerKey)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusCreated, map[string]any{"inserted": result.Inserted, "updated": result.Updated, "skipped": result.Skipped, "accounts": accounts})
	return nil
}

func (s *Server) exportAccounts(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if !accountIDsAllowed(request, body.IDs) {
		return validation("邮箱账号 ID 列表无效")
	}
	accounts, err := s.store.GetAccountCredentialsBatch(request.Context(), identityFrom(request).OwnerKey, body.IDs)
	if err != nil {
		return err
	}
	if len(accounts) != uniqueCount(body.IDs) {
		writeJSON(response, http.StatusNotFound, map[string]any{"error": "部分邮箱账号不存在"})
		return nil
	}
	response.Header().Set("Cache-Control", "no-store, private")
	writeJSON(response, http.StatusOK, map[string]any{"filename": "mail.txt", "content": importer.Serialize(accounts)})
	return nil
}

func (s *Server) orderAccounts(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if !accountIDsAllowed(request, body.IDs) {
		return validation("邮箱账号顺序无效")
	}
	ok, err := s.store.ReorderAccounts(request.Context(), identityFrom(request).OwnerKey, body.IDs)
	if err != nil {
		return err
	}
	if !ok {
		writeJSON(response, http.StatusBadRequest, map[string]any{"error": "邮箱账号顺序无效"})
		return nil
	}
	return s.listAccounts(response, request)
}

func (s *Server) updateAccount(response http.ResponseWriter, request *http.Request) error {
	id, err := parseID(request.PathValue("id"))
	if err != nil {
		return err
	}
	var body struct {
		Remark *string `json:"remark"`
		Group  *string `json:"group"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if body.Remark == nil && body.Group == nil {
		return validation("必须提供备注或分组")
	}
	if body.Remark != nil && len([]rune(*body.Remark)) > 200 || body.Group != nil && len([]rune(strings.TrimSpace(*body.Group))) > 80 {
		return validation("备注或分组长度超限")
	}
	if body.Group != nil {
		value := strings.TrimSpace(*body.Group)
		body.Group = &value
	}
	account, err := s.store.UpdateAccount(request.Context(), identityFrom(request).OwnerKey, id, store.AccountChanges{Remark: body.Remark, Group: body.Group})
	if err != nil {
		return err
	}
	if account == nil {
		writeJSON(response, http.StatusNotFound, map[string]any{"error": "邮箱账号不存在"})
		return nil
	}
	writeJSON(response, http.StatusOK, map[string]any{"account": account})
	return nil
}

func (s *Server) updateAccountToken(response http.ResponseWriter, request *http.Request) error {
	id, err := parseID(request.PathValue("id"))
	if err != nil {
		return err
	}
	var body struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if len(body.RefreshToken) < 20 {
		return validation("Refresh Token 格式不正确")
	}
	account, err := s.store.UpdateAccount(request.Context(), identityFrom(request).OwnerKey, id, store.AccountChanges{RefreshToken: &body.RefreshToken})
	if err != nil {
		return err
	}
	if account == nil {
		writeJSON(response, http.StatusNotFound, map[string]any{"error": "邮箱账号不存在"})
		return nil
	}
	writeJSON(response, http.StatusOK, map[string]any{"account": account})
	return nil
}

func (s *Server) deleteAccount(response http.ResponseWriter, request *http.Request) error {
	id, err := parseID(request.PathValue("id"))
	if err != nil {
		return err
	}
	deleted, err := s.store.DeleteAccount(request.Context(), identityFrom(request).OwnerKey, id)
	if err != nil {
		return err
	}
	response.WriteHeader(map[bool]int{true: http.StatusNoContent, false: http.StatusNotFound}[deleted])
	return nil
}

func (s *Server) groupAccounts(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		IDs   []int64 `json:"ids"`
		Group string  `json:"group"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	body.Group = strings.TrimSpace(body.Group)
	if !accountIDsAllowed(request, body.IDs) || len([]rune(body.Group)) > 80 {
		return validation("账号 ID 或分组无效")
	}
	ok, err := s.store.SetAccountsGroup(request.Context(), identityFrom(request).OwnerKey, body.IDs, body.Group)
	if err != nil {
		return err
	}
	if !ok {
		writeJSON(response, http.StatusNotFound, map[string]any{"error": "部分邮箱账号不存在"})
		return nil
	}
	return s.listAccounts(response, request)
}

func (s *Server) deleteAccounts(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		return err
	}
	if !accountIDsAllowed(request, body.IDs) {
		return validation("邮箱账号 ID 列表无效")
	}
	deleted, err := s.store.DeleteAccounts(request.Context(), identityFrom(request).OwnerKey, body.IDs)
	if err != nil {
		return err
	}
	if deleted == nil {
		writeJSON(response, http.StatusNotFound, map[string]any{"error": "部分邮箱账号不存在"})
		return nil
	}
	accounts, err := s.store.ListAccounts(request.Context(), identityFrom(request).OwnerKey)
	if err != nil {
		return err
	}
	writeJSON(response, http.StatusOK, map[string]any{"deleted": *deleted, "accounts": accounts})
	return nil
}

func positiveIDs(ids []int64) bool {
	for _, id := range ids {
		if id < 1 {
			return false
		}
	}
	return true
}

func accountIDsAllowed(request *http.Request, ids []int64) bool {
	if len(ids) < 1 || !positiveIDs(ids) {
		return false
	}
	identity := identityFrom(request)
	return identity == nil || identity.Kind != "guest" || len(ids) <= guestAccountLimit
}

func uniqueCount(ids []int64) int {
	values := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		values[id] = struct{}{}
	}
	return len(values)
}
