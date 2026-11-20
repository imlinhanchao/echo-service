package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/imlinhanchao/echo-service/internal/api"
	"github.com/imlinhanchao/echo-service/internal/config"
	"github.com/imlinhanchao/echo-service/internal/jobs"
	"github.com/imlinhanchao/echo-service/internal/providers"
	zlib "github.com/imlinhanchao/echo-service/internal/providers/zlib"
	"github.com/imlinhanchao/echo-service/internal/store"
	"github.com/imlinhanchao/echo-service/internal/store/repos"
	"github.com/imlinhanchao/echo-service/internal/tts"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[main] 配置错误: %v", err)
	}

	db, err := store.Open(cfg)
	if err != nil {
		log.Fatalf("[main] 数据库初始化失败: %v", err)
	}
	log.Printf("[main] MySQL 已连接，数据库 %s", cfg.MySQL.DB)

	r := repos.New(db)
	registry := providers.NewRegistry()
	registry.Register(zlib.New(cfg.ZlibDomains, cfg.MaxDownloadBytes))

	ttsClient := tts.New(cfg.TTSBaseURL, cfg.TTSTimeout)
	manager := jobs.NewManager(cfg, r, registry, ttsClient)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	manager.Start(ctx)

	server := api.New(cfg, r, registry, ttsClient, manager)
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Printf("[main] 监听 %s（TTS: %s）", cfg.Addr, cfg.TTSBaseURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[main] HTTP 服务失败: %v", err)
		}
	}()

	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := r.Sessions.DeleteExpired(context.Background()); err == nil && n > 0 {
					log.Printf("[main] 清理过期会话 %d 条", n)
				}
				if n, err := r.APITokens.DeleteExpired(context.Background()); err == nil && n > 0 {
					log.Printf("[main] 清理过期/撤销 Token %d 条", n)
				}
			}
		}
	}()

	<-ctx.Done()
	log.Printf("[main] 收到退出信号，正在关闭…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	manager.Stop()
	log.Printf("[main] 已退出")
}
