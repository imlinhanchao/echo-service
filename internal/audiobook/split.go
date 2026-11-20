package audiobook

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ChunkInfo describes one playable chapter/chunk of an audiobook.
type ChunkInfo struct {
	N            int     `json:"n"`
	ChapterIndex int     `json:"chapter_index"`
	Title        string  `json:"title"`
	StartS       float64 `json:"start_s"`
	EndS         float64 `json:"end_s"`
	DurationS    float64 `json:"duration_s"`
	SizeBytes    int64   `json:"size_bytes"`
	AudioFile    string  `json:"audio_file"`
	SRTFile      string  `json:"srt_file"`
}

// Manifest lists the chunks of one audiobook (data/users/<uid>/audio/<id>/manifest.json).
type Manifest struct {
	Version      int         `json:"version"`
	Format       string      `json:"format"`
	DurationS    float64     `json:"duration_s"`
	ChunkSeconds int         `json:"chunk_seconds"`
	Chunks       []ChunkInfo `json:"chunks"`
}

type chapterRange struct {
	Index  int     `json:"index"`
	Title  string  `json:"title"`
	Chars  int     `json:"chars"`
	StartS float64 `json:"start_s"`
	EndS   float64 `json:"end_s"`
}

// SplitByChapters splits a finished audiobook into per-chapter chunks (a long
// chapter is cut into <=maxChunkSeconds pieces), writes per-chunk SRT files and
// a manifest.json next to them. The full-length audio file is kept as-is.
func SplitByChapters(audioPath, chaptersPath, srtPath, outDir, ffmpegPath string, maxChunkSeconds int) (*Manifest, error) {
	if maxChunkSeconds <= 0 {
		maxChunkSeconds = 1800
	}
	if strings.TrimSpace(audioPath) == "" {
		return nil, errors.New("缺少音频文件")
	}
	ext := strings.ToLower(filepath.Ext(audioPath))
	if ext != ".mp3" && ext != ".wav" {
		return nil, fmt.Errorf("不支持的分块音频格式: %s", ext)
	}

	var cj struct {
		Chapters []chapterRange `json:"chapters"`
	}
	data, err := os.ReadFile(chaptersPath)
	if err != nil {
		return nil, fmt.Errorf("读取 chapters.json: %w", err)
	}
	if err := json.Unmarshal(data, &cj); err != nil {
		return nil, fmt.Errorf("解析 chapters.json: %w", err)
	}
	if len(cj.Chapters) == 0 {
		return nil, errors.New("chapters.json 没有章节")
	}

	cues, err := readSRTCues(srtPath)
	if err != nil {
		return nil, err
	}

	chunkDir := filepath.Join(outDir, "chapters")
	if err := os.RemoveAll(chunkDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(chunkDir, 0o755); err != nil {
		return nil, err
	}

	manifest := &Manifest{Version: 1, Format: strings.TrimPrefix(ext, "."), ChunkSeconds: maxChunkSeconds}
	n := 0
	for _, chapter := range cj.Chapters {
		duration := chapter.EndS - chapter.StartS
		if duration <= 0.5 {
			continue
		}
		parts := int(math.Ceil(duration / float64(maxChunkSeconds)))
		if parts < 1 {
			parts = 1
		}
		partDuration := duration / float64(parts)
		for part := 0; part < parts; part++ {
			start := chapter.StartS + float64(part)*partDuration
			end := start + partDuration
			if part == parts-1 {
				end = chapter.EndS
			}
			n++
			title := chapter.Title
			if parts > 1 {
				title = fmt.Sprintf("%s（%d/%d）", chapter.Title, part+1, parts)
			}
			audioName := fmt.Sprintf("%04d%s", n, ext)
			audioOut := filepath.Join(chunkDir, audioName)
			if err := sliceAudio(audioPath, audioOut, start, end-start, ffmpegPath); err != nil {
				return nil, fmt.Errorf("切分第 %d 段 %q: %w", n, title, err)
			}
			srtName := fmt.Sprintf("%04d.srt", n)
			if err := writeChunkSRT(filepath.Join(chunkDir, srtName), cues, start, end); err != nil {
				return nil, err
			}
			size := int64(0)
			if st, err := os.Stat(audioOut); err == nil {
				size = st.Size()
			}
			manifest.Chunks = append(manifest.Chunks, ChunkInfo{
				N:            n,
				ChapterIndex: chapter.Index,
				Title:        title,
				StartS:       round3(start),
				EndS:         round3(end),
				DurationS:    round3(end - start),
				SizeBytes:    size,
				AudioFile:    audioName,
				SRTFile:      srtName,
			})
		}
	}
	if len(manifest.Chunks) == 0 {
		return nil, errors.New("没有可切分的章节")
	}
	manifest.DurationS = round3(manifest.Chunks[len(manifest.Chunks)-1].EndS)

	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), out, 0o644); err != nil {
		return nil, err
	}
	return manifest, nil
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// sliceAudio extracts [start, start+duration) into outPath.
func sliceAudio(src, dst string, start, duration float64, ffmpegPath string) error {
	switch strings.ToLower(filepath.Ext(src)) {
	case ".mp3":
		if strings.TrimSpace(ffmpegPath) == "" {
			return errors.New("切分 MP3 需要 ffmpeg")
		}
		cmd := exec.Command(ffmpegPath, "-y", "-loglevel", "error",
			"-ss", fmt.Sprintf("%.3f", start),
			"-i", src,
			"-t", fmt.Sprintf("%.3f", duration),
			"-c", "copy", dst)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case ".wav":
		return sliceWAV(src, dst, start, duration)
	default:
		return fmt.Errorf("不支持切分 %s", src)
	}
}

// sliceWAV copies a frame range out of a mono PCM16 WAV file without loading
// the whole file into memory.
func sliceWAV(src, dst string, start, duration float64) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	head := make([]byte, 64*1024)
	n, _ := f.Read(head)
	head = head[:n]
	h, err := parseWAVHeader(head)
	if err != nil {
		return err
	}
	if h.Bits != 16 || h.Channels != 1 {
		return fmt.Errorf("只支持单声道 PCM16 WAV 切分（当前 %d bit / %d 声道）", h.Bits, h.Channels)
	}
	rate := int(h.SampleRate)
	startFrame := int(math.Round(start * float64(rate)))
	durFrames := int(math.Round(duration * float64(rate)))
	if startFrame < 0 {
		startFrame = 0
	}
	// parseWAVHeader only saw the first 64 KiB, so its DataLen is capped; use
	// the real file size instead.
	st, err := f.Stat()
	if err != nil {
		return err
	}
	dataLen := int(st.Size()) - h.DataOffset
	if dataLen <= 0 {
		return errors.New("WAV 没有采样数据")
	}
	if startFrame*2 > dataLen {
		return errors.New("切分起点超出音频长度")
	}
	maxFrames := (dataLen - startFrame*2) / 2
	if durFrames > maxFrames {
		durFrames = maxFrames
	}
	if durFrames <= 0 {
		return errors.New("切分长度为 0")
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.Write(wavHeaderBytes(int64(durFrames), rate)); err != nil {
		return err
	}
	if _, err := f.Seek(int64(h.DataOffset+startFrame*2), 0); err != nil {
		return err
	}
	remaining := int64(durFrames * 2)
	buf := make([]byte, 256*1024)
	for remaining > 0 {
		chunk := int64(len(buf))
		if chunk > remaining {
			chunk = remaining
		}
		read, err := f.Read(buf[:chunk])
		if read > 0 {
			if _, werr := out.Write(buf[:read]); werr != nil {
				return werr
			}
			remaining -= int64(read)
		}
		if err != nil {
			break
		}
	}
	return nil
}

type srtCueTime struct {
	start float64
	end   float64
	text  string
}

var srtTimeRe = regexp.MustCompile(`^(\d{1,3}):(\d{2}):(\d{2})[,.](\d{1,3})\s*-->\s*(\d{1,3}):(\d{2}):(\d{2})[,.](\d{1,3})`)

func readSRTCues(path string) ([]srtCueTime, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取字幕: %w", err)
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), "\uFEFF"), "\r\n", "\n")
	var cues []srtCueTime
	for _, block := range strings.Split(text, "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		for i, line := range lines {
			m := srtTimeRe.FindStringSubmatch(strings.TrimSpace(line))
			if m == nil {
				continue
			}
			cueText := strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			if cueText == "" {
				break
			}
			cues = append(cues, srtCueTime{
				start: parseSRTSeconds(m[1], m[2], m[3], m[4]),
				end:   parseSRTSeconds(m[5], m[6], m[7], m[8]),
				text:  cueText,
			})
			break
		}
	}
	return cues, nil
}

func parseSRTSeconds(h, m, s, ms string) float64 {
	hh, _ := strconv.Atoi(h)
	mm, _ := strconv.Atoi(m)
	ss, _ := strconv.Atoi(s)
	msVal, _ := strconv.Atoi((ms + "000")[:3])
	return float64(hh*3600+mm*60+ss) + float64(msVal)/1000
}

// writeChunkSRT keeps the cues overlapping [start, end) and rebases them to 0.
func writeChunkSRT(path string, cues []srtCueTime, start, end float64) error {
	var b strings.Builder
	index := 0
	for _, cue := range cues {
		if cue.end <= start || cue.start >= end {
			continue
		}
		cueStart := math.Max(0, cue.start-start)
		cueEnd := math.Min(end, cue.end) - start
		if cueEnd <= cueStart {
			continue
		}
		index++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", index,
			formatSRTTime(cueStart), formatSRTTime(cueEnd), cue.text)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
