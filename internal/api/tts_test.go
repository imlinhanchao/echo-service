package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/imlinhanchao/echo-service/internal/config"
	"github.com/imlinhanchao/echo-service/internal/tts"
)

func TestTTSSampleCachesPerVoice(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/voices":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"voice-a","name":"A","description":""}]}`)
		case "/v1/audio/speech":
			calls++
			w.Header().Set("Content-Type", "audio/wav")
			w.Header().Set("X-Audio-Seconds", "3.00")
			_, _ = w.Write([]byte("RIFF-fake-wav-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	dir := t.TempDir()
	s := &Server{
		cfg: &config.Config{DataDir: dir},
		tts: tts.New(upstream.URL, 5*time.Second),
	}

	first := httptest.NewRecorder()
	s.handleTTSSample(first, httptest.NewRequest(http.MethodGet, "/api/tts/sample?model=edge-tts&voice=voice-a", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("第一次试听状态 %d: %s", first.Code, first.Body.String())
	}
	if got := first.Header().Get("Content-Type"); got != "audio/wav" {
		t.Fatalf("Content-Type = %q", got)
	}
	if first.Body.Len() == 0 {
		t.Fatal("试听音频为空")
	}
	if calls != 1 {
		t.Fatalf("应调用上游 1 次，实际 %d", calls)
	}

	second := httptest.NewRecorder()
	s.handleTTSSample(second, httptest.NewRequest(http.MethodGet, "/api/tts/sample?model=edge-tts&voice=voice-a", nil))
	if second.Code != http.StatusOK {
		t.Fatalf("第二次试听状态 %d", second.Code)
	}
	if calls != 1 {
		t.Fatalf("第二次应命中缓存，上游调用次数 %d", calls)
	}
	if got := second.Header().Get("X-Sample-Cached"); got != "true" {
		t.Fatalf("X-Sample-Cached = %q", got)
	}

	path := filepath.Join(dir, "tts_samples", sampleFileName("edge-tts", "voice-a"))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("缓存文件不存在: %v", err)
	}
}

func TestTTSSampleRequiresModel(t *testing.T) {
	s := &Server{cfg: &config.Config{DataDir: t.TempDir()}, tts: tts.New("http://127.0.0.1:1", time.Second)}
	w := httptest.NewRecorder()
	s.handleTTSSample(w, httptest.NewRequest(http.MethodGet, "/api/tts/sample?voice=x", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 model 应返回 400，实际 %d", w.Code)
	}
}

func TestSampleFileNameIsSafe(t *testing.T) {
	got := sampleFileName("mlx-community/Qwen3-TTS", "zh-CN/Xiaoxiao")
	if got != "mlx-community_Qwen3-TTS__zh-CN_Xiaoxiao.wav" {
		t.Fatalf("unexpected filename: %s", got)
	}
}
