package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var apkVersionRe = regexp.MustCompile(`v?(\d+\.\d+\.\d+)`)

type apkRelease struct {
	Filename  string
	Path      string
	Version   string
	Size      int64
	UpdatedAt time.Time
}

// latestAPK scans <DataDir>/downloads for .apk files and returns the most
// recently modified one. Returns nil when no APK is present.
func latestAPK(dataDir string) (*apkRelease, error) {
	dir := filepath.Join(dataDir, "downloads")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var best *apkRelease
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(strings.ToLower(name), ".apk") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if best == nil || info.ModTime().After(best.UpdatedAt) {
			version := ""
			if m := apkVersionRe.FindStringSubmatch(name); len(m) >= 2 {
				version = "v" + m[1]
			}
			best = &apkRelease{
				Filename:  name,
				Path:      filepath.Join(dir, name),
				Version:   version,
				Size:      info.Size(),
				UpdatedAt: info.ModTime(),
			}
		}
	}
	return best, nil
}

// handleLatestApp returns metadata for the latest available Android APK.
// Public endpoint so mobile users can grab the APK directly from the login page.
func (s *Server) handleLatestApp(w http.ResponseWriter, r *http.Request) {
	rel, err := latestAPK(s.cfg.DataDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "apk_scan_failed", "扫描安装包目录失败")
		return
	}
	if rel == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available":    true,
		"filename":     rel.Filename,
		"version":      rel.Version,
		"size":         rel.Size,
		"updated_at":   rel.UpdatedAt.UTC().Format(time.RFC3339),
		"download_url": "/downloads/app/latest.apk",
	})
}

// handleDownloadLatestApp streams the latest Android APK with resume support.
func (s *Server) handleDownloadLatestApp(w http.ResponseWriter, r *http.Request) {
	rel, err := latestAPK(s.cfg.DataDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "apk_scan_failed", "扫描安装包目录失败")
		return
	}
	if rel == nil {
		writeError(w, http.StatusNotFound, "apk_not_found", "暂无可下载的 Android 安装包")
		return
	}
	f, err := os.Open(rel.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "apk_not_found", "安装包文件不存在")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", rel.Filename))
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, rel.Filename, rel.UpdatedAt, f)
}
