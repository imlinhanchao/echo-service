package repos

import (
	"context"
	"errors"
	"time"

	"github.com/imlinhanchao/echo-service/internal/store/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SyncRepo struct{ db *gorm.DB }

// UpsertProgress saves progress using Last-Write-Wins (LWW) based on updated_at.
func (r *SyncRepo) UpsertProgress(ctx context.Context, p *models.UserProgress) error {
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now().UTC()
	}
	// Check existing record
	var existing models.UserProgress
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND item_type = ? AND item_id = ?", p.UserID, p.ItemType, p.ItemID).
		First(&existing).Error
	if err == nil {
		// Only update if incoming updated_at is newer or equal
		if p.UpdatedAt.Before(existing.UpdatedAt) {
			return nil
		}
		return r.db.WithContext(ctx).Model(&models.UserProgress{}).
			Where("id = ?", existing.ID).
			Updates(map[string]any{
				"chapter_index": p.ChapterIndex,
				"chunk_n":       p.ChunkN,
				"offset_ms":     p.OffsetMs,
				"char_offset":   p.CharOffset,
				"updated_at":    p.UpdatedAt,
			}).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return r.db.WithContext(ctx).Create(p).Error
}

// ListProgress returns all progress records for a user.
func (r *SyncRepo) ListProgress(ctx context.Context, userID uint64) ([]models.UserProgress, error) {
	var out []models.UserProgress
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("updated_at DESC").
		Find(&out).Error
	return out, err
}

// UpsertNote saves or updates a user note using LWW.
func (r *SyncRepo) UpsertNote(ctx context.Context, note *models.UserNote) error {
	if note.UpdatedAt.IsZero() {
		note.UpdatedAt = time.Now().UTC()
	}
	if note.CreatedAt.IsZero() {
		note.CreatedAt = note.UpdatedAt
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"position_ms": note.PositionMs,
			"text":        note.Text,
			"chapter":     note.Chapter,
			"updated_at":  note.UpdatedAt,
		}),
	}).Create(note).Error
}

// ListNotes returns all timestamp notes for a user (or for a specific audiobook).
func (r *SyncRepo) ListNotes(ctx context.Context, userID uint64, audiobookID uint64) ([]models.UserNote, error) {
	var out []models.UserNote
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if audiobookID > 0 {
		q = q.Where("audiobook_id = ?", audiobookID)
	}
	err := q.Order("audiobook_id ASC, position_ms ASC").Find(&out).Error
	return out, err
}

// DeleteNote removes a note by ID for a user.
func (r *SyncRepo) DeleteNote(ctx context.Context, userID uint64, noteID string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND id = ?", userID, noteID).
		Delete(&models.UserNote{}).Error
}
