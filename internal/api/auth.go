package api

import (
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/imlinhanchao/echo-service/internal/auth"
)

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.AllowRegistration {
		writeError(w, http.StatusForbidden, "REGISTRATION_CLOSED", "注册已关闭，请联系管理员")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !emailRe.MatchString(email) {
		writeError(w, http.StatusBadRequest, "INVALID_EMAIL", "邮箱格式不正确")
		return
	}
	if len(body.Password) < 8 {
		writeError(w, http.StatusBadRequest, "WEAK_PASSWORD", "密码至少 8 位")
		return
	}
	if _, err := s.repos.Users.ByEmail(r.Context(), email); err == nil {
		writeError(w, http.StatusConflict, "EMAIL_TAKEN", "该邮箱已注册")
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "密码处理失败")
		return
	}
	user, err := s.repos.Users.Create(r.Context(), email, hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "创建账号失败")
		return
	}
	if err := s.startSession(r.Context(), w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "创建会话失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": userToView(user)})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !s.limiter.allow(clientIP(r) + "|" + email) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "尝试过于频繁，请稍后再试")
		return
	}
	user, err := s.repos.Users.ByEmail(r.Context(), email)
	if err != nil || !auth.VerifyPassword(user.PasswordHash, body.Password) {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "邮箱或密码不正确")
		return
	}
	if user.Disabled {
		writeError(w, http.StatusForbidden, "ACCOUNT_DISABLED", "账号已被禁用")
		return
	}
	if err := s.startSession(r.Context(), w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "创建会话失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": userToView(user)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":              userToView(user),
		"registration_open": s.cfg.AllowRegistration,
	})
}

// handleTokenLogin exchanges email/password for a long-lived Bearer token. This
// is the login flow for Echo App; the web test UI keeps using the session cookie.
func (s *Server) handleTokenLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !s.limiter.allow(clientIP(r) + "|token|" + email) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "尝试过于频繁，请稍后再试")
		return
	}
	user, err := s.repos.Users.ByEmail(r.Context(), email)
	if err != nil || !auth.VerifyPassword(user.PasswordHash, body.Password) {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "邮箱或密码不正确")
		return
	}
	if user.Disabled {
		writeError(w, http.StatusForbidden, "ACCOUNT_DISABLED", "账号已被禁用")
		return
	}
	token, err := auth.NewAPIToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "生成 Token 失败")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "app"
	}
	expires := time.Now().Add(s.cfg.TokenTTL)
	if _, err := s.repos.APITokens.Create(r.Context(), user.ID, name, auth.HashToken(token), expires); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "保存 Token 失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"token_type": "Bearer",
		"expires_at": expires.UTC(),
		"user":       userToView(user),
	})
}

// handleTokenRefresh rotates a Bearer token: the old one is revoked immediately.
func (s *Server) handleTokenRefresh(w http.ResponseWriter, r *http.Request) {
	oldToken, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "BEARER_REQUIRED", "刷新 Token 需要 Bearer Token")
		return
	}
	row, err := s.repos.APITokens.ByHash(r.Context(), auth.HashToken(oldToken))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "INVALID_TOKEN", "Token 已失效，请重新登录")
		return
	}
	token, err := auth.NewAPIToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "生成 Token 失败")
		return
	}
	expires := time.Now().Add(s.cfg.TokenTTL)
	if _, err := s.repos.APITokens.Create(r.Context(), row.UserID, row.Name, auth.HashToken(token), expires); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "保存 Token 失败")
		return
	}
	if err := s.repos.APITokens.Revoke(r.Context(), row.ID); err != nil {
		// The new token is valid; log the failure but do not fail the request.
		log.Printf("[api] revoke old token %d failed: %v", row.ID, err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"token_type": "Bearer",
		"expires_at": expires.UTC(),
		"user":       userToView(userFrom(r)),
	})
}

// handleTokenRevoke revokes the Bearer token used for this request.
func (s *Server) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	token, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "BEARER_REQUIRED", "撤销 Token 需要 Bearer Token")
		return
	}
	if err := s.repos.APITokens.RevokeByHash(r.Context(), auth.HashToken(token)); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "撤销 Token 失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
