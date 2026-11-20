// Command prune-full-audio removes the redundant full-length audio files of
// audiobooks that already have per-chapter chunks, and updates the database.
//
// Usage:
//
//	go run ./cmd/prune-full-audio [-dry-run]
package main

import (
	"context"
	"encoding/json"
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
	dryRun := flag.Bool("dry-run", false, "只打印，不删除")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}
	db, err := store.Open(cfg)
	if err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	r := repos.New(db)
	var items []models.Audiobook
	if err := r.DB.WithContext(context.Background()).Find(&items).Error; err != nil {
		log.Fatalf("读取有声书失败: %v", err)
	}
	removed, freed := 0, int64(0)
	for _, ab := range items {
		dir := ""
		if ab.ManifestPath != "" {
			dir = filepath.Dir(filepath.Join(cfg.DataDir, ab.ManifestPath))
		} else if ab.AudioPath != "" {
			dir = filepath.Dir(filepath.Join(cfg.DataDir, ab.AudioPath))
		}
		if dir == "" {
			continue
		}
		manifestPath := filepath.Join(dir, "manifest.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var manifest audiobook.Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			log.Printf("#%d manifest 损坏，跳过: %v", ab.ID, err)
			continue
		}
		full := ""
		for _, name := range []string{"final.mp3", "final.wav"} {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil {
				full = p
				freed += st.Size()
				break
			}
		}
		if full == "" {
			continue
		}
		relManifest, _ := filepath.Rel(cfg.DataDir, manifestPath)
		var total int64
		for _, c := range manifest.Chunks {
			total += c.SizeBytes
		}
		if *dryRun {
			log.Printf("#%d 将删除 %s（%d 块）", ab.ID, full, len(manifest.Chunks))
			continue
		}
		if err := os.Remove(full); err != nil {
			log.Printf("#%d 删除失败: %v", ab.ID, err)
			continue
		}
		if err := r.DB.WithContext(context.Background()).Model(&models.Audiobook{}).Where("id = ?", ab.ID).
			Updates(map[string]any{"audio_path": "", "manifest_path": relManifest, "size_bytes": uint64(total)}).Error; err != nil {
			log.Printf("#%d 更新数据库失败: %v", ab.ID, err)
			continue
		}
		removed++
		log.Printf("#%d 已删除整本音频，保留 %d 块", ab.ID, len(manifest.Chunks))
	}
	fmt.Printf("完成：删除 %d 个整本音频，释放 %.1f MB\n", removed, float64(freed)/1e6)
}
