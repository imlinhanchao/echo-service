package audiobook

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitByChaptersWAV(t *testing.T) {
	dir := t.TempDir()
	rate := 24000
	total := 4 * rate // 4 秒
	samples := make([]float32, total)
	for i := range samples {
		samples[i] = 0.2 * float32(math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
	}
	audio := filepath.Join(dir, "final.wav")
	if err := writeWAVFile(audio, samples, rate); err != nil {
		t.Fatal(err)
	}

	chapters := map[string]any{"chapters": []map[string]any{
		{"index": 1, "title": "第一章", "chars": 10, "start_s": 0.0, "end_s": 2.0},
		{"index": 2, "title": "第二章", "chars": 10, "start_s": 2.0, "end_s": 4.0},
	}}
	chaptersPath := filepath.Join(dir, "chapters.json")
	raw, _ := json.Marshal(chapters)
	if err := os.WriteFile(chaptersPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	srtPath := filepath.Join(dir, "subtitles.srt")
	srt := "1\n00:00:00,000 --> 00:00:01,000\n甲\n\n2\n00:00:01,000 --> 00:00:02,000\n乙\n\n" +
		"3\n00:00:02,000 --> 00:00:03,000\n丙\n\n4\n00:00:03,000 --> 00:00:04,000\n丁\n\n"
	if err := os.WriteFile(srtPath, []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}

	// 单块上限 1 秒 -> 每章切成 2 块，共 4 块
	manifest, err := SplitByChapters(audio, chaptersPath, srtPath, dir, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Chunks) != 4 {
		t.Fatalf("chunks = %d, want 4", len(manifest.Chunks))
	}
	for i, chunk := range manifest.Chunks {
		if chunk.N != i+1 {
			t.Fatalf("chunk.N = %d, want %d", chunk.N, i+1)
		}
		if _, err := os.Stat(filepath.Join(dir, "chapters", chunk.AudioFile)); err != nil {
			t.Fatalf("chunk %d audio missing: %v", chunk.N, err)
		}
		srtData, err := os.ReadFile(filepath.Join(dir, "chapters", chunk.SRTFile))
		if err != nil {
			t.Fatalf("chunk %d srt missing: %v", chunk.N, err)
		}
		if !strings.Contains(string(srtData), "-->") {
			t.Fatalf("chunk %d srt empty", chunk.N)
		}
	}
	// 第二块的 SRT 应从 0 开始（时间轴按块重排）
	second, err := os.ReadFile(filepath.Join(dir, "chapters", manifest.Chunks[1].SRTFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(second), "00:00:00,000 -->") {
		t.Fatalf("第二块字幕没有从 0 开始: %s", second)
	}
	if manifest.DurationS != 4 {
		t.Fatalf("manifest duration = %v, want 4", manifest.DurationS)
	}
}
