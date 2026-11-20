// Package models contains the GORM entities (the TypeORM "entity" equivalent).
package models

import (
	"time"

	"gorm.io/datatypes"
)

// User is an application account. All business data belongs to exactly one user.
type User struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement"`
	Email        string    `gorm:"size:255;not null;uniqueIndex:uk_users_email"`
	PasswordHash string    `gorm:"size:255;not null"`
	CreatedAt    time.Time `gorm:"type:datetime(3);not null"`
	Disabled     bool      `gorm:"not null;default:false"`
}

func (User) TableName() string { return "users" }

// Session is a server-side login session; only the SHA-256 of the token is stored.
type Session struct {
	TokenHash string    `gorm:"primaryKey;size:64"`
	UserID    uint64    `gorm:"not null;index:idx_sessions_user"`
	CreatedAt time.Time `gorm:"type:datetime(3);not null"`
	ExpiresAt time.Time `gorm:"type:datetime(3);not null;index:idx_sessions_exp"`
	UserAgent string    `gorm:"size:255"`
	IP        string    `gorm:"size:45"`
	User      User      `gorm:"constraint:OnDelete:CASCADE"`
}

func (Session) TableName() string { return "sessions" }

// APIToken is a long-lived bearer token for Echo App (mobile/desktop clients).
// Only the SHA-256 of the token is stored.
type APIToken struct {
	ID         uint64     `gorm:"primaryKey;autoIncrement"`
	UserID     uint64     `gorm:"not null;index:idx_api_tokens_user"`
	Name       string     `gorm:"size:120"`
	TokenHash  string     `gorm:"size:64;not null;uniqueIndex:uk_api_tokens_hash"`
	CreatedAt  time.Time  `gorm:"type:datetime(3);not null"`
	ExpiresAt  time.Time  `gorm:"type:datetime(3);not null;index:idx_api_tokens_exp"`
	LastUsedAt *time.Time `gorm:"type:datetime(3)"`
	RevokedAt  *time.Time `gorm:"type:datetime(3)"`
	User       User       `gorm:"constraint:OnDelete:CASCADE"`
}

func (APIToken) TableName() string { return "api_tokens" }

// ProviderAccount stores one user's credentials for one book provider.
type ProviderAccount struct {
	ID             uint64         `gorm:"primaryKey;autoIncrement"`
	UserID         uint64         `gorm:"not null;uniqueIndex:uk_provider_accounts_user,priority:1"`
	Provider       string         `gorm:"size:32;not null;uniqueIndex:uk_provider_accounts_user,priority:2"`
	Domain         string         `gorm:"size:255"`
	CredentialsEnc []byte         `gorm:"type:varbinary(8192);not null"`
	Status         string         `gorm:"size:16;not null"`
	AccountInfo    datatypes.JSON `gorm:"type:json"`
	LastVerifiedAt *time.Time     `gorm:"type:datetime(3)"`
	CreatedAt      time.Time      `gorm:"type:datetime(3);not null"`
	UpdatedAt      time.Time      `gorm:"type:datetime(3);not null"`
	User           User           `gorm:"constraint:OnDelete:CASCADE"`
}

func (ProviderAccount) TableName() string { return "provider_accounts" }

// Book is one downloaded book in a user's library.
type Book struct {
	ID             uint64         `gorm:"primaryKey;autoIncrement"`
	UserID         uint64         `gorm:"not null;uniqueIndex:uk_books_user_source,priority:1;index:idx_books_user_created,priority:1"`
	Provider       string         `gorm:"size:32;not null;uniqueIndex:uk_books_user_source,priority:2"`
	ProviderBookID string         `gorm:"size:128;not null;uniqueIndex:uk_books_user_source,priority:3"`
	Title          string         `gorm:"size:512;not null"`
	Authors        datatypes.JSON `gorm:"type:json"`
	Year           string         `gorm:"size:16"`
	Publisher      string         `gorm:"size:255"`
	Language       string         `gorm:"size:32"`
	Format         string         `gorm:"size:16"`
	SizeBytes      uint64
	ISBN           string    `gorm:"size:64"`
	CoverURL       string    `gorm:"size:1024"`
	Description    string    `gorm:"type:mediumtext"`
	SourceURL      string    `gorm:"size:1024"`
	FilePath       string    `gorm:"size:1024"`
	SHA256         string    `gorm:"size:64"`
	Status         string    `gorm:"size:16;not null"`
	CreatedAt      time.Time `gorm:"type:datetime(3);not null;index:idx_books_user_created,priority:2"`
	User           User      `gorm:"constraint:OnDelete:CASCADE"`
}

func (Book) TableName() string { return "books" }

// Job is a background task (download or audiobook conversion).
type Job struct {
	ID         uint64         `gorm:"primaryKey;autoIncrement"`
	UserID     uint64         `gorm:"not null;index:idx_jobs_user_created,priority:1"`
	Kind       string         `gorm:"size:16;not null"`
	Status     string         `gorm:"size:16;not null;index:idx_jobs_status"`
	Progress   uint8          `gorm:"not null;default:0"`
	Error      string         `gorm:"type:text"`
	Payload    datatypes.JSON `gorm:"type:json"`
	Result     datatypes.JSON `gorm:"type:json"`
	RefID      *uint64
	CreatedAt  time.Time  `gorm:"type:datetime(3);not null;index:idx_jobs_user_created,priority:2"`
	StartedAt  *time.Time `gorm:"type:datetime(3)"`
	FinishedAt *time.Time `gorm:"type:datetime(3)"`
	User       User       `gorm:"constraint:OnDelete:CASCADE"`
}

func (Job) TableName() string { return "jobs" }

// Audiobook is a produced audio file (plus subtitles) for one book.
type Audiobook struct {
	ID            uint64  `gorm:"primaryKey;autoIncrement"`
	UserID        uint64  `gorm:"not null;index:idx_audiobooks_user,priority:1"`
	BookID        uint64  `gorm:"not null"`
	Model         string  `gorm:"size:64;not null"`
	Voice         string  `gorm:"size:64;not null"`
	Speed         float64 `gorm:"type:decimal(3,2);not null;default:1.00"`
	Instruct      string  `gorm:"size:512"`
	FallbackModel string  `gorm:"size:64"`
	Status        string  `gorm:"size:16;not null"`
	Progress      uint8   `gorm:"not null;default:0"`
	Error         string  `gorm:"type:text"`
	AudioPath     string  `gorm:"size:1024"`
	SRTPath       string  `gorm:"size:1024"`
	ManifestPath  string  `gorm:"size:1024"`
	Format        string  `gorm:"size:8"`
	DurationS     float64 `gorm:"type:decimal(10,3)"`
	SizeBytes     uint64
	Segments      uint32
	Chars         uint32
	CreatedAt     time.Time  `gorm:"type:datetime(3);not null;index:idx_audiobooks_user,priority:2"`
	FinishedAt    *time.Time `gorm:"type:datetime(3)"`
	User          User       `gorm:"constraint:OnDelete:CASCADE"`
	Book          Book       `gorm:"constraint:OnDelete:CASCADE"`
}

func (Audiobook) TableName() string { return "audiobooks" }

// UserSettings holds per-user defaults for audiobook conversion.
type UserSettings struct {
	UserID          uint64  `gorm:"primaryKey;autoIncrement:false"`
	DefaultModel    string  `gorm:"size:64"`
	DefaultVoice    string  `gorm:"size:64"`
	DefaultSpeed    float64 `gorm:"type:decimal(3,2);default:1.00"`
	DefaultInstruct string  `gorm:"size:512"`
	FallbackModel   string  `gorm:"size:64"`
	AllowEdge       bool    `gorm:"not null;default:true"`
	User            User    `gorm:"constraint:OnDelete:CASCADE"`
}

func (UserSettings) TableName() string { return "user_settings" }

// Job kinds and statuses.
const (
	JobKindDownload  = "download"
	JobKindAudiobook = "audiobook"

	JobQueued     = "queued"
	JobRunning    = "running"
	JobCancelling = "cancelling"
	JobDone       = "done"
	JobFailed     = "failed"
	JobCancelled  = "cancelled"
)
