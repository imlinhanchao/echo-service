package audiobook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/imlinhanchao/echo-service/internal/tts"
)

// Options controls one conversion run.
type Options struct {
	Model               string // TTS 模型 id，如 "edge-tts" / "qwen3-tts-1.7b"
	Voice               string
	LangCode            string // 默认 "zh"
	Instruct            string
	FallbackModel       string // 可为空
	Speed               float64
	ChapterIndexes      []int // nil/空 = 全部章节
	MaxChars            int   // 全书字符上限，0 = 不限制
	FFmpegPath          string
	SegmentConcurrency  int
	ReuseSegments       bool // true 时复用 segments/%05d.wav，只补缺失段（断点续转）
	ChapterChunkSeconds int  // 章节分块上限秒数，0 表示默认 1800（30 分钟）
}

// ChapterMark locates a chapter on the final audio timeline.
type ChapterMark struct {
	Title  string  `json:"title"`
	StartS float64 `json:"start_s"`
	EndS   float64 `json:"end_s"`
}

// Result describes a finished conversion.
type Result struct {
	AudioPath    string
	SRTPath      string
	ManifestPath string
	Format       string // "mp3" 或 "wav"
	DurationS    float64
	SizeBytes    int64
	Segments     int
	Chars        int
	Chapters     []ChapterMark
	Warnings     []string
}

// Progress receives a 0-100 percentage and a human readable message.
type Progress func(progress uint8, message string)

// CancelCheck is polled before every synthesis request.
type CancelCheck func() bool

// ErrCancelled is returned when CancelCheck reports cancellation.
var ErrCancelled = errors.New("conversion cancelled")

// retryBackoff is the wait before each of the three retries of a segment.
var retryBackoff = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}

type segmentTask struct {
	global  int
	chapter int
	text    string
}

type segmentResult struct {
	path      string
	durationS float64
	text      string
	chapter   int
}

type chapterBlock struct {
	chapter  Chapter
	segments []string
}

type segmentTime struct {
	start float64
	end   float64
}

type chaptersJSON struct {
	Chapters []chapterEntry `json:"chapters"`
}

type chapterEntry struct {
	Index  int     `json:"index"`
	Title  string  `json:"title"`
	Chars  int     `json:"chars"`
	StartS float64 `json:"start_s"`
	EndS   float64 `json:"end_s"`
}

// Run executes the whole EPUB -> TTS -> mastered audio pipeline.
func Run(ctx context.Context, ttsClient *tts.Client, epubPath, outputDir string, opts Options, progress Progress, cancelled CancelCheck) (*Result, error) {
	if ttsClient == nil {
		return nil, errors.New("TTS 客户端为空")
	}
	if cancelled == nil {
		cancelled = func() bool { return false }
	}
	if progress == nil {
		progress = func(uint8, string) {}
	}
	if strings.TrimSpace(opts.LangCode) == "" {
		opts.LangCode = "zh"
	}
	if opts.Speed <= 0 {
		opts.Speed = 1
	}
	if opts.SegmentConcurrency < 1 {
		opts.SegmentConcurrency = 1
	}

	chapters, err := ParseEPUB(epubPath)
	if err != nil {
		return nil, err
	}
	selected, err := selectChapters(chapters, opts.ChapterIndexes)
	if err != nil {
		return nil, err
	}

	var blocks []chapterBlock
	totalSegments := 0
	totalChars := 0
	for _, ch := range selected {
		segs := SegmentText(ch.Text, defaultSegmentRunes)
		blocks = append(blocks, chapterBlock{chapter: ch, segments: segs})
		totalSegments += len(segs)
		totalChars += ch.Chars
	}
	if opts.MaxChars > 0 && totalChars > opts.MaxChars {
		return nil, fmt.Errorf("全书字符数 %d 超过单任务上限 %d，请减少章节或调大 MAX_JOB_CHARS", totalChars, opts.MaxChars)
	}
	if totalSegments == 0 || totalChars == 0 {
		return nil, errors.New("没有可合成的文本")
	}

	if err := os.MkdirAll(filepath.Join(outputDir, "segments"), 0o755); err != nil {
		return nil, fmt.Errorf("创建输出目录: %w", err)
	}

	var warnings []string
	var warnMu sync.Mutex
	addWarning := func(msg string) {
		warnMu.Lock()
		warnings = append(warnings, msg)
		warnMu.Unlock()
	}

	if cancelled() {
		return nil, ErrCancelled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := ttsClient.Warmup(ctx, opts.Model); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		addWarning(fmt.Sprintf("模型预热失败: %v", err))
	}

	tasks := make([]segmentTask, 0, totalSegments)
	g := 0
	for ci, b := range blocks {
		for _, s := range b.segments {
			g++
			tasks = append(tasks, segmentTask{global: g, chapter: ci, text: s})
		}
	}

	results := make([]segmentResult, totalSegments+1)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	taskCh := make(chan segmentTask, len(tasks))
	for _, t := range tasks {
		taskCh <- t
	}
	close(taskCh)

	var mu sync.Mutex
	var firstErr error
	completed := 0
	var fallbackMu sync.Mutex
	fallbackActive := false
	fallbackWarned := false

	setErr := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		mu.Unlock()
	}

	worker := func() {
		for t := range taskCh {
			if workerCtx.Err() != nil {
				return
			}
			if cancelled() {
				setErr(ErrCancelled)
				return
			}
			model := opts.Model
			fallbackMu.Lock()
			if fallbackActive {
				model = opts.FallbackModel
			}
			fallbackMu.Unlock()

			path := filepath.Join(outputDir, "segments", fmt.Sprintf("%05d.wav", t.global))
			var res tts.SpeechResult
			reused := false
			if opts.ReuseSegments {
				if data, rerr := os.ReadFile(path); rerr == nil && len(data) > 44 {
					res.Audio = data
					reused = true
					if d, derr := wavDurationSeconds(data); derr == nil {
						res.DurationS = d
					}
				}
			}

			if !reused {
				var err error
				res, err = synthesizeWithRetry(workerCtx, ttsClient, opts, model, t.text)
				if err != nil {
					if workerCtx.Err() != nil {
						if ctx.Err() != nil {
							setErr(ctx.Err())
						} else {
							setErr(workerCtx.Err())
						}
						return
					}
					if opts.FallbackModel != "" && model != opts.FallbackModel {
						fallbackMu.Lock()
						switchNow := !fallbackActive
						warnNow := false
						if switchNow {
							fallbackActive = true
							if !fallbackWarned {
								fallbackWarned = true
								warnNow = true
							}
						}
						fallbackMu.Unlock()
						if warnNow {
							addWarning(fmt.Sprintf("模型 %s 合成失败，剩余段落改用 %s", opts.Model, opts.FallbackModel))
						}
						res, err = synthesizeWithRetry(workerCtx, ttsClient, opts, opts.FallbackModel, t.text)
						if err != nil {
							if workerCtx.Err() != nil {
								if ctx.Err() != nil {
									setErr(ctx.Err())
								} else {
									setErr(workerCtx.Err())
								}
								return
							}
							setErr(fmt.Errorf("合成第 %d 段失败: %w", t.global, err))
							return
						}
					} else {
						setErr(fmt.Errorf("合成第 %d 段失败: %w", t.global, err))
						return
					}
				}

				if err := os.WriteFile(path, res.Audio, 0o644); err != nil {
					setErr(fmt.Errorf("写入第 %d 段音频: %w", t.global, err))
					return
				}
			}
			dur := res.DurationS
			if dur <= 0 {
				if d, derr := wavDurationSeconds(res.Audio); derr == nil {
					dur = d
				}
			}

			mu.Lock()
			results[t.global] = segmentResult{
				path:      path,
				durationS: dur,
				text:      t.text,
				chapter:   t.chapter,
			}
			completed++
			pct := uint8(completed * 100 / totalSegments)
			progress(pct, fmt.Sprintf("已合成 %d/%d 段", completed, totalSegments))
			mu.Unlock()
		}
	}

	workers := opts.SegmentConcurrency
	if workers > len(tasks) {
		workers = len(tasks)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker()
		}()
	}
	wg.Wait()

	mu.Lock()
	runErr := firstErr
	mu.Unlock()
	if runErr != nil {
		return nil, runErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Collect segment paths and make sure every duration is known for the SRT.
	segPaths := make([]string, totalSegments)
	for i := 1; i <= totalSegments; i++ {
		segPaths[i-1] = results[i].path
		if results[i].durationS <= 0 {
			if data, err := os.ReadFile(results[i].path); err == nil {
				if d, derr := wavDurationSeconds(data); derr == nil {
					results[i].durationS = d
				}
			}
		}
	}

	times := make([]segmentTime, totalSegments+1)
	cursor := 0.0
	for i := 1; i <= totalSegments; i++ {
		if i > 1 {
			cursor += gapSeconds
		}
		dur := results[i].durationS
		times[i] = segmentTime{start: cursor, end: cursor + dur}
		cursor += dur
	}
	computedTotal := cursor

	finalWAV := filepath.Join(outputDir, "final.wav")
	totalFrames, err := writeMasteredWAVStream(finalWAV, segPaths, gapSeconds, targetSampleRate)
	if err != nil {
		return nil, fmt.Errorf("写入 final.wav: %w", err)
	}
	actualDuration := float64(totalFrames) / float64(targetSampleRate)
	if math.Abs(actualDuration-computedTotal) > 0.1 {
		addWarning(fmt.Sprintf("字幕时间轴总长 %.3fs 与音频实际时长 %.3fs 不一致", computedTotal, actualDuration))
	}

	srtPath := filepath.Join(outputDir, "subtitles.srt")
	cues := make([]srtCue, 0, totalSegments)
	for i := 1; i <= totalSegments; i++ {
		cues = append(cues, srtCue{
			Start: times[i].start,
			End:   times[i].end,
			Text:  results[i].text,
		})
	}
	if err := writeSRTFile(srtPath, cues); err != nil {
		return nil, fmt.Errorf("写入 subtitles.srt: %w", err)
	}

	marks := make([]ChapterMark, 0, len(blocks))
	entries := make([]chapterEntry, 0, len(blocks))
	g = 1
	for _, b := range blocks {
		mark := ChapterMark{Title: b.chapter.Title}
		if len(b.segments) > 0 {
			mark.StartS = times[g].start
			mark.EndS = times[g+len(b.segments)-1].end
			g += len(b.segments)
		} else if g > 1 {
			mark.StartS = times[g-1].end
			mark.EndS = mark.StartS
		}
		marks = append(marks, mark)
		entries = append(entries, chapterEntry{
			Index:  b.chapter.Index,
			Title:  b.chapter.Title,
			Chars:  b.chapter.Chars,
			StartS: mark.StartS,
			EndS:   mark.EndS,
		})
	}
	chapterData, err := json.MarshalIndent(chaptersJSON{Chapters: entries}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("生成 chapters.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "chapters.json"), chapterData, 0o644); err != nil {
		return nil, fmt.Errorf("写入 chapters.json: %w", err)
	}

	audioPath := finalWAV
	format := "wav"
	if strings.TrimSpace(opts.FFmpegPath) != "" {
		mp3Path := filepath.Join(outputDir, "final.mp3")
		if err := encodeMP3(opts.FFmpegPath, finalWAV, mp3Path); err != nil {
			_ = os.Remove(mp3Path)
			addWarning(fmt.Sprintf("MP3 编码失败，保留 WAV: %v", err))
		} else {
			_ = os.Remove(finalWAV)
			audioPath = mp3Path
			format = "mp3"
		}
	}

	// Split into per-chapter chunks (<=30 min each) so apps can stream/seek and
	// resume downloads without pulling a multi-GB file. The full file is kept.
	chunkSeconds := opts.ChapterChunkSeconds
	if chunkSeconds <= 0 {
		chunkSeconds = 1800
	}
	manifestPath := ""
	sizeBytes := int64(0)
	if manifest, serr := SplitByChapters(audioPath, filepath.Join(outputDir, "chapters.json"), srtPath, outputDir, opts.FFmpegPath, chunkSeconds); serr != nil {
		addWarning(fmt.Sprintf("章节分块失败: %v", serr))
		stat, err := os.Stat(audioPath)
		if err != nil {
			return nil, fmt.Errorf("读取最终音频信息: %w", err)
		}
		sizeBytes = stat.Size()
	} else {
		// The full-length file is redundant once per-chapter chunks exist;
		// drop it so a 15-hour book does not occupy twice the disk.
		manifestPath = filepath.Join(outputDir, "manifest.json")
		for _, c := range manifest.Chunks {
			sizeBytes += c.SizeBytes
		}
		_ = os.Remove(audioPath)
		audioPath = ""
	}

	warnMu.Lock()
	finalWarnings := append([]string(nil), warnings...)
	warnMu.Unlock()

	return &Result{
		AudioPath:    audioPath,
		SRTPath:      srtPath,
		ManifestPath: manifestPath,
		Format:       format,
		DurationS:    actualDuration,
		SizeBytes:    sizeBytes,
		Segments:     totalSegments,
		Chars:        totalChars,
		Chapters:     marks,
		Warnings:     finalWarnings,
	}, nil
}

func selectChapters(chapters []Chapter, indexes []int) ([]Chapter, error) {
	if len(indexes) == 0 {
		return chapters, nil
	}
	wanted := make(map[int]bool, len(indexes))
	for _, idx := range indexes {
		wanted[idx] = true
	}
	var out []Chapter
	for _, ch := range chapters {
		if wanted[ch.Index] {
			out = append(out, ch)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("所选章节不存在")
	}
	return out, nil
}

func synthesizeWithRetry(ctx context.Context, client *tts.Client, opts Options, model, text string) (tts.SpeechResult, error) {
	var lastErr error
	for attempt := 0; attempt <= len(retryBackoff); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return tts.SpeechResult{}, ctx.Err()
			case <-time.After(retryBackoff[attempt-1]):
			}
		}
		res, err := client.Speech(ctx, tts.SpeechRequest{
			Model:    model,
			Input:    text,
			Voice:    opts.Voice,
			LangCode: opts.LangCode,
			Instruct: opts.Instruct,
			Format:   "wav",
			Speed:    opts.Speed,
		})
		if err == nil && len(res.Audio) > 0 {
			return res, nil
		}
		if err == nil {
			err = errors.New("TTS 返回空音频")
		}
		lastErr = err
		if !tts.Retryable(err) {
			break
		}
		if ctx.Err() != nil {
			return tts.SpeechResult{}, ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = errors.New("TTS 合成失败")
	}
	return tts.SpeechResult{}, lastErr
}

func encodeMP3(ffmpegPath, wavPath, mp3Path string) error {
	resolved, err := exec.LookPath(ffmpegPath)
	if err != nil {
		return fmt.Errorf("找不到 ffmpeg: %w", err)
	}
	cmd := exec.Command(resolved,
		"-y", "-loglevel", "error",
		"-i", wavPath,
		"-codec:a", "libmp3lame", "-b:a", "128k",
		mp3Path,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg 编码失败: %v: %s", err, strings.TrimSpace(string(output)))
	}
	stat, err := os.Stat(mp3Path)
	if err != nil {
		return fmt.Errorf("ffmpeg 未生成 MP3: %w", err)
	}
	if stat.Size() == 0 {
		return errors.New("ffmpeg 生成的 MP3 为空")
	}
	return nil
}
