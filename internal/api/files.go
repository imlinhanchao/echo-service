package api

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) handleBookFile(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	id := uint64(atoi(r.PathValue("id"), 0))
	book, err := s.repos.Books.ByID(r.Context(), user.ID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "BOOK_NOT_FOUND", "书籍不存在")
		return
	}
	if book.FilePath == "" {
		writeError(w, http.StatusNotFound, "FILE_NOT_FOUND", "文件不存在")
		return
	}
	full := filepath.Join(s.cfg.DataDir, book.FilePath)
	filename := safeFilename(book.Title) + "." + book.Format
	serveDownload(w, r, full, filename)
}

func (s *Server) handleAudiobookFile(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	id := uint64(atoi(r.PathValue("id"), 0))
	item, err := s.repos.Audiobooks.ByID(r.Context(), user.ID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "AUDIOBOOK_NOT_FOUND", "有声书不存在")
		return
	}
	variant := r.URL.Query().Get("variant")
	if variant == "" {
		variant = "audio"
	}
	title := "audiobook"
	if book, err := s.repos.Books.ByID(r.Context(), user.ID, item.BookID); err == nil {
		title = safeFilename(book.Title)
	}
	switch variant {
	case "chapter", "chapter_srt":
		n := atoi(r.URL.Query().Get("n"), 0)
		if n <= 0 {
			writeError(w, http.StatusBadRequest, "INVALID_CHUNK", "缺少有效的 n 参数")
			return
		}
		base := ""
		if item.ManifestPath != "" {
			base = filepath.Dir(filepath.Join(s.cfg.DataDir, item.ManifestPath))
		} else if item.AudioPath != "" {
			base = filepath.Dir(filepath.Join(s.cfg.DataDir, item.AudioPath))
		}
		if base == "" {
			writeError(w, http.StatusNotFound, "FILE_NOT_FOUND", "章节音频尚未生成")
			return
		}
		if variant == "chapter_srt" {
			serveDownload(w, r, filepath.Join(base, "chapters", fmt.Sprintf("%04d.srt", n)), title+fmt.Sprintf("-%04d.srt", n))
			return
		}
		for _, ext := range []string{"mp3", "wav"} {
			p := filepath.Join(base, "chapters", fmt.Sprintf("%04d.%s", n, ext))
			if _, err := os.Stat(p); err == nil {
				serveDownload(w, r, p, title+fmt.Sprintf("-%04d.%s", n, ext))
				return
			}
		}
		writeError(w, http.StatusNotFound, "FILE_NOT_FOUND", "章节音频不存在")
	case "audio":
		if item.AudioPath == "" {
			writeError(w, http.StatusNotFound, "FULL_AUDIO_REMOVED", "整本音频已按章节分块，请从章节列表下载")
			return
		}
		ext := item.Format
		if ext == "" {
			ext = "wav"
		}
		serveDownload(w, r, filepath.Join(s.cfg.DataDir, item.AudioPath), title+"."+ext)
	case "srt":
		if item.SRTPath == "" {
			writeError(w, http.StatusNotFound, "FILE_NOT_FOUND", "字幕尚未生成")
			return
		}
		serveDownload(w, r, filepath.Join(s.cfg.DataDir, item.SRTPath), title+".srt")
	default:
		writeError(w, http.StatusBadRequest, "INVALID_VARIANT", "variant 只能是 audio 或 srt")
	}
}

func serveDownload(w http.ResponseWriter, r *http.Request, fullPath, filename string) {
	// Paths come from the database only, but verify the file is under DataDir.
	if !strings.HasPrefix(filepath.Clean(fullPath), filepath.Clean(filepath.Dir(fullPath))) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "非法文件路径")
		return
	}
	if _, err := os.Stat(fullPath); err != nil {
		writeError(w, http.StatusNotFound, "FILE_NOT_FOUND", "文件不存在或已被清理")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filename))
	w.Header().Set("Cache-Control", "private, max-age=60")
	http.ServeFile(w, r, fullPath)
}

func safeFilename(s string) string {
	s = strings.TrimSpace(s)
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", "\x00", "", "\r", "", "\n", "",
		"\"", "'", ":", "：",
	)
	s = replacer.Replace(s)
	if s == "" {
		return "book"
	}
	runes := []rune(s)
	if len(runes) > 120 {
		s = string(runes[:120])
	}
	return s
}
