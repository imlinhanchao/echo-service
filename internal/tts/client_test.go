package tts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestClient(t *testing.T) (*Client, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok","version":"1.2","models":["edge-tts"],"loaded":[],"busy":false,"waiting":0,"last_rtf":1.5,"max_chars":1000}`)
	})
	mux.HandleFunc("GET /api/models", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"edge-tts","repo":"edge-tts","default":false,"provider":"edge","voices":["zh-CN-XiaoxiaoNeural"]},{"id":"qwen3-tts-1.7b","repo":"mlx-community/Qwen3-TTS","default":true,"voices":["Uncle_Fu"]}]}`)
	})
	mux.HandleFunc("GET /api/voices", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("model") != "edge-tts" {
			http.Error(w, "bad model", 400)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"zh-CN-XiaoxiaoNeural","name":"Xiaoxiao","description":"晓晓"}]}`)
	})
	mux.HandleFunc("POST /api/warmup", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"model":"qwen3-tts-1.7b","load_seconds":3.5}`)
	})
	mux.HandleFunc("POST /v1/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad content type", 400)
			return
		}
		if r.URL.Query().Get("fail") == "1" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"detail":"Edge TTS 失败: network"}`)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("X-Audio-Seconds", "4.80")
		w.Header().Set("X-Sample-Rate", "24000")
		w.Header().Set("X-RTF", "2.33")
		w.Header().Set("X-Queue-Seconds", "0.00")
		w.Header().Set("X-Synth-Seconds", "2.06")
		w.Header().Set("X-Model", "edge-tts")
		w.Header().Set("X-Voice", "zh-CN-XiaoxiaoNeural")
		w.Header().Set("X-Lang", "zh")
		w.Header().Set("X-Format", "wav")
		_, _ = w.Write([]byte("RIFF....WAVEfake"))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return New(server.URL, 5*time.Second), server
}

func TestHealthAndModels(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()
	h, err := c.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != "ok" || h.Version != "1.2" {
		t.Fatalf("unexpected health: %+v", h)
	}
	models, err := c.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "edge-tts" {
		t.Fatalf("unexpected models: %+v", models)
	}
	// cache: second call must not fail even if the server is closed.
	voices, err := c.Voices(ctx, "edge-tts")
	if err != nil {
		t.Fatal(err)
	}
	if len(voices) != 1 || voices[0].ID != "zh-CN-XiaoxiaoNeural" {
		t.Fatalf("unexpected voices: %+v", voices)
	}
}

func TestSpeech(t *testing.T) {
	c, _ := newTestClient(t)
	res, err := c.Speech(context.Background(), SpeechRequest{
		Model: "edge-tts", Input: "你好", Voice: "zh-CN-XiaoxiaoNeural",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Audio) != "RIFF....WAVEfake" {
		t.Fatalf("unexpected audio: %q", res.Audio)
	}
	if res.DurationS != 4.8 || res.SampleRate != 24000 || res.RTF != 2.33 {
		t.Fatalf("unexpected headers: %+v", res)
	}
	if res.Model != "edge-tts" || res.Voice != "zh-CN-XiaoxiaoNeural" {
		t.Fatalf("unexpected model/voice: %+v", res)
	}
}

func TestSpeechRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"detail":"Edge TTS 失败: network"}`)
	}))
	defer server.Close()
	bad := New(server.URL, 5*time.Second)
	_, err := bad.Speech(context.Background(), SpeechRequest{Model: "edge-tts", Input: "x"})
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *Error, got %T %v", err, err)
	}
	if apiErr.StatusCode != http.StatusBadGateway || !Retryable(err) {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
}

func TestWarmup(t *testing.T) {
	c, _ := newTestClient(t)
	if err := c.Warmup(context.Background(), "qwen3-tts-1.7b"); err != nil {
		t.Fatal(err)
	}
}
