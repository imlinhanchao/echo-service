// Package tts is a client for the Mac aggregation service (Qwen3-TTS / Kokoro
// / Edge TTS behind one OpenAI-compatible endpoint).
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Model struct {
	ID       string   `json:"id"`
	Repo     string   `json:"repo"`
	Default  bool     `json:"default"`
	Provider string   `json:"provider"`
	Voices   []string `json:"voices"`
}

type Voice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Health struct {
	Status   string   `json:"status"`
	Version  string   `json:"version"`
	Models   []string `json:"models"`
	Loaded   []string `json:"loaded"`
	Busy     bool     `json:"busy"`
	Waiting  int      `json:"waiting"`
	LastRTF  *float64 `json:"last_rtf"`
	MaxChars int      `json:"max_chars"`
}

// SpeechRequest is one synthesis request.
type SpeechRequest struct {
	Model    string
	Input    string
	Voice    string
	LangCode string
	Instruct string
	Format   string
	Speed    float64
}

// SpeechResult carries the audio bytes plus the service's response headers.
type SpeechResult struct {
	Audio      []byte
	Format     string
	DurationS  float64
	SampleRate float64
	RTF        float64
	QueueS     float64
	SynthS     float64
	Model      string
	Voice      string
	Lang       string
	FormatOut  string
}

// Error is a non-2xx response from the TTS service.
type Error struct {
	StatusCode int
	Message    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("TTS 服务返回 %d: %s", e.StatusCode, e.Message)
}

// Retryable reports whether the error is worth retrying / falling back.
func Retryable(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.StatusCode >= 500 || e.StatusCode == http.StatusTooManyRequests
	}
	return true // network errors are retryable
}

type Client struct {
	base string
	http *http.Client

	mu       sync.Mutex
	models   []Model
	modelsAt time.Time
}

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		http: &http.Client{Timeout: timeout},
	}
}

func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	if err := c.getJSON(ctx, "/health", &h); err != nil {
		return h, err
	}
	return h, nil
}

// Models returns the model list, cached for five minutes.
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	c.mu.Lock()
	if len(c.models) > 0 && time.Since(c.modelsAt) < 5*time.Minute {
		out := append([]Model(nil), c.models...)
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()

	var payload struct {
		Data []Model `json:"data"`
	}
	if err := c.getJSON(ctx, "/api/models", &payload); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.models = payload.Data
	c.modelsAt = time.Now()
	c.mu.Unlock()
	return append([]Model(nil), payload.Data...), nil
}

func (c *Client) Voices(ctx context.Context, model string) ([]Voice, error) {
	var payload struct {
		Data []Voice `json:"data"`
	}
	path := "/api/voices?model=" + urlQueryEscape(model)
	if err := c.getJSON(ctx, path, &payload); err != nil {
		return nil, err
	}
	return payload.Data, nil
}

func (c *Client) Warmup(ctx context.Context, model string) error {
	body, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/warmup", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("TTS 服务不可用: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &Error{StatusCode: resp.StatusCode, Message: readError(resp)}
	}
	return nil
}

// Speech synthesizes one segment and returns audio plus timing metadata.
func (c *Client) Speech(ctx context.Context, r SpeechRequest) (SpeechResult, error) {
	format := r.Format
	if format == "" {
		format = "wav"
	}
	payload := map[string]any{
		"model":           r.Model,
		"input":           r.Input,
		"voice":           r.Voice,
		"response_format": format,
	}
	if r.LangCode != "" {
		payload["lang_code"] = r.LangCode
	}
	if r.Instruct != "" {
		payload["instruct"] = r.Instruct
	}
	if r.Speed > 0 && r.Speed != 1.0 {
		payload["speed"] = r.Speed
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return SpeechResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/audio/speech", bytes.NewReader(body))
	if err != nil {
		return SpeechResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return SpeechResult{}, fmt.Errorf("TTS 服务不可用: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return SpeechResult{}, &Error{StatusCode: resp.StatusCode, Message: readError(resp)}
	}
	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return SpeechResult{}, fmt.Errorf("读取 TTS 音频: %w", err)
	}
	h := resp.Header
	return SpeechResult{
		Audio:      audio,
		Format:     format,
		DurationS:  headerFloat(h, "X-Audio-Seconds"),
		SampleRate: headerFloat(h, "X-Sample-Rate"),
		RTF:        headerFloat(h, "X-RTF"),
		QueueS:     headerFloat(h, "X-Queue-Seconds"),
		SynthS:     headerFloat(h, "X-Synth-Seconds"),
		Model:      h.Get("X-Model"),
		Voice:      h.Get("X-Voice"),
		Lang:       h.Get("X-Lang"),
		FormatOut:  h.Get("X-Format"),
	}, nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("TTS 服务不可用: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &Error{StatusCode: resp.StatusCode, Message: readError(resp)}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func readError(resp *http.Response) string {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	var payload struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(data, &payload) == nil && payload.Detail != "" {
		return payload.Detail
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return resp.Status
	}
	return s
}

func headerFloat(h http.Header, key string) float64 {
	v := strings.TrimSpace(h.Get(key))
	if v == "" {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return f
}

// urlQueryEscape escapes a query value without importing net/url twice.
func urlQueryEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "+", "%2B"), " ", "%20")
}
