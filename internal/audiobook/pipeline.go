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
	ReuseSegments       bool                                         // true 时复用 segments/%05d.wav，只补缺失段（断点续转）
	ChapterChunkSeconds int                                          // 章节分块上限秒数，0 表示默认 1800（30 分钟）
	OnChunkReady        func(manifestPath string, manifest *Manifest) // 每完成一个可播放章节/30分钟分块即回调
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

	type blockRange struct {
		startSeg int
		endSeg   int
	}
	blockRanges := make([]blockRange, len(blocks))
	tasks := make([]segmentTask, 0, totalSegments)
	g := 0
	for ci, b := range blocks {
		if len(b.segments) == 0 {
			continue
		}
		start := g + 1
		for _, s := range b.segments {
			g++
			tasks = append(tasks, segmentTask{global: g, chapter: ci, text: s})
		}
		blockRanges[ci] = blockRange{startSeg: start, endSeg: g}
	}

	chunkSeconds := opts.ChapterChunkSeconds
	if chunkSeconds <= 0 {
		chunkSeconds = 1800
	}
	chaptersDir := filepath.Join(outputDir, "chapters")
	if err := os.RemoveAll(chaptersDir); err != nil {
		return nil, fmt.Errorf("清理章节目录: %w", err)
	}
	if err := os.MkdirAll(chaptersDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建章节目录: %w", err)
	}
	manifestPath := filepath.Join(outputDir, "manifest.json")

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

	// Dedicated sequential chunk emitter: as soon as the next chapter (or a
	// <=chunkSeconds slice of a long chapter) finishes synthesizing, master it
	// directly to chapters/%04d.{mp3|wav} + .srt and atomically update
	// manifest.json so listeners can start playback right after chunk #1.
	emitNotify := make(chan struct{}, 1)
	emitDone := make(chan struct{})
	triggerEmit := func() {
		select {
		case emitNotify <- struct{}{}:
		default:
		}
	}

	chunkFormat := "wav"
	if strings.TrimSpace(opts.FFmpegPath) != "" {
		chunkFormat = "mp3"
	}
	manifest := Manifest{
		Version:      1,
		Format:       chunkFormat,
		ChunkSeconds: chunkSeconds,
	}
	var emittedSizeBytes int64
	var emitErr error

	go func() {
		defer close(emitDone)
		blockIdx := 0
		segStart := 0
		partInBlock := 0
		timelineCursor := 0.0

		emitSlice := func(ci, sFrom, sTo, partNum int, isMultiPart bool) error {
			count := sTo - sFrom + 1
			slicePaths := make([]string, count)
			sliceCues := make([]srtCue, 0, count)
			localCursor := 0.0

			mu.Lock()
			for idx := 0; idx < count; idx++ {
				r := results[sFrom+idx]
				slicePaths[idx] = r.path
				if idx > 0 {
					localCursor += gapSeconds
				}
				dur := r.durationS
				sliceCues = append(sliceCues, srtCue{
					Start: localCursor,
					End:   localCursor + dur,
					Text:  r.text,
				})
				localCursor += dur
			}
			mu.Unlock()

			n := len(manifest.Chunks) + 1
			wavOut := filepath.Join(chaptersDir, fmt.Sprintf("%04d.wav", n))
			frames, err := writeMasteredWAVStream(wavOut, slicePaths, gapSeconds, targetSampleRate)
			if err != nil {
				return fmt.Errorf("写入第 %d 块音频: %w", n, err)
			}
			actualDur := float64(frames) / float64(targetSampleRate)

			audioName := fmt.Sprintf("%04d.wav", n)
			audioPath := wavOut
			if strings.TrimSpace(opts.FFmpegPath) != "" {
				mp3Name := fmt.Sprintf("%04d.mp3", n)
				mp3Out := filepath.Join(chaptersDir, mp3Name)
				if err := encodeMP3(opts.FFmpegPath, wavOut, mp3Out); err != nil {
					_ = os.Remove(mp3Out)
					addWarning(fmt.Sprintf("第 %d 块 MP3 编码失败，保留 WAV: %v", n, err))
					manifest.Format = "wav"
				} else {
					_ = os.Remove(wavOut)
					audioName = mp3Name
					audioPath = mp3Out
				}
			}

			srtName := fmt.Sprintf("%04d.srt", n)
			if err := writeSRTFile(filepath.Join(chaptersDir, srtName), sliceCues); err != nil {
				return fmt.Errorf("写入第 %d 块字幕: %w", n, err)
			}

			size := int64(0)
			if st, serr := os.Stat(audioPath); serr == nil {
				size = st.Size()
			}
			title := blocks[ci].chapter.Title
			if isMultiPart {
				title = fmt.Sprintf("%s（%d）", title, partNum)
			}
			startS := timelineCursor
			endS := startS + actualDur
			timelineCursor = endS

			manifest.Chunks = append(manifest.Chunks, ChunkInfo{
				N:            n,
				ChapterIndex: blocks[ci].chapter.Index,
				Title:        title,
				StartS:       math.Round(startS*1000) / 1000,
				EndS:         math.Round(endS*1000) / 1000,
				DurationS:    math.Round(actualDur*1000) / 1000,
				SizeBytes:    size,
				AudioFile:    audioName,
				SRTFile:      srtName,
			})
			manifest.DurationS = math.Round(timelineCursor*1000) / 1000
			emittedSizeBytes += size

			raw, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return fmt.Errorf("序列化 manifest.json: %w", err)
			}
			tmpManifest := manifestPath + ".tmp"
			if err := os.WriteFile(tmpManifest, raw, 0o644); err != nil {
				return fmt.Errorf("写入 manifest.json.tmp: %w", err)
			}
			if err := os.Rename(tmpManifest, manifestPath); err != nil {
				return fmt.Errorf("更新 manifest.json: %w", err)
			}
			if opts.OnChunkReady != nil {
				snap := manifest
				snap.Chunks = append([]ChunkInfo(nil), manifest.Chunks...)
				opts.OnChunkReady(manifestPath, &snap)
			}
			return nil
		}

		drainReady := func() {
			for blockIdx < len(blocks) {
				if workerCtx.Err() != nil {
					return
				}
				br := blockRanges[blockIdx]
				if br.startSeg == 0 {
					blockIdx++
					segStart = 0
					partInBlock = 0
					continue
				}
				if segStart == 0 {
					segStart = br.startSeg
				}
				// Scan contiguous ready segments starting at segStart.
				mu.Lock()
				accDur := 0.0
				readyEnd := segStart - 1
				crossedLimit := false
				for s := segStart; s <= br.endSeg; s++ {
					if results[s].path == "" {
						break
					}
					if s > segStart {
						accDur += gapSeconds
					}
					accDur += results[s].durationS
					readyEnd = s
					if accDur >= float64(chunkSeconds) {
						crossedLimit = true
						break
					}
				}
				mu.Unlock()

				if readyEnd < segStart {
					return
				}
				if readyEnd == br.endSeg || crossedLimit {
					partInBlock++
					isMultiPart := partInBlock > 1 || readyEnd < br.endSeg
					if err := emitSlice(blockIdx, segStart, readyEnd, partInBlock, isMultiPart); err != nil {
						emitErr = err
						setErr(err)
						return
					}
					if readyEnd == br.endSeg {
						blockIdx++
						segStart = 0
						partInBlock = 0
					} else {
						segStart = readyEnd + 1
					}
					continue
				}
				return
			}
		}

		for range emitNotify {
			drainReady()
		}
		drainReady()
	}()

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
			triggerEmit()
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
	close(emitNotify)
	<-emitDone

	mu.Lock()
	runErr := firstErr
	mu.Unlock()
	if runErr != nil {
		return nil, runErr
	}
	if emitErr != nil {
		return nil, emitErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Ensure every segment duration is populated for full-book subtitles & chapter marks.
	for i := 1; i <= totalSegments; i++ {
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
	actualDuration := manifest.DurationS
	if actualDuration <= 0 {
		actualDuration = cursor
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

	warnMu.Lock()
	finalWarnings := append([]string(nil), warnings...)
	warnMu.Unlock()

	return &Result{
		AudioPath:    "",
		SRTPath:      srtPath,
		ManifestPath: manifestPath,
		Format:       manifest.Format,
		DurationS:    actualDuration,
		SizeBytes:    emittedSizeBytes,
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
