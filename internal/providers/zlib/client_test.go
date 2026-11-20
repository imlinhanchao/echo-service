package zlib

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/imlinhanchao/echo-service/internal/providers"
)

func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func testCredentials(domain string) providers.Credentials {
	return providers.Credentials{
		"domain":        domain,
		"remix_userid":  "42",
		"remix_userkey": "FAKE-KEY",
	}
}

func requireErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is(%v)", err, target)
	}
}

type capturedRequest struct {
	method      string
	path        string
	form        string
	accept      string
	contentType string
	userAgent   string
	userID      string
	userKey     string
	cookies     map[string]string
}

func TestClientDoSendsProtocol(t *testing.T) {
	captured := make(chan capturedRequest, 1)
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cookies := make(map[string]string)
		for _, cookie := range r.Cookies() {
			cookies[cookie.Name] = cookie.Value
		}
		captured <- capturedRequest{
			method:      r.Method,
			path:        r.URL.Path,
			form:        string(body),
			accept:      r.Header.Get("Accept"),
			contentType: r.Header.Get("Content-Type"),
			userAgent:   r.Header.Get("User-Agent"),
			userID:      r.Header.Get("remix-userid"),
			userKey:     r.Header.Get("remix-userkey"),
			cookies:     cookies,
		}
		writeJSON(w, `{"success":1}`)
	})

	c := newClient(server.URL, "42", "FAKE-KEY", nil)
	body, err := c.do(context.Background(), http.MethodPost, eapiSearchPath, url.Values{
		"message": {"dune"},
		"page":    {"2"},
	})
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if string(body) != `{"success":1}` {
		t.Fatalf("body = %q", body)
	}

	got := <-captured
	if got.method != http.MethodPost {
		t.Errorf("method = %q", got.method)
	}
	if got.path != eapiSearchPath {
		t.Errorf("path = %q", got.path)
	}
	if !strings.Contains(got.form, "message=dune") || !strings.Contains(got.form, "page=2") {
		t.Errorf("form = %q", got.form)
	}
	if got.accept != "application/json" {
		t.Errorf("Accept = %q", got.accept)
	}
	if !strings.HasPrefix(got.contentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q", got.contentType)
	}
	if !strings.Contains(got.userAgent, "Chrome/") {
		t.Errorf("User-Agent = %q", got.userAgent)
	}
	if got.userID != "42" || got.userKey != "FAKE-KEY" {
		t.Errorf("remix headers = %q/%q", got.userID, got.userKey)
	}
	if got.cookies["remix_userid"] != "42" || got.cookies["remix_userkey"] != "FAKE-KEY" {
		t.Errorf("cookies = %v", got.cookies)
	}
}

func TestClientLogin(t *testing.T) {
	tests := []struct {
		name     string
		response string
		wantID   string
		wantKey  string
		wantErr  bool
	}{
		{
			name:     "numeric id",
			response: `{"success":1,"user":{"id":12345,"remix_userkey":"KEY"}}`,
			wantID:   "12345",
			wantKey:  "KEY",
		},
		{
			name:     "string id with legacy key field",
			response: `{"success":1,"user":{"id":"777","remix_user_key":"LEGACY"}}`,
			wantID:   "777",
			wantKey:  "LEGACY",
		},
		{
			name:     "rejected",
			response: `{"success":0,"error":"Invalid email or password"}`,
			wantErr:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != eapiLoginPath {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				_ = r.ParseForm()
				if r.PostForm.Get("email") != "reader@example.com" || r.PostForm.Get("password") != "secret" {
					t.Errorf("form = %v", r.PostForm)
				}
				writeJSON(w, test.response)
			})

			c := newClient(server.URL, "", "", nil)
			userID, userKey, err := c.login(context.Background(), "reader@example.com", "secret")
			if test.wantErr {
				if err == nil {
					t.Fatal("login succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("login: %v", err)
			}
			if userID != test.wantID || userKey != test.wantKey {
				t.Fatalf("credentials = %q/%q, want %q/%q", userID, userKey, test.wantID, test.wantKey)
			}
		})
	}
}

func TestClientErrorClassification(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		location    string
		body        string
		withSession bool
		want        error
		wantMessage string
	}{
		{
			name:        "expired session",
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body:        `{"success":0,"error":"Invalid credentials"}`,
			withSession: true,
			want:        providers.ErrSessionExpired,
			wantMessage: "Invalid credentials",
		},
		{
			name:        "please login",
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body:        `{"success":0,"error":"Please login"}`,
			withSession: true,
			want:        providers.ErrSessionExpired,
		},
		{
			name:        "blocked html",
			status:      http.StatusForbidden,
			contentType: "text/html; charset=utf-8",
			body:        `<html><body>challenge</body></html>`,
			want:        providers.ErrBlocked,
		},
		{
			name:        "blocked redirect",
			status:      http.StatusServiceUnavailable,
			contentType: "text/plain",
			location:    "https://diamwall.example/check",
			want:        providers.ErrBlocked,
		},
		{
			name:        "gateway json is unavailable",
			status:      http.StatusServiceUnavailable,
			contentType: "application/json",
			body:        `{"success":0,"error":"upstream down"}`,
			want:        providers.ErrProviderUnavailable,
		},
		{
			name:        "daily limit",
			status:      http.StatusOK,
			contentType: "application/json",
			body:        `{"success":0,"error":"Daily limit reached"}`,
			withSession: true,
			want:        providers.ErrQuotaExceeded,
			wantMessage: "Daily limit",
		},
		{
			name:        "server error",
			status:      http.StatusInternalServerError,
			contentType: "text/plain",
			body:        "boom",
			want:        providers.ErrProviderUnavailable,
		},
		{
			name:        "not found",
			status:      http.StatusNotFound,
			contentType: "application/json",
			body:        `{"success":0,"error":"book not found"}`,
			want:        providers.ErrNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if test.location != "" {
					w.Header().Set("Location", test.location)
				}
				w.Header().Set("Content-Type", test.contentType)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			})

			userKey := ""
			if test.withSession {
				userKey = "FAKE-KEY"
			}
			c := newClient(server.URL, "42", userKey, nil)
			_, err := c.do(context.Background(), http.MethodGet, eapiProfilePath, nil)
			if err == nil {
				t.Fatal("request succeeded, want error")
			}
			requireErrorIs(t, err, test.want)
			if test.wantMessage != "" && !strings.Contains(err.Error(), test.wantMessage) {
				t.Errorf("error = %v, want message containing %q", err, test.wantMessage)
			}
		})
	}
}

func TestClientProbeDomain(t *testing.T) {
	healthy := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != eapiDomainsInfoPath {
			t.Errorf("path = %q", r.URL.Path)
		}
		writeJSON(w, `{"success":1,"domains":["example.test"]}`)
	})
	unhealthy := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	if !probeDomain(context.Background(), healthy.URL, healthy.Client()) {
		t.Error("healthy mirror reported as unhealthy")
	}
	if probeDomain(context.Background(), unhealthy.URL, unhealthy.Client()) {
		t.Error("unhealthy mirror reported as healthy")
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"traversal", `../../etc/passwd`, "passwd"},
		{"windows traversal", `..\..\evil.epub`, "evil.epub"},
		{"control characters", "../\x01evil\x1bname.epub", "evilname.epub"},
		{"bidi override", "book\u202e.epub", "book.epub"},
		{"empty", "..", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sanitizeFilename(test.in); got != test.want {
				t.Fatalf("sanitizeFilename(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}

	long := strings.Repeat("a", 300) + ".epub"
	got := sanitizeFilename(long)
	if len([]rune(got)) > maxFilenameRunes {
		t.Fatalf("filename has %d runes, want at most %d", len([]rune(got)), maxFilenameRunes)
	}
	if !strings.HasSuffix(got, ".epub") {
		t.Fatalf("truncated filename lost its extension: %q", got)
	}
}

func TestFilenameFromResponse(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Content-Disposition", `attachment; filename="plain name.epub"`)
	if got := filenameFromResponse(resp); got != "plain name.epub" {
		t.Fatalf("filename = %q", got)
	}

	resp.Header.Set("Content-Disposition", `attachment; filename*=UTF-8''caf%C3%A9.epub`)
	if got := filenameFromResponse(resp); got != "café.epub" {
		t.Fatalf("RFC 5987 filename = %q", got)
	}
}
