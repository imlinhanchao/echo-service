// Package repos contains the GORM repository layer. Every user-owned query is
// scoped by user_id; handlers never build raw SQL themselves.
package repos

import (
	"context"
	"errors"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/imlinhanchao/echo-service/internal/store/models"
)

type Repos struct {
	DB         *gorm.DB
	Users      *UserRepo
	Sessions   *SessionRepo
	Providers  *ProviderAccountRepo
	Books      *BookRepo
	Jobs       *JobRepo
	Audiobooks *AudiobookRepo
	Settings   *SettingsRepo
	APITokens  *APITokenRepo
}

func New(db *gorm.DB) *Repos {
	return &Repos{
		DB:         db,
		Users:      &UserRepo{db: db},
		Sessions:   &SessionRepo{db: db},
		Providers:  &ProviderAccountRepo{db: db},
		Books:      &BookRepo{db: db},
		Jobs:       &JobRepo{db: db},
		Audiobooks: &AudiobookRepo{db: db},
		Settings:   &SettingsRepo{db: db},
		APITokens:  &APITokenRepo{db: db},
	}
}

func utcNow() time.Time { return time.Now().UTC() }

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

type UserRepo struct{ db *gorm.DB }

func (r *UserRepo) Create(ctx context.Context, email, passwordHash string) (*models.User, error) {
	u := &models.User{Email: email, PasswordHash: passwordHash, CreatedAt: utcNow()}
	if err := r.db.WithContext(ctx).Create(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

func (r *UserRepo) ByEmail(ctx context.Context, email string) (*models.User, error) {
	var u models.User
	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) ByID(ctx context.Context, id uint64) (*models.User, error) {
	var u models.User
	if err := r.db.WithContext(ctx).First(&u, id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

type SessionRepo struct{ db *gorm.DB }

func (r *SessionRepo) Create(ctx context.Context, tokenHash string, userID uint64, expires time.Time, ua, ip string) error {
	return r.db.WithContext(ctx).Create(&models.Session{
		TokenHash: tokenHash,
		UserID:    userID,
		CreatedAt: utcNow(),
		ExpiresAt: expires,
		UserAgent: truncate(ua, 255),
		IP:        truncate(ip, 45),
	}).Error
}

func (r *SessionRepo) ByToken(ctx context.Context, tokenHash string) (*models.Session, error) {
	var s models.Session
	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND expires_at > ?", tokenHash, utcNow()).
		First(&s).Error
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SessionRepo) Delete(ctx context.Context, tokenHash string) error {
	return r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).Delete(&models.Session{}).Error
}

func (r *SessionRepo) DeleteByUser(ctx context.Context, userID uint64) error {
	return r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&models.Session{}).Error
}

func (r *SessionRepo) DeleteExpired(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Where("expires_at <= ?", utcNow()).Delete(&models.Session{})
	return res.RowsAffected, res.Error
}

// ---------------------------------------------------------------------------
// Provider accounts
// ---------------------------------------------------------------------------

type ProviderAccountRepo struct{ db *gorm.DB }

func (r *ProviderAccountRepo) Upsert(ctx context.Context, userID uint64, provider, domain string, enc []byte, status string, info datatypes.JSON) (*models.ProviderAccount, error) {
	now := utcNow()
	row := &models.ProviderAccount{
		UserID:         userID,
		Provider:       provider,
		Domain:         domain,
		CredentialsEnc: enc,
		Status:         status,
		AccountInfo:    info,
		LastVerifiedAt: &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "provider"}},
		DoUpdates: clause.Assignments(map[string]any{
			"domain":           domain,
			"credentials_enc":  enc,
			"status":           status,
			"account_info":     info,
			"last_verified_at": now,
			"updated_at":       now,
		}),
	}).Create(row).Error
	if err != nil {
		return nil, err
	}
	return r.ByUserProvider(ctx, userID, provider)
}

func (r *ProviderAccountRepo) ByUserProvider(ctx context.Context, userID uint64, provider string) (*models.ProviderAccount, error) {
	var a models.ProviderAccount
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND provider = ?", userID, provider).
		First(&a).Error
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *ProviderAccountRepo) UpdateStatus(ctx context.Context, userID uint64, provider, status string, info datatypes.JSON) error {
	updates := map[string]any{
		"status":           status,
		"account_info":     info,
		"last_verified_at": utcNow(),
		"updated_at":       utcNow(),
	}
	return r.db.WithContext(ctx).Model(&models.ProviderAccount{}).
		Where("user_id = ? AND provider = ?", userID, provider).
		Updates(updates).Error
}

func (r *ProviderAccountRepo) Delete(ctx context.Context, userID uint64, provider string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND provider = ?", userID, provider).
		Delete(&models.ProviderAccount{}).Error
}

// ---------------------------------------------------------------------------
// Books
// ---------------------------------------------------------------------------

type BookRepo struct{ db *gorm.DB }

func (r *BookRepo) Create(ctx context.Context, b *models.Book) error {
	if b.CreatedAt.IsZero() {
		b.CreatedAt = utcNow()
	}
	return r.db.WithContext(ctx).Create(b).Error
}

func (r *BookRepo) List(ctx context.Context, userID uint64) ([]models.Book, error) {
	var out []models.Book
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").
		Find(&out).Error
	return out, err
}

func (r *BookRepo) ByID(ctx context.Context, userID, id uint64) (*models.Book, error) {
	var b models.Book
	err := r.db.WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&b).Error
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *BookRepo) BySource(ctx context.Context, userID uint64, provider, ref string) (*models.Book, error) {
	var b models.Book
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND provider = ? AND provider_book_id = ?", userID, provider, ref).
		First(&b).Error
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *BookRepo) Delete(ctx context.Context, userID, id uint64) error {
	return r.db.WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).Delete(&models.Book{}).Error
}

// ---------------------------------------------------------------------------
// Jobs
// ---------------------------------------------------------------------------

type JobRepo struct{ db *gorm.DB }

func (r *JobRepo) Create(ctx context.Context, j *models.Job) error {
	if j.CreatedAt.IsZero() {
		j.CreatedAt = utcNow()
	}
	return r.db.WithContext(ctx).Create(j).Error
}

func (r *JobRepo) ByID(ctx context.Context, userID, id uint64) (*models.Job, error) {
	var j models.Job
	err := r.db.WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&j).Error
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (r *JobRepo) ByIDAnyUser(ctx context.Context, id uint64) (*models.Job, error) {
	var j models.Job
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&j).Error
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (r *JobRepo) List(ctx context.Context, userID uint64, limit, offset int) ([]models.Job, int64, error) {
	q := r.db.WithContext(ctx).Model(&models.Job{}).Where("user_id = ?", userID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []models.Job
	err := q.Order("id DESC").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}

// Claim atomically moves a queued job to running; only one caller can win.
func (r *JobRepo) Claim(ctx context.Context, id uint64) (bool, error) {
	res := r.db.WithContext(ctx).Model(&models.Job{}).
		Where("id = ? AND status = ?", id, models.JobQueued).
		Updates(map[string]any{"status": models.JobRunning, "started_at": utcNow()})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func (r *JobRepo) UpdateProgress(ctx context.Context, id uint64, progress uint8) error {
	return r.db.WithContext(ctx).Model(&models.Job{}).
		Where("id = ?", id).
		Update("progress", progress).Error
}

func (r *JobRepo) SetStatus(ctx context.Context, id uint64, status, errMsg string) error {
	updates := map[string]any{"status": status}
	if errMsg != "" {
		updates["error"] = errMsg
	}
	return r.db.WithContext(ctx).Model(&models.Job{}).Where("id = ?", id).Updates(updates).Error
}

func (r *JobRepo) Finish(ctx context.Context, id uint64, status, errMsg string, result datatypes.JSON) error {
	updates := map[string]any{
		"status":      status,
		"finished_at": utcNow(),
	}
	if errMsg != "" {
		updates["error"] = errMsg
	}
	if status == models.JobDone {
		updates["progress"] = 100
	}
	if len(result) > 0 {
		updates["result"] = result
	}
	return r.db.WithContext(ctx).Model(&models.Job{}).Where("id = ?", id).Updates(updates).Error
}

// Cancel marks a queued job cancelled, or asks a running job to stop.
func (r *JobRepo) Cancel(ctx context.Context, userID, id uint64) (*models.Job, error) {
	j, err := r.ByID(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	switch j.Status {
	case models.JobQueued:
		err = r.db.WithContext(ctx).Model(&models.Job{}).Where("id = ?", id).
			Updates(map[string]any{"status": models.JobCancelled, "finished_at": utcNow()}).Error
	case models.JobRunning:
		err = r.db.WithContext(ctx).Model(&models.Job{}).Where("id = ?", id).
			Update("status", models.JobCancelling).Error
	}
	if err != nil {
		return nil, err
	}
	return r.ByID(ctx, userID, id)
}

// IsCancelled reports whether the job should stop (worker polls this).
func (r *JobRepo) IsCancelled(ctx context.Context, id uint64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&models.Job{}).
		Where("id = ? AND status IN ?", id, []string{models.JobCancelling, models.JobCancelled}).
		Count(&count).Error
	return count > 0, err
}

func (r *JobRepo) ActiveCountByUser(ctx context.Context, userID uint64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&models.Job{}).
		Where("user_id = ? AND status IN ?", userID,
			[]string{models.JobQueued, models.JobRunning, models.JobCancelling}).
		Count(&count).Error
	return count, err
}

func (r *JobRepo) MarkStaleRunning(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Model(&models.Job{}).
		Where("status IN ?", []string{models.JobRunning, models.JobCancelling}).
		Updates(map[string]any{
			"status":      models.JobFailed,
			"error":       "服务重启中断，请重试",
			"finished_at": utcNow(),
		})
	return res.RowsAffected, res.Error
}

func (r *JobRepo) Queued(ctx context.Context) ([]models.Job, error) {
	var out []models.Job
	err := r.db.WithContext(ctx).
		Where("status = ?", models.JobQueued).
		Order("id ASC").Find(&out).Error
	return out, err
}

// ---------------------------------------------------------------------------
// Audiobooks
// ---------------------------------------------------------------------------

type AudiobookRepo struct{ db *gorm.DB }

func (r *AudiobookRepo) Create(ctx context.Context, a *models.Audiobook) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = utcNow()
	}
	return r.db.WithContext(ctx).Create(a).Error
}

func (r *AudiobookRepo) ByID(ctx context.Context, userID, id uint64) (*models.Audiobook, error) {
	var a models.Audiobook
	err := r.db.WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).First(&a).Error
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *AudiobookRepo) List(ctx context.Context, userID uint64) ([]models.Audiobook, error) {
	var out []models.Audiobook
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").Find(&out).Error
	return out, err
}

func (r *AudiobookRepo) ListByBook(ctx context.Context, userID, bookID uint64) ([]models.Audiobook, error) {
	var out []models.Audiobook
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND book_id = ?", userID, bookID).
		Order("id DESC").Find(&out).Error
	return out, err
}

func (r *AudiobookRepo) UpdateProgress(ctx context.Context, id uint64, progress uint8) error {
	return r.db.WithContext(ctx).Model(&models.Audiobook{}).
		Where("id = ?", id).Update("progress", progress).Error
}

func (r *AudiobookRepo) Finish(ctx context.Context, id uint64, status, errMsg string, a *models.Audiobook) error {
	updates := map[string]any{"status": status, "finished_at": utcNow()}
	if errMsg != "" {
		updates["error"] = errMsg
	}
	if a != nil {
		updates["audio_path"] = a.AudioPath
		updates["srt_path"] = a.SRTPath
		updates["manifest_path"] = a.ManifestPath
		updates["format"] = a.Format
		updates["duration_s"] = a.DurationS
		updates["size_bytes"] = a.SizeBytes
		updates["segments"] = a.Segments
		updates["chars"] = a.Chars
	}
	if status == "done" {
		updates["progress"] = 100
	}
	return r.db.WithContext(ctx).Model(&models.Audiobook{}).Where("id = ?", id).Updates(updates).Error
}

func (r *AudiobookRepo) Delete(ctx context.Context, userID, id uint64) error {
	return r.db.WithContext(ctx).Where("user_id = ? AND id = ?", userID, id).Delete(&models.Audiobook{}).Error
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

type SettingsRepo struct{ db *gorm.DB }

func (r *SettingsRepo) Get(ctx context.Context, userID uint64) (*models.UserSettings, error) {
	var s models.UserSettings
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &models.UserSettings{UserID: userID, DefaultSpeed: 1, AllowEdge: true}, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SettingsRepo) Save(ctx context.Context, s *models.UserSettings) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"default_model", "default_voice", "default_speed", "default_instruct", "fallback_model", "allow_edge"}),
	}).Create(s).Error
}

// ---------------------------------------------------------------------------

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ---------------------------------------------------------------------------
// API tokens (Echo App bearer auth)
// ---------------------------------------------------------------------------

type APITokenRepo struct{ db *gorm.DB }

func (r *APITokenRepo) Create(ctx context.Context, userID uint64, name, tokenHash string, expires time.Time) (*models.APIToken, error) {
	t := &models.APIToken{
		UserID:    userID,
		Name:      truncate(name, 120),
		TokenHash: tokenHash,
		CreatedAt: utcNow(),
		ExpiresAt: expires,
	}
	if err := r.db.WithContext(ctx).Create(t).Error; err != nil {
		return nil, err
	}
	return t, nil
}

// ByHash returns a usable (not revoked, not expired) token row.
func (r *APITokenRepo) ByHash(ctx context.Context, tokenHash string) (*models.APIToken, error) {
	var t models.APIToken
	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", tokenHash, utcNow()).
		First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *APITokenRepo) Touch(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Model(&models.APIToken{}).
		Where("id = ?", id).Update("last_used_at", utcNow()).Error
}

func (r *APITokenRepo) Revoke(ctx context.Context, id uint64) error {
	now := utcNow()
	return r.db.WithContext(ctx).Model(&models.APIToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", now).Error
}

func (r *APITokenRepo) RevokeByHash(ctx context.Context, tokenHash string) error {
	now := utcNow()
	return r.db.WithContext(ctx).Model(&models.APIToken{}).
		Where("token_hash = ? AND revoked_at IS NULL", tokenHash).
		Update("revoked_at", now).Error
}

func (r *APITokenRepo) DeleteExpired(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("expires_at <= ? OR revoked_at IS NOT NULL", utcNow()).
		Delete(&models.APIToken{})
	return res.RowsAffected, res.Error
}
