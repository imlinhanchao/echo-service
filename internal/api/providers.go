package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/imlinhanchao/echo-service/internal/providers"
	"github.com/imlinhanchao/echo-service/internal/secret"
	"github.com/imlinhanchao/echo-service/internal/store"
	"github.com/imlinhanchao/echo-service/internal/store/models"
)

// accountCredentialsLoader is implemented by providers that support a
// password login (used by the account form).
type accountCredentialsLoader interface {
	Login(ctx context.Context, domain, email, password string) (providers.Credentials, error)
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	out := make([]providerView, 0)
	for _, p := range s.registry.List() {
		info := providers.Describe(p)
		view := providerView{
			ID:             info.ID,
			Name:           info.Name,
			Capabilities:   info.Capabilities,
			DefaultDomain:  info.DefaultDomain,
			Domains:        info.Domains,
			CredentialKeys: info.CredentialKeys,
		}
		if account, err := s.repos.Providers.ByUserProvider(r.Context(), user.ID, info.ID); err == nil {
			view.Account = accountToView(account)
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

func (s *Server) handlePutAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	providerID := r.PathValue("id")
	p, ok := s.registry.Get(providerID)
	if !ok {
		writeError(w, http.StatusNotFound, "PROVIDER_NOT_FOUND", "未知图书源")
		return
	}
	info := providers.Describe(p)

	var body struct {
		Mode         string `json:"mode"`
		Domain       string `json:"domain"`
		Email        string `json:"email"`
		Password     string `json:"password"`
		RemixUserID  string `json:"remix_userid"`
		RemixUserKey string `json:"remix_userkey"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	domain := strings.TrimRight(strings.TrimSpace(body.Domain), "/")
	if domain == "" {
		domain = info.DefaultDomain
	}
	if !domainAllowed(domain, info.Domains) {
		writeError(w, http.StatusBadRequest, "INVALID_DOMAIN", "请选择该图书源支持的镜像域名")
		return
	}

	var creds providers.Credentials
	switch body.Mode {
	case "password":
		login, ok := p.(accountCredentialsLoader)
		if !ok {
			writeError(w, http.StatusBadRequest, "UNSUPPORTED", "该图书源不支持密码登录")
			return
		}
		if strings.TrimSpace(body.Email) == "" || body.Password == "" {
			writeError(w, http.StatusBadRequest, "INVALID_INPUT", "邮箱和密码不能为空")
			return
		}
		var err error
		creds, err = login.Login(r.Context(), domain, body.Email, body.Password)
		if err != nil {
			mapProviderError(w, err)
			return
		}
	case "cookies":
		if strings.TrimSpace(body.RemixUserID) == "" || strings.TrimSpace(body.RemixUserKey) == "" {
			writeError(w, http.StatusBadRequest, "INVALID_INPUT", "remix_userid 和 remix_userkey 不能为空")
			return
		}
		creds = providers.Credentials{
			"domain":        domain,
			"remix_userid":  strings.TrimSpace(body.RemixUserID),
			"remix_userkey": strings.TrimSpace(body.RemixUserKey),
		}
	default:
		writeError(w, http.StatusBadRequest, "INVALID_MODE", "mode 必须是 password 或 cookies")
		return
	}

	accountInfo, err := p.Verify(r.Context(), creds)
	if err != nil {
		mapProviderError(w, err)
		return
	}
	enc, err := secret.EncryptJSON(s.cfg.Secret, creds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "凭据加密失败")
		return
	}
	account, err := s.repos.Providers.Upsert(r.Context(), user.ID, providerID, domain, enc, "ok", store.JSON(accountInfo))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "保存账号失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": accountToView(account)})
}

func (s *Server) handleVerifyAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	providerID := r.PathValue("id")
	p, ok := s.registry.Get(providerID)
	if !ok {
		writeError(w, http.StatusNotFound, "PROVIDER_NOT_FOUND", "未知图书源")
		return
	}
	account, err := s.repos.Providers.ByUserProvider(r.Context(), user.ID, providerID)
	if err != nil {
		writeError(w, http.StatusConflict, "PROVIDER_NOT_CONFIGURED", "请先配置该图书源账号")
		return
	}
	var creds providers.Credentials
	if err := secret.DecryptJSON(s.cfg.Secret, account.CredentialsEnc, &creds); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "读取凭据失败")
		return
	}
	info, err := p.Verify(r.Context(), creds)
	if err != nil {
		status := "error"
		if isSessionError(err) {
			status = "needs_login"
		}
		_ = s.repos.Providers.UpdateStatus(r.Context(), user.ID, providerID, status, nil)
		mapProviderError(w, err)
		return
	}
	if err := s.repos.Providers.UpdateStatus(r.Context(), user.ID, providerID, "ok", store.JSON(info)); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "更新账号状态失败")
		return
	}
	account, _ = s.repos.Providers.ByUserProvider(r.Context(), user.ID, providerID)
	writeJSON(w, http.StatusOK, map[string]any{"account": accountToView(account)})
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	providerID := r.PathValue("id")
	if err := s.repos.Providers.Delete(r.Context(), user.ID, providerID); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "删除账号失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// loadCredentials returns a user's decrypted credentials for a provider.
func (s *Server) loadCredentials(ctx context.Context, userID uint64, providerID string) (providers.Credentials, error) {
	account, err := s.repos.Providers.ByUserProvider(ctx, userID, providerID)
	if err != nil {
		return nil, providers.ErrNotConfigured
	}
	var creds providers.Credentials
	if err := secret.DecryptJSON(s.cfg.Secret, account.CredentialsEnc, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func accountToView(a *models.ProviderAccount) *accountView {
	if a == nil {
		return nil
	}
	view := &accountView{
		Configured:     true,
		Status:         a.Status,
		LastVerifiedAt: a.LastVerifiedAt,
	}
	var info providers.AccountInfo
	if err := json.Unmarshal(a.AccountInfo, &info); err == nil {
		view.Email = info.Email
		view.DailyUsed = info.DailyUsed
		view.DailyLimit = info.DailyLimit
		view.DailyRemaining = info.DailyRemaining
		view.QuotaKnown = info.QuotaKnown
	}
	return view
}

func domainAllowed(domain string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, d := range allowed {
		if strings.EqualFold(strings.TrimRight(d, "/"), domain) {
			return true
		}
	}
	return false
}

func isSessionError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "session")
}
