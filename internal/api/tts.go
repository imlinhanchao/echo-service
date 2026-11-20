package api

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/imlinhanchao/echo-service/internal/tts"
)

func (s *Server) handleTTSModels(w http.ResponseWriter, r *http.Request) {
	health, healthErr := s.tts.Health(r.Context())
	models, modelsErr := s.tts.Models(r.Context())
	if healthErr != nil && modelsErr != nil {
		writeError(w, http.StatusBadGateway, "TTS_UNAVAILABLE", "TTS 聚合服务不可用: "+modelsErr.Error())
		return
	}
	if models == nil {
		models = []tts.Model{}
	}
	for i := range models {
		if models[i].Provider != "" {
			continue
		}
		if strings.Contains(strings.ToLower(models[i].ID), "edge") {
			models[i].Provider = "edge"
		} else {
			models[i].Provider = "local"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"health":        health,
		"models":        models,
		"max_job_chars": s.cfg.MaxJobChars,
	})
}

func (s *Server) handleTTSVoices(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		writeError(w, http.StatusBadRequest, "INVALID_MODEL", "缺少 model 参数")
		return
	}
	voices, err := s.tts.Voices(r.Context(), model)
	if err != nil {
		writeError(w, http.StatusBadGateway, "TTS_UNAVAILABLE", "读取音色失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"voices": voices})
}

// sampleText is synthesized once per (model, voice) pair and cached on disk.
const sampleText = "这是一段试听示例。声音的温度，藏在每一句话的停顿里。愿你在书声里遇见另一个世界。"

var (
	sampleLocksMu sync.Mutex
	sampleLocks   = map[string]*sync.Mutex{}
)

func sampleLock(key string) *sync.Mutex {
	sampleLocksMu.Lock()
	defer sampleLocksMu.Unlock()
	mu, ok := sampleLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		sampleLocks[key] = mu
	}
	return mu
}

var sampleNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sampleFileName(model, voice string) string {
	name := model + "__" + voice
	return sampleNameRe.ReplaceAllString(name, "_") + ".wav"
}

// handleTTSSample returns a short cached sample for one model/voice pair so the
// frontend can audition every TTS voice without waiting for a full chapter.
func (s *Server) handleTTSSample(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	voice := strings.TrimSpace(r.URL.Query().Get("voice"))
	if model == "" {
		writeError(w, http.StatusBadRequest, "INVALID_MODEL", "缺少 model 参数")
		return
	}
	if voice == "" {
		if voices, err := s.tts.Voices(r.Context(), model); err == nil && len(voices) > 0 {
			voice = voices[0].ID
		}
	}

	dir := filepath.Join(s.cfg.DataDir, "tts_samples")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "创建试听缓存目录失败")
		return
	}
	path := filepath.Join(dir, sampleFileName(model, voice))

	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		writeSample(w, data, model, voice, true)
		return
	}

	mu := sampleLock(model + "|" + voice)
	mu.Lock()
	defer mu.Unlock()
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		writeSample(w, data, model, voice, true)
		return
	}

	res, err := s.tts.Speech(r.Context(), tts.SpeechRequest{
		Model:    model,
		Input:    sampleText,
		Voice:    voice,
		LangCode: "zh",
		Format:   "wav",
		Speed:    1.0,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "TTS_SAMPLE_FAILED", "试听合成失败: "+err.Error())
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, res.Audio, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "写入试听缓存失败")
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
	writeSample(w, res.Audio, model, voice, false)
}

func writeSample(w http.ResponseWriter, data []byte, model, voice string, cached bool) {
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Sample-Model", model)
	w.Header().Set("X-Sample-Voice", voice)
	w.Header().Set("X-Sample-Cached", strconv.FormatBool(cached))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
