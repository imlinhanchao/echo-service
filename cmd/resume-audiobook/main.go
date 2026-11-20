// Command resume-audiobook re-runs the mastering stage of an interrupted
// audiobook job, reusing the segment WAVs already written by the pipeline.
//
// Usage:
//
//	go run ./cmd/resume-audiobook -job 11
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"path/filepath"

	"github.com/imlinhanchao/echo-service/internal/audiobook"
	"github.com/imlinhanchao/echo-service/internal/config"
	"github.com/imlinhanchao/echo-service/internal/store"
	"github.com/imlinhanchao/echo-service/internal/store/models"
	"github.com/imlinhanchao/echo-service/internal/store/repos"
	"github.com/imlinhanchao/echo-service/internal/tts"
)

type payload struct {
	BookID        uint64  `json:"book_id"`
	Model         string  `json:"model"`
	Voice         string  `json:"voice"`
	Speed         float64 `json:"speed"`
	Instruct      string  `json:"instruct"`
	FallbackModel string  `json:"fallback_model"`
	Chapters      []int   `json:"chapters"`
}

func main() {
	jobID := flag.Int64("job", 0, "要恢复的 audiobook 任务 ID")
	flag.Parse()
	if *jobID <= 0 {
		log.Fatal("用法: resume-audiobook -job <任务ID>")
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}
	db, err := store.Open(cfg)
	if err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	r := repos.New(db)
	ctx := context.Background()

	job, err := r.Jobs.ByIDAnyUser(ctx, uint64(*jobID))
	if err != nil {
		log.Fatalf("任务不存在: %v", err)
	}
	if job.Kind != models.JobKindAudiobook {
		log.Fatalf("任务 %d 不是有声书任务（kind=%s）", job.ID, job.Kind)
	}
	var p payload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		log.Fatalf("任务参数无效: %v", err)
	}
	book, err := r.Books.ByID(ctx, job.UserID, p.BookID)
	if err != nil {
		log.Fatalf("书籍不存在: %v", err)
	}

	var ab models.Audiobook
	if err := r.DB.WithContext(ctx).
		Where("user_id = ? AND book_id = ? AND status IN ?", job.UserID, p.BookID,
			[]string{models.JobRunning, models.JobFailed}).
		Order("id DESC").First(&ab).Error; err != nil {
		log.Fatalf("找不到对应的有声书记录: %v", err)
	}

	outputDir := filepath.Join(cfg.DataDir, "users", fmt.Sprint(job.UserID), "audio", fmt.Sprint(job.ID))
	log.Printf("恢复任务 #%d：书籍《%s》，模型 %s/%s，输出 %s", job.ID, book.Title, p.Model, p.Voice, outputDir)

	_ = r.DB.WithContext(ctx).Model(&models.Job{}).Where("id = ?", job.ID).
		Updates(map[string]any{"status": models.JobRunning, "error": ""}).Error
	_ = r.DB.WithContext(ctx).Model(&models.Audiobook{}).Where("id = ?", ab.ID).
		Updates(map[string]any{"status": models.JobRunning, "error": ""}).Error

	opts := audiobook.Options{
		Model:              p.Model,
		Voice:              p.Voice,
		LangCode:           "zh",
		Instruct:           p.Instruct,
		FallbackModel:      p.FallbackModel,
		Speed:              p.Speed,
		ChapterIndexes:     p.Chapters,
		MaxChars:           cfg.MaxJobChars,
		FFmpegPath:         cfg.FFmpegPath,
		SegmentConcurrency: 1,
		ReuseSegments:      true,
	}
	client := tts.New(cfg.TTSBaseURL, cfg.TTSTimeout)
	last := -1
	progress := func(pct uint8, msg string) {
		if int(pct) != last && (int(pct)%5 == 0 || pct == 100) {
			last = int(pct)
			log.Printf("  %3d%% %s", pct, msg)
		}
	}

	res, err := audiobook.Run(ctx, client, filepath.Join(cfg.DataDir, book.FilePath), outputDir, opts, progress, func() bool { return false })
	if err != nil {
		_ = r.Audiobooks.Finish(ctx, ab.ID, models.JobFailed, err.Error(), nil)
		_ = r.Jobs.Finish(ctx, job.ID, models.JobFailed, err.Error(), nil)
		log.Fatalf("恢复失败: %v", err)
	}

	rec := &models.Audiobook{
		Format:    res.Format,
		DurationS: res.DurationS,
		SizeBytes: uint64(res.SizeBytes),
		Segments:  uint32(res.Segments),
		Chars:     uint32(res.Chars),
	}
	rec.SRTPath, _ = filepath.Rel(cfg.DataDir, res.SRTPath)
	if res.AudioPath != "" {
		rec.AudioPath, _ = filepath.Rel(cfg.DataDir, res.AudioPath)
	}
	if res.ManifestPath != "" {
		rec.ManifestPath, _ = filepath.Rel(cfg.DataDir, res.ManifestPath)
	}
	if err := r.Audiobooks.Finish(ctx, ab.ID, "done", "", rec); err != nil {
		log.Fatalf("更新有声书记录失败: %v", err)
	}
	if err := r.Jobs.Finish(ctx, job.ID, models.JobDone, "", store.JSON(map[string]any{"audiobook_id": ab.ID})); err != nil {
		log.Fatalf("更新任务记录失败: %v", err)
	}
	for _, w := range res.Warnings {
		log.Printf("warning: %s", w)
	}
	log.Printf("完成：manifest=%s（%s，%.1f 秒，%d 段，%.1f MB）", rec.ManifestPath, res.Format, res.DurationS, res.Segments, float64(res.SizeBytes)/1e6)
}
