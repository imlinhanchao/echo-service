package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/imlinhanchao/echo-service/internal/config"
)

func TestAppDownloadEndpoints(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{
		cfg: &config.Config{DataDir: dir},
		mux: http.NewServeMux(),
	}
	srv.mux.HandleFunc("GET /api/app/latest", srv.handleLatestApp)
	srv.mux.HandleFunc("GET /downloads/app/latest.apk", srv.handleDownloadLatestApp)

	// 1. No APK yet
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/app/latest", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var emptyResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &emptyResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if emptyResp["available"] != false {
		t.Fatalf("expected available=false, got %v", emptyResp["available"])
	}

	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/downloads/app/latest.apk", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when empty, got %d", rec.Code)
	}

	// 2. Add two APKs; newest should be selected
	dlDir := filepath.Join(dir, "downloads")
	if err := os.MkdirAll(dlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(dlDir, "echo-v0.3.2-arm64.apk")
	newPath := filepath.Join(dlDir, "echo-v0.3.4-arm64.apk")
	if err := os.WriteFile(oldPath, []byte("old-apk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new-apk-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, past, past); err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/app/latest", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["available"] != true || resp["filename"] != "echo-v0.3.4-arm64.apk" || resp["version"] != "v0.3.4" {
		t.Fatalf("unexpected latest metadata: %+v", resp)
	}

	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/downloads/app/latest.apk", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 download, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != "new-apk-bytes" {
		t.Fatalf("unexpected body: %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.android.package-archive" {
		t.Fatalf("unexpected Content-Type: %q", ct)
	}
}
