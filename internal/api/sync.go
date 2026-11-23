package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/imlinhanchao/echo-service/internal/store/models"
)

type progressPayload struct {
	ItemType     string    `json:"item_type"`
	ItemID       uint64    `json:"item_id"`
	ChapterIndex int       `json:"chapter_index"`
	ChunkN       int       `json:"chunk_n"`
	OffsetMs     int       `json:"offset_ms"`
	CharOffset   int       `json:"char_offset"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type syncProgressBody struct {
	Progresses []progressPayload `json:"progresses"`
}

func (s *Server) handleGetProgress(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	if s.repos == nil || s.repos.Sync == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务未就绪")
		return
	}
	list, err := s.repos.Sync.ListProgress(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "查询进度失败")
		return
	}
	if list == nil {
		list = []models.UserProgress{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"progresses": list})
}

func (s *Server) handlePostProgress(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	if s.repos == nil || s.repos.Sync == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务未就绪")
		return
	}
	var body syncProgressBody
	if !decodeJSON(w, r, &body) {
		return
	}

	for _, p := range body.Progresses {
		p.ItemType = strings.TrimSpace(p.ItemType)
		if p.ItemType == "" || p.ItemID == 0 {
			continue
		}
		if p.UpdatedAt.IsZero() {
			p.UpdatedAt = time.Now().UTC()
		}
		row := &models.UserProgress{
			UserID:       user.ID,
			ItemType:     p.ItemType,
			ItemID:       p.ItemID,
			ChapterIndex: p.ChapterIndex,
			ChunkN:       p.ChunkN,
			OffsetMs:     p.OffsetMs,
			CharOffset:   p.CharOffset,
			UpdatedAt:    p.UpdatedAt,
		}
		if err := s.repos.Sync.UpsertProgress(r.Context(), row); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "保存进度失败")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

type notePayload struct {
	ID          string    `json:"id"`
	AudiobookID uint64    `json:"audiobook_id"`
	PositionMs  int       `json:"position_ms"`
	Text        string    `json:"text"`
	Chapter     string    `json:"chapter"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type syncNotesBody struct {
	Notes []notePayload `json:"notes"`
}

func (s *Server) handleGetNotes(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	if s.repos == nil || s.repos.Sync == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务未就绪")
		return
	}
	var audiobookID uint64
	if raw := r.URL.Query().Get("audiobook_id"); raw != "" {
		if id, err := strconv.ParseUint(raw, 10, 64); err == nil {
			audiobookID = id
		}
	}

	list, err := s.repos.Sync.ListNotes(r.Context(), user.ID, audiobookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "查询笔记失败")
		return
	}
	if list == nil {
		list = []models.UserNote{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": list})
}

func (s *Server) handlePostNotes(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	if s.repos == nil || s.repos.Sync == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务未就绪")
		return
	}
	var body syncNotesBody
	if !decodeJSON(w, r, &body) {
		return
	}

	for _, n := range body.Notes {
		n.ID = strings.TrimSpace(n.ID)
		if n.ID == "" || n.AudiobookID == 0 {
			continue
		}
		now := time.Now().UTC()
		if n.CreatedAt.IsZero() {
			n.CreatedAt = now
		}
		if n.UpdatedAt.IsZero() {
			n.UpdatedAt = now
		}
		row := &models.UserNote{
			ID:          n.ID,
			UserID:      user.ID,
			AudiobookID: n.AudiobookID,
			PositionMs:  n.PositionMs,
			Text:        n.Text,
			Chapter:     n.Chapter,
			CreatedAt:   n.CreatedAt,
			UpdatedAt:   n.UpdatedAt,
		}
		if err := s.repos.Sync.UpsertNote(r.Context(), row); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "保存笔记失败")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	if s.repos == nil || s.repos.Sync == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务未就绪")
		return
	}
	noteID := strings.TrimSpace(r.PathValue("id"))
	if noteID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "note id 不能为空")
		return
	}

	if err := s.repos.Sync.DeleteNote(r.Context(), user.ID, noteID); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "删除笔记失败")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
