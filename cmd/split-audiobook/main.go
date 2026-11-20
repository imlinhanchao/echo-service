// Command split-audiobook generates per-chapter chunks for an existing
// audiobook (backfill for books converted before chunking existed).
//
// Usage:
//
//	go run ./cmd/split-audiobook -id 7 [-chunk 1800]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/imlinhanchao/echo-service/internal/audiobook"
	"github.com/imlinhanchao/echo-service/internal/config"
	"github.com/imlinhanchao/echo-service/internal/store"
	"github.com/imlinhanchao/echo-service/internal/store/models"
	"github.com/imlinhanchao/echo-service/internal/store/repos"
)

func main() {
	id := flag.Int64("id", 0, "有声书 ID")
	chunkSeconds := flag.Int("chunk", 1800, "单块最大秒数")
	flag.Parse()
	if *id <= 0 {
		log.Fatal("用法: split-audiobook -id <有声书ID> [-chunk 1800]")
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
	var ab models.Audiobook
	if err := r.DB.WithContext(context.Background()).First(&ab, *id).Error; err != nil {
		log.Fatalf("有声书不存在: %v", err)
	}
	if ab.AudioPath == "" || ab.SRTPath == "" {
		log.Fatalf("有声书 %d 还没有音频/字幕文件", ab.ID)
	}
	dir := filepath.Dir(filepath.Join(cfg.DataDir, ab.AudioPath))
	audio := filepath.Join(cfg.DataDir, ab.AudioPath)
	chapters := filepath.Join(dir, "chapters.json")
	srt := filepath.Join(cfg.DataDir, ab.SRTPath)
	log.Printf("分块：有声书 #%d，音频 %s，单块上限 %d 秒", ab.ID, audio, *chunkSeconds)

	m, err := audiobook.SplitByChapters(audio, chapters, srt, dir, cfg.FFmpegPath, *chunkSeconds)
	if err != nil {
		log.Fatalf("分块失败: %v", err)
	}
	var totalSize int64
	for _, c := range m.Chunks {
		totalSize += c.SizeBytes
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	relManifest, _ := filepath.Rel(cfg.DataDir, manifestPath)
	// 整本音频不再保留：分块已覆盖播放/下载，避免双份磁盘。
	_ = os.Remove(audio)
	if err := r.DB.WithContext(context.Background()).Model(&models.Audiobook{}).Where("id = ?", ab.ID).
		Updates(map[string]any{
			"audio_path":    "",
			"manifest_path": relManifest,
			"size_bytes":    uint64(totalSize),
		}).Error; err != nil {
		log.Fatalf("更新数据库失败: %v", err)
	}
	log.Printf("完成：%d 块，总时长 %.1f 秒，清单 %s，已删除整本音频", len(m.Chunks), m.DurationS, manifestPath)
	for _, c := range m.Chunks {
		fmt.Printf("  %04d  %-40s %7.1f 分钟  %6.1f MB\n", c.N, c.Title, c.DurationS/60, float64(c.SizeBytes)/1e6)
	}
}
