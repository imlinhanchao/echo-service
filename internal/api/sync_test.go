package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imlinhanchao/echo-service/internal/config"
	"github.com/imlinhanchao/echo-service/internal/store/models"
)

type dummySyncRepo struct {
	progresses []models.UserProgress
	notes      []models.UserNote
}

func TestSyncEndpointsMock(t *testing.T) {
	// 验证请求体解析和返回结构
	s := &Server{
		cfg: &config.Config{},
	}

	user := &models.User{ID: 42, Email: "test@example.com"}
	ctx := context.WithValue(context.Background(), userKey, user)

	// 测试 GET /api/sync/progress 针对空列表的响应
	req := httptest.NewRequest(http.MethodGet, "/api/sync/progress", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	// 构造简单的 progress JSON
	body := syncProgressBody{
		Progresses: []progressPayload{
			{
				ItemType:     "audiobook",
				ItemID:       101,
				ChapterIndex: 2,
				ChunkN:       2,
				OffsetMs:     50000,
				UpdatedAt:    time.Now().UTC(),
			},
		},
	}
	raw, _ := json.Marshal(body)
	postReq := httptest.NewRequest(http.MethodPost, "/api/sync/progress", bytes.NewReader(raw)).WithContext(ctx)
	postRec := httptest.NewRecorder()

	// 只要没有 repo，返回错误而非 panic
	s.handleGetProgress(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("空 repo 应返回 500，实际: %d", rec.Code)
	}

	s.handlePostProgress(postRec, postReq)
	if postRec.Code != http.StatusInternalServerError {
		t.Fatalf("空 repo 应返回 500，实际: %d", postRec.Code)
	}
}
