package models

import "time"

// UserProgress stores playback or reading progress for one book/audiobook.
type UserProgress struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       uint64    `gorm:"not null;uniqueIndex:uk_user_progress,priority:1" json:"user_id"`
	ItemType     string    `gorm:"size:16;not null;uniqueIndex:uk_user_progress,priority:2" json:"item_type"` // "audiobook" or "book"
	ItemID       uint64    `gorm:"not null;uniqueIndex:uk_user_progress,priority:3" json:"item_id"`
	ChapterIndex int       `gorm:"not null;default:0" json:"chapter_index"`
	ChunkN       int       `gorm:"not null;default:0" json:"chunk_n"`
	OffsetMs     int       `gorm:"not null;default:0" json:"offset_ms"`
	CharOffset   int       `gorm:"not null;default:0" json:"char_offset"`
	UpdatedAt    time.Time `gorm:"type:datetime(3);not null" json:"updated_at"`
	User         User      `gorm:"constraint:OnDelete:CASCADE" json:"-"`
}

func (UserProgress) TableName() string { return "user_progress" }

// UserNote stores one timestamped note for an audiobook.
type UserNote struct {
	ID          string    `gorm:"primaryKey;size:64" json:"id"`
	UserID      uint64    `gorm:"not null;index:idx_user_notes_user,priority:1" json:"user_id"`
	AudiobookID uint64    `gorm:"not null;index:idx_user_notes_user,priority:2" json:"audiobook_id"`
	PositionMs  int       `gorm:"not null" json:"position_ms"`
	Text        string    `gorm:"type:text;not null" json:"text"`
	Chapter     string    `gorm:"size:255" json:"chapter"`
	CreatedAt   time.Time `gorm:"type:datetime(3);not null" json:"created_at"`
	UpdatedAt   time.Time `gorm:"type:datetime(3);not null" json:"updated_at"`
	User        User      `gorm:"constraint:OnDelete:CASCADE" json:"-"`
}

func (UserNote) TableName() string { return "user_notes" }
