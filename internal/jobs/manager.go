// Package jobs runs background tasks (downloads and audiobook conversions).
// Jobs are persisted in MySQL and claimed atomically, so a restart is safe.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gorm.io/datatypes"

	"github.com/imlinhanchao/echo-service/internal/audiobook"
	"github.com/imlinhanchao/echo-service/internal/config"
	"github.com/imlinhanchao/echo-service/internal/providers"
	"github.com/imlinhanchao/echo-service/internal/secret"
	"github.com/imlinhanchao/echo-service/internal/store"
	"github.com/imlinhanchao/echo-service/internal/store/models"
	"github.com/imlinhanchao/echo-service/internal/store/repos"
	"github.com/imlinhanchao/echo-service/internal/tts"
)

const (
	globalWorkers = 4
	queueSize     = 256
)

// Manager owns the in-process queue and executes jobs.
type Manager struct {
	cfg      *config.Config
	repos    *repos.Repos
	registry *providers.Registry
	tts      *tts.Client

	queue chan uint64
	wg    sync.WaitGroup
	sem   chan struct{} // global job concurrency

	mu      sync.Mutex
	cancels map[uint64]context.CancelFunc

	localSem chan struct{} // 本地模型同时只跑 1 个转换
	edgeSem  chan struct{} // Edge 同时最多 2 个转换
}

func NewManager(cfg *config.Config, r *repos.Repos, reg *providers.Registry, ttsClient *tts.Client) *Manager {
	return &Manager{
		cfg:      cfg,
		repos:    r,
		registry: reg,
		tts:      ttsClient,
		queue:    make(chan uint64, queueSize),
		sem:      make(chan struct{}, globalWorkers),
		cancels:  make(map[uint64]context.CancelFunc),
		localSem: make(chan struct{}, 1),
		edgeSem:  make(chan struct{}, 2),
	}
}

// Start recovers queued jobs and launches the dispatcher.
func (m *Manager) Start(ctx context.Context) {
	if n, err := m.repos.Jobs.MarkStaleRunning(ctx); err != nil {
		log.Printf("[jobs] 清理残留 running 任务失败: %v", err)
	} else if n > 0 {
		log.Printf("[jobs] 已将 %d 个中断任务标记为 failed", n)
	}
	pending, err := m.repos.Jobs.Queued(ctx)
	if err != nil {
		log.Printf("[jobs] 读取待执行任务失败: %v", err)
	}
	m.wg.Add(1)
	go m.dispatchLoop(ctx)
	for _, j := range pending {
		select {
		case m.queue <- j.ID:
		default:
			log.Printf("[jobs] 队列已满，任务 %d 等待重启后重试", j.ID)
		}
	}
}

func (m *Manager) Stop() {
	m.wg.Wait()
}

func (m *Manager) dispatchLoop(ctx context.Context) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-m.queue:
			m.sem <- struct{}{}
			m.wg.Add(1)
			go func(id uint64) {
				defer func() { <-m.sem; m.wg.Done() }()
				m.run(ctx, id)
			}(id)
		}
	}
}

// Submit persists a job and enqueues it.
func (m *Manager) Submit(ctx context.Context, userID uint64, kind string, payload any, refID *uint64) (*models.Job, error) {
	active, err := m.repos.Jobs.ActiveCountByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if active > 0 {
		return nil, ErrUserBusy
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	job := &models.Job{
		UserID:  userID,
		Kind:    kind,
		Status:  models.JobQueued,
		Payload: datatypes.JSON(raw),
		RefID:   refID,
	}
	if err := m.repos.Jobs.Create(ctx, job); err != nil {
		return nil, err
	}
	select {
	case m.queue <- job.ID:
	default:
		_ = m.repos.Jobs.Finish(context.Background(), job.ID, models.JobFailed, "任务队列已满，请稍后重试", nil)
		return nil, ErrQueueFull
	}
	return job, nil
}

var (
	ErrUserBusy  = errors.New("已有进行中的任务，请等待完成或取消后再试")
	ErrQueueFull = errors.New("任务队列已满，请稍后重试")
)

func (m *Manager) Cancel(ctx context.Context, userID, id uint64) (*models.Job, error) {
	job, err := m.repos.Jobs.Cancel(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	cancel := m.cancels[id]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return job, nil
}

func (m *Manager) run(parent context.Context, id uint64) {
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	m.cancels[id] = cancel
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.cancels, id)
		m.mu.Unlock()
		cancel()
	}()

	claimed, err := m.repos.Jobs.Claim(ctx, id)
	if err != nil {
		log.Printf("[jobs] 领取任务 %d 失败: %v", id, err)
		return
	}
	if !claimed {
		return // cancelled or claimed elsewhere
	}
	job, err := m.repos.Jobs.ByIDAnyUser(ctx, id)
	if err != nil {
		log.Printf("[jobs] 读取任务 %d 失败: %v", id, err)
		return
	}

	var runErr error
	var result any
	switch job.Kind {
	case models.JobKindDownload:
		result, runErr = m.runDownload(ctx, job)
	case models.JobKindAudiobook:
		result, runErr = m.runAudiobook(ctx, job)
	default:
		runErr = fmt.Errorf("未知任务类型 %q", job.Kind)
	}

	// Final bookkeeping must not be cancelled by the job's own context.
	finalCtx := context.WithoutCancel(ctx)
	if runErr != nil {
		status := models.JobFailed
		msg := runErr.Error()
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, audiobook.ErrCancelled) {
			status = models.JobCancelled
			msg = "已取消"
		}
		_ = m.repos.Jobs.Finish(finalCtx, id, status, msg, nil)
		log.Printf("[jobs] 任务 %d (%s) 结束: %s %v", id, job.Kind, status, runErr)
		return
	}
	raw, _ := json.Marshal(result)
	_ = m.repos.Jobs.Finish(finalCtx, id, models.JobDone, "", datatypes.JSON(raw))
	log.Printf("[jobs] 任务 %d (%s) 完成", id, job.Kind)
}

// ---------------------------------------------------------------------------
// Credentials helpers
// ---------------------------------------------------------------------------

func (m *Manager) loadCredentials(ctx context.Context, userID uint64, providerID string) (providers.Credentials, error) {
	account, err := m.repos.Providers.ByUserProvider(ctx, userID, providerID)
	if err != nil {
		return nil, providers.ErrNotConfigured
	}
	var creds providers.Credentials
	if err := secret.DecryptJSON(m.cfg.Secret, account.CredentialsEnc, &creds); err != nil {
		return nil, fmt.Errorf("解密 %s 凭据失败: %w", providerID, err)
	}
	return creds, nil
}

// ---------------------------------------------------------------------------
// Download job
// ---------------------------------------------------------------------------

type downloadPayload struct {
	Provider string `json:"provider"`
	Ref      string `json:"ref"`
}

func (m *Manager) runDownload(ctx context.Context, job *models.Job) (any, error) {
	var p downloadPayload
	if err := store.DecodeJSON(job.Payload, &p); err != nil {
		return nil, fmt.Errorf("任务参数无效: %w", err)
	}
	provider, ok := m.registry.Get(p.Provider)
	if !ok {
		return nil, fmt.Errorf("未知图书源 %q", p.Provider)
	}
	creds, err := m.loadCredentials(ctx, job.UserID, p.Provider)
	if err != nil {
		return nil, err
	}

	destDir := filepath.Join(m.cfg.DataDir, "users", fmt.Sprint(job.UserID), "books")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建书库目录失败: %w", err)
	}
	book, err := provider.GetBook(ctx, creds, p.Ref)
	if err != nil {
		return nil, err
	}
	if existing, err := m.repos.Books.BySource(ctx, job.UserID, p.Provider, p.Ref); err == nil && existing != nil {
		return nil, fmt.Errorf("《%s》已在书库中", existing.Title)
	}

	lastUpdate := time.Now()
	res, err := provider.Download(ctx, creds, p.Ref, destDir, func(written, total int64) {
		if total <= 0 {
			return
		}
		if time.Since(lastUpdate) < 300*time.Millisecond {
			return
		}
		lastUpdate = time.Now()
		pct := uint8(written * 100 / total)
		if pct > 99 {
			pct = 99
		}
		_ = m.repos.Jobs.UpdateProgress(ctx, job.ID, pct)
	})
	if err != nil {
		return nil, err
	}

	relPath, err := filepath.Rel(m.cfg.DataDir, res.Path)
	if err != nil {
		return nil, err
	}
	authors := store.JSON(book.Authors)
	if book.Authors == nil {
		authors = store.JSON([]string{})
	}
	rec := &models.Book{
		UserID:         job.UserID,
		Provider:       p.Provider,
		ProviderBookID: p.Ref,
		Title:          book.Title,
		Authors:        authors,
		Year:           book.Year,
		Publisher:      book.Publisher,
		Language:       book.Language,
		Format:         res.Format,
		SizeBytes:      uint64(res.Size),
		ISBN:           book.ISBN,
		CoverURL:       book.CoverURL,
		Description:    book.Description,
		SourceURL:      book.SourceURL,
		FilePath:       relPath,
		SHA256:         res.SHA256,
		Status:         "downloaded",
	}
	if err := m.repos.Books.Create(ctx, rec); err != nil {
		return nil, fmt.Errorf("写入书库失败: %w", err)
	}
	return map[string]any{"book_id": rec.ID}, nil
}

// ---------------------------------------------------------------------------
// Audiobook job
// ---------------------------------------------------------------------------

type audiobookPayload struct {
	BookID        uint64  `json:"book_id"`
	Model         string  `json:"model"`
	Voice         string  `json:"voice"`
	Speed         float64 `json:"speed"`
	Instruct      string  `json:"instruct"`
	FallbackModel string  `json:"fallback_model"`
	Chapters      []int   `json:"chapters"`
}

func (m *Manager) runAudiobook(ctx context.Context, job *models.Job) (any, error) {
	var p audiobookPayload
	if err := store.DecodeJSON(job.Payload, &p); err != nil {
		return nil, fmt.Errorf("任务参数无效: %w", err)
	}
	book, err := m.repos.Books.ByID(ctx, job.UserID, p.BookID)
	if err != nil {
		return nil, fmt.Errorf("书籍不存在")
	}
	if book.Format != "epub" {
		return nil, fmt.Errorf("目前只支持 EPUB 转有声书，当前格式为 %s", book.Format)
	}
	if p.Model == "" {
		return nil, fmt.Errorf("未指定 TTS 模型")
	}
	epubPath := filepath.Join(m.cfg.DataDir, book.FilePath)

	audiobookDir := filepath.Join(m.cfg.DataDir, "users", fmt.Sprint(job.UserID), "audio", fmt.Sprint(job.ID))
	rec := &models.Audiobook{
		UserID:        job.UserID,
		BookID:        book.ID,
		Model:         p.Model,
		Voice:         p.Voice,
		Speed:         p.Speed,
		Instruct:      p.Instruct,
		FallbackModel: p.FallbackModel,
		Status:        models.JobRunning,
	}
	if rec.Speed <= 0 {
		rec.Speed = 1.0
	}
	if err := m.repos.Audiobooks.Create(ctx, rec); err != nil {
		return nil, fmt.Errorf("创建有声书记录失败: %w", err)
	}

	isEdge := p.Model == "edge-tts"
	if isEdge {
		m.edgeSem <- struct{}{}
		defer func() { <-m.edgeSem }()
	} else {
		m.localSem <- struct{}{}
		defer func() { <-m.localSem }()
	}

	progress := func(pct uint8, message string) {
		_ = m.repos.Audiobooks.UpdateProgress(context.WithoutCancel(ctx), rec.ID, pct)
		_ = m.repos.Jobs.UpdateProgress(context.WithoutCancel(ctx), job.ID, pct)
	}
	cancelled := func() bool {
		yes, _ := m.repos.Jobs.IsCancelled(context.Background(), job.ID)
		return yes
	}
	concurrency := 1
	if isEdge {
		concurrency = 6
	}
	opts := audiobook.Options{
		Model:              p.Model,
		Voice:              p.Voice,
		LangCode:           "zh",
		Instruct:           p.Instruct,
		FallbackModel:      p.FallbackModel,
		Speed:              rec.Speed,
		ChapterIndexes:     p.Chapters,
		MaxChars:           m.cfg.MaxJobChars,
		FFmpegPath:         m.cfg.FFmpegPath,
		SegmentConcurrency: concurrency,
	}
	res, err := audiobook.Run(ctx, m.tts, epubPath, audiobookDir, opts, progress, cancelled)
	if err != nil {
		_ = m.repos.Audiobooks.Finish(context.WithoutCancel(ctx), rec.ID, models.JobFailed, err.Error(), nil)
		return nil, err
	}

	rec.SRTPath, _ = filepath.Rel(m.cfg.DataDir, res.SRTPath)
	if res.AudioPath != "" {
		rec.AudioPath, _ = filepath.Rel(m.cfg.DataDir, res.AudioPath)
	}
	if res.ManifestPath != "" {
		rec.ManifestPath, _ = filepath.Rel(m.cfg.DataDir, res.ManifestPath)
	}
	rec.Format = res.Format
	rec.DurationS = res.DurationS
	rec.SizeBytes = uint64(res.SizeBytes)
	rec.Segments = uint32(res.Segments)
	rec.Chars = uint32(res.Chars)
	if err := m.repos.Audiobooks.Finish(context.WithoutCancel(ctx), rec.ID, "done", "", rec); err != nil {
		return nil, err
	}
	// segments/ 中间文件不再需要
	_ = removeDir(filepath.Join(audiobookDir, "segments"))
	return map[string]any{"audiobook_id": rec.ID}, nil
}
