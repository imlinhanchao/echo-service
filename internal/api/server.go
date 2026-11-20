// Package api exposes the HTTP API and serves the embedded test frontend.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/imlinhanchao/echo-service/internal/auth"
	"github.com/imlinhanchao/echo-service/internal/config"
	"github.com/imlinhanchao/echo-service/internal/jobs"
	"github.com/imlinhanchao/echo-service/internal/providers"
	"github.com/imlinhanchao/echo-service/internal/store/models"
	"github.com/imlinhanchao/echo-service/internal/store/repos"
	"github.com/imlinhanchao/echo-service/internal/tts"
	"github.com/imlinhanchao/echo-service/internal/web"
)

const sessionCookie = "echo_session"

type Server struct {
	cfg      *config.Config
	repos    *repos.Repos
	registry *providers.Registry
	tts      *tts.Client
	jobs     *jobs.Manager
	mux      *http.ServeMux
	limiter  *loginLimiter
}

func New(cfg *config.Config, r *repos.Repos, reg *providers.Registry, ttsClient *tts.Client, jm *jobs.Manager) *Server {
	s := &Server{
		cfg:      cfg,
		repos:    r,
		registry: reg,
		tts:      ttsClient,
		jobs:     jm,
		mux:      http.NewServeMux(),
		limiter:  newLoginLimiter(5, time.Minute),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.logging(s.recoverer(s.csrf(s.mux)))
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": "0.1.0"})
	})

	// auth
	m.HandleFunc("POST /api/auth/register", s.handleRegister)
	m.HandleFunc("POST /api/auth/login", s.handleLogin)
	m.HandleFunc("POST /api/auth/logout", s.handleLogout)
	m.HandleFunc("POST /api/auth/token", s.handleTokenLogin)
	m.HandleFunc("POST /api/auth/token/refresh", s.auth(s.handleTokenRefresh))
	m.HandleFunc("POST /api/auth/token/revoke", s.auth(s.handleTokenRevoke))
	m.HandleFunc("GET /api/auth/me", s.auth(s.handleMe))

	// providers / accounts
	m.HandleFunc("GET /api/providers", s.auth(s.handleProviders))
	m.HandleFunc("PUT /api/providers/{id}/account", s.auth(s.handlePutAccount))
	m.HandleFunc("POST /api/providers/{id}/account/verify", s.auth(s.handleVerifyAccount))
	m.HandleFunc("DELETE /api/providers/{id}/account", s.auth(s.handleDeleteAccount))

	// search / detail
	m.HandleFunc("GET /api/search", s.auth(s.handleSearch))
	m.HandleFunc("GET /api/providers/{id}/books/{ref}", s.auth(s.handleBookDetail))

	// library
	m.HandleFunc("POST /api/library/download", s.auth(s.handleDownload))
	m.HandleFunc("GET /api/library", s.auth(s.handleLibrary))
	m.HandleFunc("GET /api/library/{id}", s.auth(s.handleLibraryItem))
	m.HandleFunc("DELETE /api/library/{id}", s.auth(s.handleDeleteBook))
	m.HandleFunc("GET /api/library/{id}/chapters", s.auth(s.handleChapters))
	m.HandleFunc("POST /api/library/{id}/audiobooks", s.auth(s.handleCreateAudiobook))

	// audiobooks
	m.HandleFunc("GET /api/audiobooks", s.auth(s.handleAudiobooks))
	m.HandleFunc("GET /api/audiobooks/{id}", s.auth(s.handleAudiobook))
	m.HandleFunc("GET /api/audiobooks/{id}/chapters", s.auth(s.handleAudiobookChapters))
	m.HandleFunc("DELETE /api/audiobooks/{id}", s.auth(s.handleDeleteAudiobook))

	// jobs
	m.HandleFunc("GET /api/jobs", s.auth(s.handleJobs))
	m.HandleFunc("GET /api/jobs/{id}", s.auth(s.handleJob))
	m.HandleFunc("POST /api/jobs/{id}/cancel", s.auth(s.handleCancelJob))

	// tts proxy
	m.HandleFunc("GET /api/tts/models", s.auth(s.handleTTSModels))
	m.HandleFunc("GET /api/tts/voices", s.auth(s.handleTTSVoices))
	m.HandleFunc("GET /api/tts/sample", s.auth(s.handleTTSSample))

	// files
	m.HandleFunc("GET /api/files/books/{id}", s.auth(s.handleBookFile))
	m.HandleFunc("GET /api/files/audiobooks/{id}", s.auth(s.handleAudiobookFile))

	// frontend
	m.Handle("GET /", http.FileServerFS(web.Assets))
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

type ctxKey int

const userKey ctxKey = iota

func userFrom(r *http.Request) *models.User {
	u, _ := r.Context().Value(userKey).(*models.User)
	return u
}

// bearerToken extracts the token from an "Authorization: Bearer ..." header.
func bearerToken(r *http.Request) (string, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}

// authenticate resolves the caller from either a Bearer token (Echo App) or the
// browser session cookie (test web UI).
func (s *Server) authenticate(r *http.Request) (*models.User, error) {
	if token, ok := bearerToken(r); ok {
		row, err := s.repos.APITokens.ByHash(r.Context(), auth.HashToken(token))
		if err != nil {
			return nil, err
		}
		user, err := s.repos.Users.ByID(r.Context(), row.UserID)
		if err != nil || user.Disabled {
			return nil, errUserUnavailable
		}
		// Touch last_used_at at most once a minute to avoid a write per request.
		if row.LastUsedAt == nil || time.Since(*row.LastUsedAt) > time.Minute {
			_ = s.repos.APITokens.Touch(r.Context(), row.ID)
		}
		return user, nil
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return nil, errUnauthorized
	}
	sess, err := s.repos.Sessions.ByToken(r.Context(), auth.HashToken(cookie.Value))
	if err != nil {
		return nil, errUnauthorized
	}
	user, err := s.repos.Users.ByID(r.Context(), sess.UserID)
	if err != nil || user.Disabled {
		return nil, errUserUnavailable
	}
	return user, nil
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := s.authenticate(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "请先登录，或在 Authorization 头提供有效的 Bearer Token")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
	}
}

var (
	errUnauthorized    = errors.New("unauthorized")
	errUserUnavailable = errors.New("user unavailable")
)

func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			// Bearer-token requests are not subject to CSRF (no ambient cookie).
			if token, ok := bearerToken(r); ok && token != "" {
				next.ServeHTTP(w, r)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeError(w, http.StatusForbidden, "CSRF", "跨站请求被拒绝")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[api] panic %s %s: %v", r.Method, r.URL.Path, rec)
				writeError(w, http.StatusInternalServerError, "INTERNAL", "服务内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/health" {
			log.Printf("[api] %s %s -> %d (%s)", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

func (s *Server) startSession(ctx context.Context, w http.ResponseWriter, r *http.Request, user *models.User) error {
	token, err := auth.NewToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(s.cfg.SessionTTL)
	if err := s.repos.Sessions.Create(ctx, auth.HashToken(token), user.ID, expires, r.UserAgent(), clientIP(r)); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Server) clearSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		_ = s.repos.Sessions.Delete(r.Context(), auth.HashToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "读取请求体失败")
		return false
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "请求体不能为空")
		return false
	}
	if err := json.Unmarshal(body, out); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "请求体不是合法 JSON")
		return false
	}
	return true
}

// mapProviderError converts provider/plugin errors into HTTP responses.
func mapProviderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, providers.ErrNotConfigured):
		writeError(w, http.StatusConflict, "PROVIDER_NOT_CONFIGURED", "请先在“账号”页配置该图书源的账号")
	case errors.Is(err, providers.ErrSessionExpired):
		writeError(w, http.StatusUnauthorized, "ZLIB_SESSION_EXPIRED", "zlib 会话已失效，请重新登录或更新 Cookie")
	case errors.Is(err, providers.ErrQuotaExceeded):
		writeError(w, http.StatusForbidden, "QUOTA_EXCEEDED", "今日下载额度已用尽")
	case errors.Is(err, providers.ErrBlocked):
		writeError(w, http.StatusServiceUnavailable, "PROVIDER_BLOCKED", "镜像被反爬拦截，请稍后重试或更换域名")
	case errors.Is(err, providers.ErrProviderUnavailable):
		writeError(w, http.StatusBadGateway, "PROVIDER_UNAVAILABLE", "图书源暂不可用："+err.Error())
	case errors.Is(err, providers.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "未找到该图书")
	default:
		writeError(w, http.StatusBadGateway, "PROVIDER_ERROR", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Login rate limiting
// ---------------------------------------------------------------------------

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{attempts: make(map[string][]time.Time), limit: limit, window: window}
}

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)
	kept := l.attempts[key][:0]
	for _, t := range l.attempts[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.attempts[key] = kept
		return false
	}
	l.attempts[key] = append(kept, now)
	return true
}
