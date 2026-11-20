package store_test

import (
	"context"
	"crypto/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/imlinhanchao/echo-service/internal/config"
	"github.com/imlinhanchao/echo-service/internal/store"
	"github.com/imlinhanchao/echo-service/internal/store/models"
	"github.com/imlinhanchao/echo-service/internal/store/repos"
)

// These tests need a real MySQL server. Enable them with:
//
//	ECHO_TEST_MYSQL=1 ECHO_MYSQL_PASSWORD=... go test ./internal/store/...
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	if os.Getenv("ECHO_TEST_MYSQL") != "1" {
		t.Skip("设置 ECHO_TEST_MYSQL=1 后运行 MySQL 集成测试")
	}
	password := os.Getenv("ECHO_MYSQL_PASSWORD")
	if password == "" {
		t.Skip("ECHO_MYSQL_PASSWORD 未设置")
	}
	port, _ := strconv.Atoi(envOr("ECHO_MYSQL_PORT", "3306"))
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return &config.Config{
		DataDir: t.TempDir(),
		MySQL: config.MySQLConfig{
			Host:     envOr("ECHO_MYSQL_HOST", "127.0.0.1"),
			Port:     port,
			User:     envOr("ECHO_MYSQL_USER", "hancel"),
			Password: password,
			DB:       envOr("ECHO_TEST_MYSQL_DB", "echo_service_test"),
		},
		Secret:     secret,
		SessionTTL: time.Hour,
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func setup(t *testing.T) *repos.Repos {
	t.Helper()
	cfg := testConfig(t)
	db, err := store.Open(cfg)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	// Clean tables in dependency order.
	if err := db.Exec("SET FOREIGN_KEY_CHECKS=0").Error; err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"audiobooks", "jobs", "books", "provider_accounts", "sessions", "user_settings", "users"} {
		if err := db.Exec("TRUNCATE TABLE " + table).Error; err != nil {
			t.Fatalf("清空 %s: %v", table, err)
		}
	}
	_ = db.Exec("SET FOREIGN_KEY_CHECKS=1").Error
	return repos.New(db)
}

func TestUserAndSessionLifecycle(t *testing.T) {
	r := setup(t)
	ctx := context.Background()

	user, err := r.Users.Create(ctx, "a@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Users.ByEmail(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}

	expires := time.Now().Add(time.Hour)
	if err := r.Sessions.Create(ctx, "tokenhash-a", user.ID, expires, "ua", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	sess, err := r.Sessions.ByToken(ctx, "tokenhash-a")
	if err != nil {
		t.Fatal(err)
	}
	if sess.UserID != user.ID {
		t.Fatalf("session user mismatch: %d", sess.UserID)
	}
	if err := r.Sessions.Delete(ctx, "tokenhash-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Sessions.ByToken(ctx, "tokenhash-a"); err == nil {
		t.Fatal("删除后不应还能查到 session")
	}
}

func TestAccountIsolation(t *testing.T) {
	r := setup(t)
	ctx := context.Background()

	a, _ := r.Users.Create(ctx, "a@example.com", "hash")
	b, _ := r.Users.Create(ctx, "b@example.com", "hash")

	bookA := &models.Book{
		UserID: a.ID, Provider: "zlib", ProviderBookID: "1:aa",
		Title: "A 的书", Status: "downloaded",
	}
	if err := r.Books.Create(ctx, bookA); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Books.ByID(ctx, a.ID, bookA.ID); err != nil {
		t.Fatalf("A 应能读取自己的书: %v", err)
	}
	if _, err := r.Books.ByID(ctx, b.ID, bookA.ID); err == nil {
		t.Fatal("B 不应读取 A 的书")
	}
	booksB, err := r.Books.List(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(booksB) != 0 {
		t.Fatalf("B 的书库应为空，得到 %d", len(booksB))
	}
	// provider accounts are per-user
	enc := []byte("cipher")
	if _, err := r.Providers.Upsert(ctx, a.ID, "zlib", "https://z-lib.gd", enc, "ok", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Providers.ByUserProvider(ctx, b.ID, "zlib"); err == nil {
		t.Fatal("B 不应看到 A 的图书源账号")
	}
}

func TestJobClaimIsAtomic(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	user, _ := r.Users.Create(ctx, "c@example.com", "hash")

	job := &models.Job{UserID: user.ID, Kind: models.JobKindDownload, Status: models.JobQueued}
	if err := r.Jobs.Create(ctx, job); err != nil {
		t.Fatal(err)
	}

	type result struct {
		ok  bool
		err error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			ok, err := r.Jobs.Claim(ctx, job.ID)
			results <- result{ok, err}
		}()
	}
	claimed := 0
	for i := 0; i < 2; i++ {
		res := <-results
		if res.err != nil {
			t.Fatal(res.err)
		}
		if res.ok {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("同一任务应只被领取一次，实际 %d 次", claimed)
	}

	if err := r.Jobs.Finish(ctx, job.ID, models.JobDone, "", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Jobs.ByID(ctx, user.ID, job.ID)
	if got.Status != models.JobDone || got.Progress != 100 {
		t.Fatalf("完成任务状态错误: %+v", got)
	}
}

func TestCancelAndStaleRecovery(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	user, _ := r.Users.Create(ctx, "d@example.com", "hash")

	queued := &models.Job{UserID: user.ID, Kind: models.JobKindDownload, Status: models.JobQueued}
	_ = r.Jobs.Create(ctx, queued)
	if _, err := r.Jobs.Cancel(ctx, user.ID, queued.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Jobs.ByID(ctx, user.ID, queued.ID)
	if got.Status != models.JobCancelled {
		t.Fatalf("queued 任务取消后应为 cancelled，得到 %s", got.Status)
	}

	running := &models.Job{UserID: user.ID, Kind: models.JobKindAudiobook, Status: models.JobRunning}
	_ = r.Jobs.Create(ctx, running)
	n, err := r.Jobs.MarkStaleRunning(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("应至少修复 1 个 running 任务，实际 %d", n)
	}
	got, _ = r.Jobs.ByID(ctx, user.ID, running.ID)
	if got.Status != models.JobFailed {
		t.Fatalf("running 任务重启后应 failed，得到 %s", got.Status)
	}
}

func TestAPITokenLifecycle(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	user, err := r.Users.Create(ctx, "token@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}

	row, err := r.APITokens.Create(ctx, user.ID, "iPhone", "hash-phone", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if row.ID == 0 || row.Name != "iPhone" {
		t.Fatalf("unexpected token row: %+v", row)
	}
	if _, err := r.APITokens.ByHash(ctx, "hash-phone"); err != nil {
		t.Fatalf("有效 token 应能查到: %v", err)
	}

	// 过期 token 不可用
	if _, err := r.APITokens.Create(ctx, user.ID, "old", "hash-expired", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.APITokens.ByHash(ctx, "hash-expired"); err == nil {
		t.Fatal("过期 token 不应可用")
	}

	// 撤销后不可用
	if err := r.APITokens.Revoke(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.APITokens.ByHash(ctx, "hash-phone"); err == nil {
		t.Fatal("已撤销 token 不应可用")
	}

	// 清理过期/撤销记录
	if n, err := r.APITokens.DeleteExpired(ctx); err != nil || n < 2 {
		t.Fatalf("清理数量 = %d, err = %v", n, err)
	}
}
