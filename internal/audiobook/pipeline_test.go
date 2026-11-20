package audiobook

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imlinhanchao/echo-service/internal/tts"
)

// fakeTTSServer implements the warmup and speech endpoints the tts client
// uses. When failModel is non-empty, requests for that model return HTTP 500.
func fakeTTSServer(t *testing.T, failModel string) *httptest.Server {
	t.Helper()
	var speechCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/warmup":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/audio/speech":
			speechCalls.Add(1)
			var req struct {
				Model string `json:"model"`
				Input string `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if failModel != "" && req.Model == failModel {
				http.Error(w, "model exploded", http.StatusInternalServerError)
				return
			}
			dur := 0.2 + float64(len([]rune(req.Input)))*0.005
			if dur > 0.8 {
				dur = 0.8
			}
			audio := makeSineWAV(24000, dur, 0.2, 1)
			w.Header().Set("Content-Type", "audio/wav")
			w.Header().Set("X-Audio-Seconds", strconv.FormatFloat(dur, 'f', 6, 64))
			w.Header().Set("X-Sample-Rate", "24000")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(audio)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func countSRTCues(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read srt: %v", err)
	}
	return strings.Count(string(data), "-->")
}

func TestRunPipeline(t *testing.T) {
	srv := fakeTTSServer(t, "")
	client := tts.New(srv.URL, 5*time.Second)
	epubPath := writeTestEPUB(t)
	outDir := t.TempDir()

	var progressCalls atomic.Int64
	var lastPct atomic.Int64
	res, err := Run(context.Background(), client, epubPath, outDir, Options{
		Model:              "edge-tts",
		Voice:              "zh-CN-XiaoxiaoNeural",
		LangCode:           "zh",
		SegmentConcurrency: 2,
		MaxChars:           100000,
	}, func(p uint8, msg string) {
		if p > 100 {
			t.Errorf("progress = %d, want <= 100", p)
		}
		if !strings.Contains(msg, "已合成") {
			t.Errorf("progress message = %q", msg)
		}
		progressCalls.Add(1)
		lastPct.Store(int64(p))
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res == nil {
		t.Fatal("Run returned nil result")
	}
	if res.Format != "wav" {
		t.Errorf("format = %q, want wav", res.Format)
	}
	if res.Segments <= 0 {
		t.Errorf("segments = %d, want > 0", res.Segments)
	}
	if res.Chars <= 0 {
		t.Errorf("chars = %d, want > 0", res.Chars)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", res.Warnings)
	}
	if got := progressCalls.Load(); got != int64(res.Segments) {
		t.Errorf("progress calls = %d, want %d", got, res.Segments)
	}
	if got := lastPct.Load(); got != 100 {
		t.Errorf("last progress = %d, want 100", got)
	}

	// 分块成功后整本音频会被删除，只保留 chapters/ 与 manifest.json。
	if res.AudioPath != "" {
		t.Errorf("audio path = %q, want empty (full file pruned)", res.AudioPath)
	}
	manifestPath := filepath.Join(outDir, "manifest.json")
	if res.ManifestPath != manifestPath {
		t.Errorf("manifest path = %q, want %q", res.ManifestPath, manifestPath)
	}
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(manifest.Chunks) == 0 {
		t.Fatal("manifest has no chunks")
	}
	if res.SizeBytes <= 0 {
		t.Errorf("size = %d, want > 0", res.SizeBytes)
	}
	if math.Abs(manifest.DurationS-res.DurationS) > 0.5 {
		t.Errorf("manifest duration = %.3f, result duration = %.3f", manifest.DurationS, res.DurationS)
	}
	if _, err := os.Stat(filepath.Join(outDir, "final.wav")); !os.IsNotExist(err) {
		t.Errorf("final.wav should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "chapters", manifest.Chunks[0].AudioFile)); err != nil {
		t.Errorf("chunk audio missing: %v", err)
	}
	if res.DurationS <= 0 {
		t.Errorf("duration = %f, want > 0", res.DurationS)
	}

	srtPath := filepath.Join(outDir, "subtitles.srt")
	if res.SRTPath != srtPath {
		t.Errorf("srt path = %q, want %q", res.SRTPath, srtPath)
	}
	if n := countSRTCues(t, srtPath); n != res.Segments {
		t.Errorf("SRT cues = %d, want %d", n, res.Segments)
	}

	if len(res.Chapters) != 2 {
		t.Fatalf("chapter marks = %d, want 2", len(res.Chapters))
	}
	if res.Chapters[0].Title != "第一章 开端" {
		t.Errorf("chapter 1 title = %q", res.Chapters[0].Title)
	}
	if res.Chapters[0].StartS != 0 {
		t.Errorf("chapter 1 start = %f, want 0", res.Chapters[0].StartS)
	}
	if res.Chapters[1].StartS <= res.Chapters[0].EndS {
		t.Errorf("chapter 2 start %f should follow chapter 1 end %f (gap)", res.Chapters[1].StartS, res.Chapters[0].EndS)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "chapters.json"))
	if err != nil {
		t.Fatalf("read chapters.json: %v", err)
	}
	var doc chaptersJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse chapters.json: %v", err)
	}
	if len(doc.Chapters) != 2 {
		t.Fatalf("chapters.json entries = %d, want 2", len(doc.Chapters))
	}
	if doc.Chapters[0].Index != 1 || doc.Chapters[1].Index != 2 {
		t.Errorf("chapter indexes = %d,%d", doc.Chapters[0].Index, doc.Chapters[1].Index)
	}
	for _, entry := range doc.Chapters {
		if entry.Chars < 30 {
			t.Errorf("chapter %d chars = %d, want >= 30", entry.Index, entry.Chars)
		}
	}
}

func TestRunCancelled(t *testing.T) {
	srv := fakeTTSServer(t, "")
	client := tts.New(srv.URL, 5*time.Second)
	res, err := Run(context.Background(), client, writeTestEPUB(t), t.TempDir(), Options{
		Model: "edge-tts",
	}, nil, func() bool { return true })
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
}

func TestRunMaxChars(t *testing.T) {
	srv := fakeTTSServer(t, "")
	client := tts.New(srv.URL, 5*time.Second)
	_, err := Run(context.Background(), client, writeTestEPUB(t), t.TempDir(), Options{
		Model:    "edge-tts",
		MaxChars: 10,
	}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("err = %v, want max chars error", err)
	}
}

func TestRunFallbackModel(t *testing.T) {
	oldBackoff := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { retryBackoff = oldBackoff }()

	srv := fakeTTSServer(t, "bad-model")
	client := tts.New(srv.URL, 5*time.Second)
	res, err := Run(context.Background(), client, writeTestEPUB(t), t.TempDir(), Options{
		Model:              "bad-model",
		FallbackModel:      "good-model",
		SegmentConcurrency: 1,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Segments <= 0 {
		t.Fatalf("segments = %d", res.Segments)
	}
	found := false
	for _, warning := range res.Warnings {
		if strings.Contains(warning, "good-model") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want fallback warning", res.Warnings)
	}
	if res.ManifestPath == "" {
		t.Error("manifest path is empty")
	} else if _, err := os.Stat(res.ManifestPath); err != nil {
		t.Errorf("manifest missing: %v", err)
	}
}
