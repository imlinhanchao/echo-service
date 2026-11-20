package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/imlinhanchao/echo-service/internal/audiobook"
)

type chunkView struct {
	audiobook.ChunkInfo
	AudioURL string `json:"audio_url"`
	SRTURL   string `json:"srt_url"`
}

// handleAudiobookChapters returns the chapter manifest of a finished audiobook
// (chapter chunks <=30 min, each with its own audio and SRT).
func (s *Server) handleAudiobookChapters(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	id := uint64(atoi(r.PathValue("id"), 0))
	item, err := s.repos.Audiobooks.ByID(r.Context(), user.ID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "AUDIOBOOK_NOT_FOUND", "有声书不存在")
		return
	}
	dir := ""
	if item.ManifestPath != "" {
		dir = filepath.Dir(filepath.Join(s.cfg.DataDir, item.ManifestPath))
	} else if item.AudioPath != "" {
		dir = filepath.Dir(filepath.Join(s.cfg.DataDir, item.AudioPath))
	}
	if dir == "" {
		writeError(w, http.StatusNotFound, "CHAPTERS_NOT_READY", "音频尚未生成")
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		writeError(w, http.StatusNotFound, "CHAPTERS_NOT_READY", "章节清单尚未生成")
		return
	}
	var manifest audiobook.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "章节清单损坏")
		return
	}
	chunks := make([]chunkView, 0, len(manifest.Chunks))
	for _, c := range manifest.Chunks {
		chunks = append(chunks, chunkView{
			ChunkInfo: c,
			AudioURL:  fmt.Sprintf("/api/files/audiobooks/%d?variant=chapter&n=%d", item.ID, c.N),
			SRTURL:    fmt.Sprintf("/api/files/audiobooks/%d?variant=chapter_srt&n=%d", item.ID, c.N),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"audiobook_id":  item.ID,
		"format":        manifest.Format,
		"duration_s":    manifest.DurationS,
		"chunk_seconds": manifest.ChunkSeconds,
		"chunks":        chunks,
		"audio_url":     fmt.Sprintf("/api/files/audiobooks/%d?variant=audio", item.ID),
		"srt_url":       fmt.Sprintf("/api/files/audiobooks/%d?variant=srt", item.ID),
	})
}
