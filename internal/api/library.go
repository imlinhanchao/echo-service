package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"github.com/imlinhanchao/echo-service/internal/audiobook"
	"github.com/imlinhanchao/echo-service/internal/store/models"
)

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	var body struct {
		Provider string `json:"provider"`
		Ref      string `json:"ref"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.Provider = strings.TrimSpace(body.Provider)
	body.Ref = strings.TrimSpace(body.Ref)
	if body.Provider == "" || body.Ref == "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "provider 和 ref 不能为空")
		return
	}
	if _, ok := s.registry.Get(body.Provider); !ok {
		writeError(w, http.StatusNotFound, "PROVIDER_NOT_FOUND", "未知图书源")
		return
	}
	if _, err := s.loadCredentials(r.Context(), user.ID, body.Provider); err != nil {
		mapProviderError(w, err)
		return
	}
	job, err := s.jobs.Submit(r.Context(), user.ID, models.JobKindDownload, map[string]string{
		"provider": body.Provider,
		"ref":      body.Ref,
	}, nil)
	if err != nil {
		mapJobSubmitError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": jobToView(job)})
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	books, err := s.repos.Books.List(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "读取书库失败")
		return
	}
	out := make([]libraryBookView, 0, len(books))
	for i := range books {
		out = append(out, bookToView(&books[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"books": out})
}

func (s *Server) handleLibraryItem(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	id := uint64(atoi(r.PathValue("id"), 0))
	book, err := s.repos.Books.ByID(r.Context(), user.ID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "BOOK_NOT_FOUND", "书籍不存在")
		return
	}
	audiobooks, err := s.repos.Audiobooks.ListByBook(r.Context(), user.ID, book.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "读取有声书失败")
		return
	}
	out := make([]audiobookView, 0, len(audiobooks))
	for i := range audiobooks {
		out = append(out, audiobookToView(&audiobooks[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"book": bookToView(book), "audiobooks": out})
}

func (s *Server) handleDeleteBook(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	id := uint64(atoi(r.PathValue("id"), 0))
	book, err := s.repos.Books.ByID(r.Context(), user.ID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "BOOK_NOT_FOUND", "书籍不存在")
		return
	}
	// Remove generated audio directories first (rows are deleted by FK cascade).
	if audiobooks, err := s.repos.Audiobooks.ListByBook(r.Context(), user.ID, book.ID); err == nil {
		for i := range audiobooks {
			audio := audiobooks[i]
			if audio.ManifestPath != "" {
				_ = os.RemoveAll(filepath.Dir(filepath.Join(s.cfg.DataDir, audio.ManifestPath)))
			} else if audio.AudioPath != "" {
				_ = os.RemoveAll(filepath.Dir(filepath.Join(s.cfg.DataDir, audio.AudioPath)))
			}
		}
	}
	if book.FilePath != "" {
		_ = os.Remove(filepath.Join(s.cfg.DataDir, book.FilePath))
	}
	if err := s.repos.Books.Delete(r.Context(), user.ID, book.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "删除书籍失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type chapterView struct {
	Index   int    `json:"index"`
	Title   string `json:"title"`
	Chars   int    `json:"chars"`
	Preview string `json:"preview"`
	Text    string `json:"text,omitempty"`
}

func (s *Server) handleChapters(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	id := uint64(atoi(r.PathValue("id"), 0))
	book, err := s.repos.Books.ByID(r.Context(), user.ID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "BOOK_NOT_FOUND", "书籍不存在")
		return
	}
	if book.Format != "epub" {
		writeError(w, http.StatusBadRequest, "UNSUPPORTED_FORMAT", "只有 EPUB 支持章节解析，当前格式为 "+book.Format)
		return
	}
	chapters, err := audiobook.ParseEPUB(filepath.Join(s.cfg.DataDir, book.FilePath))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "PARSE_FAILED", "解析 EPUB 失败: "+err.Error())
		return
	}
	full := r.URL.Query().Get("full") == "1"
	out := make([]chapterView, 0, len(chapters))
	for _, ch := range chapters {
		v := chapterView{Index: ch.Index, Title: ch.Title, Chars: ch.Chars, Preview: preview(ch.Text, 120)}
		if full {
			v.Text = ch.Text
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"chapters": out})
}

func (s *Server) handleCreateAudiobook(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	bookID := uint64(atoi(r.PathValue("id"), 0))
	book, err := s.repos.Books.ByID(r.Context(), user.ID, bookID)
	if err != nil {
		writeError(w, http.StatusNotFound, "BOOK_NOT_FOUND", "书籍不存在")
		return
	}
	if book.Format != "epub" {
		writeError(w, http.StatusBadRequest, "UNSUPPORTED_FORMAT", "目前只支持 EPUB 转有声书，当前格式为 "+book.Format)
		return
	}
	var body struct {
		Model         string  `json:"model"`
		Voice         string  `json:"voice"`
		Speed         float64 `json:"speed"`
		Instruct      string  `json:"instruct"`
		FallbackModel string  `json:"fallback_model"`
		Chapters      []int   `json:"chapters"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.Model = strings.TrimSpace(body.Model)
	if body.Model == "" {
		writeError(w, http.StatusBadRequest, "INVALID_MODEL", "请选择 TTS 模型")
		return
	}
	if body.Speed <= 0 {
		body.Speed = 1.0
	}
	if body.Speed < 0.6 {
		body.Speed = 0.6
	}
	if body.Speed > 1.6 {
		body.Speed = 1.6
	}
	payload := map[string]any{
		"book_id":        book.ID,
		"model":          body.Model,
		"voice":          body.Voice,
		"speed":          body.Speed,
		"instruct":       body.Instruct,
		"fallback_model": body.FallbackModel,
		"chapters":       body.Chapters,
	}
	job, err := s.jobs.Submit(r.Context(), user.ID, models.JobKindAudiobook, payload, &book.ID)
	if err != nil {
		mapJobSubmitError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": jobToView(job)})
}

func (s *Server) handleAudiobooks(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	items, err := s.repos.Audiobooks.List(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "读取有声书失败")
		return
	}
	out := make([]audiobookView, 0, len(items))
	for i := range items {
		v := audiobookToView(&items[i])
		if book, err := s.repos.Books.ByID(r.Context(), user.ID, items[i].BookID); err == nil {
			v.BookTitle = book.Title
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"audiobooks": out})
}

func (s *Server) handleAudiobook(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	id := uint64(atoi(r.PathValue("id"), 0))
	item, err := s.repos.Audiobooks.ByID(r.Context(), user.ID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "AUDIOBOOK_NOT_FOUND", "有声书不存在")
		return
	}
	v := audiobookToView(item)
	if book, err := s.repos.Books.ByID(r.Context(), user.ID, item.BookID); err == nil {
		v.BookTitle = book.Title
	}
	writeJSON(w, http.StatusOK, map[string]any{"audiobook": v})
}

func (s *Server) handleDeleteAudiobook(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	id := uint64(atoi(r.PathValue("id"), 0))
	item, err := s.repos.Audiobooks.ByID(r.Context(), user.ID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "AUDIOBOOK_NOT_FOUND", "有声书不存在")
		return
	}
	if item.ManifestPath != "" {
		_ = os.RemoveAll(filepath.Dir(filepath.Join(s.cfg.DataDir, item.ManifestPath)))
	} else if item.AudioPath != "" {
		_ = os.RemoveAll(filepath.Dir(filepath.Join(s.cfg.DataDir, item.AudioPath)))
	}
	if err := s.repos.Audiobooks.Delete(r.Context(), user.ID, item.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "删除有声书失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func mapJobSubmitError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "INTERNAL"
	msg := err.Error()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		status, code = http.StatusNotFound, "NOT_FOUND"
	}
	if strings.Contains(msg, "进行中的任务") {
		status, code = http.StatusConflict, "JOB_BUSY"
	}
	if strings.Contains(msg, "队列已满") {
		status, code = http.StatusTooManyRequests, "QUEUE_FULL"
	}
	writeError(w, status, code, msg)
}

func preview(text string, n int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "…"
}
