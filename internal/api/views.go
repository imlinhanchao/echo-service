package api

import (
	"encoding/json"
	"time"

	"github.com/imlinhanchao/echo-service/internal/store"
	"github.com/imlinhanchao/echo-service/internal/store/models"
)

type userView struct {
	ID    uint64 `json:"id"`
	Email string `json:"email"`
}

func userToView(u *models.User) userView {
	return userView{ID: u.ID, Email: u.Email}
}

type accountView struct {
	Configured     bool       `json:"configured"`
	Status         string     `json:"status,omitempty"`
	Email          string     `json:"email,omitempty"`
	DailyUsed      int        `json:"daily_used"`
	DailyLimit     int        `json:"daily_limit"`
	DailyRemaining int        `json:"daily_remaining"`
	QuotaKnown     bool       `json:"quota_known"`
	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`
}

type providerView struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Capabilities   []string     `json:"capabilities"`
	DefaultDomain  string       `json:"default_domain"`
	Domains        []string     `json:"domains"`
	CredentialKeys []string     `json:"credential_keys"`
	Account        *accountView `json:"account"`
}

type jobView struct {
	ID         uint64          `json:"id"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Progress   uint8           `json:"progress"`
	Error      string          `json:"error,omitempty"`
	RefID      *uint64         `json:"ref_id,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
}

func jobToView(j *models.Job) jobView {
	v := jobView{
		ID:         j.ID,
		Kind:       j.Kind,
		Status:     j.Status,
		Progress:   j.Progress,
		Error:      j.Error,
		RefID:      j.RefID,
		CreatedAt:  j.CreatedAt,
		StartedAt:  j.StartedAt,
		FinishedAt: j.FinishedAt,
	}
	if len(j.Result) > 0 {
		v.Result = json.RawMessage(j.Result)
	}
	return v
}

type libraryBookView struct {
	ID             uint64    `json:"id"`
	Provider       string    `json:"provider"`
	ProviderBookID string    `json:"provider_book_id"`
	Title          string    `json:"title"`
	Authors        []string  `json:"authors"`
	Year           string    `json:"year"`
	Publisher      string    `json:"publisher"`
	Language       string    `json:"language"`
	Format         string    `json:"format"`
	SizeBytes      uint64    `json:"size_bytes"`
	ISBN           string    `json:"isbn"`
	CoverURL       string    `json:"cover_url"`
	Description    string    `json:"description"`
	SourceURL      string    `json:"source_url"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

func bookToView(b *models.Book) libraryBookView {
	authors := []string{}
	_ = store.DecodeJSON(b.Authors, &authors)
	if authors == nil {
		authors = []string{}
	}
	return libraryBookView{
		ID:             b.ID,
		Provider:       b.Provider,
		ProviderBookID: b.ProviderBookID,
		Title:          b.Title,
		Authors:        authors,
		Year:           b.Year,
		Publisher:      b.Publisher,
		Language:       b.Language,
		Format:         b.Format,
		SizeBytes:      b.SizeBytes,
		ISBN:           b.ISBN,
		CoverURL:       b.CoverURL,
		Description:    b.Description,
		SourceURL:      b.SourceURL,
		Status:         b.Status,
		CreatedAt:      b.CreatedAt,
	}
}

type audiobookView struct {
	ID          uint64     `json:"id"`
	BookID      uint64     `json:"book_id"`
	BookTitle   string     `json:"book_title,omitempty"`
	Model       string     `json:"model"`
	Voice       string     `json:"voice"`
	Speed       float64    `json:"speed"`
	Instruct    string     `json:"instruct,omitempty"`
	Status      string     `json:"status"`
	Progress    uint8      `json:"progress"`
	Error       string     `json:"error,omitempty"`
	Format      string     `json:"format,omitempty"`
	DurationS   float64    `json:"duration_s"`
	SizeBytes   uint64     `json:"size_bytes"`
	Segments    uint32     `json:"segments"`
	Chars       uint32     `json:"chars"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	AudioURL    string     `json:"audio_url,omitempty"`
	SRTURL      string     `json:"srt_url,omitempty"`
	ChaptersURL string     `json:"chapters_url,omitempty"`
}

func audiobookToView(a *models.Audiobook) audiobookView {
	v := audiobookView{
		ID:         a.ID,
		BookID:     a.BookID,
		Model:      a.Model,
		Voice:      a.Voice,
		Speed:      a.Speed,
		Instruct:   a.Instruct,
		Status:     a.Status,
		Progress:   a.Progress,
		Error:      a.Error,
		Format:     a.Format,
		DurationS:  a.DurationS,
		SizeBytes:  a.SizeBytes,
		Segments:   a.Segments,
		Chars:      a.Chars,
		CreatedAt:  a.CreatedAt,
		FinishedAt: a.FinishedAt,
	}
	if a.AudioPath != "" {
		v.AudioURL = "/api/files/audiobooks/" + itoa(a.ID) + "?variant=audio"
	}
	if a.SRTPath != "" {
		v.SRTURL = "/api/files/audiobooks/" + itoa(a.ID) + "?variant=srt"
	}
	if a.AudioPath != "" || a.ManifestPath != "" {
		v.ChaptersURL = "/api/audiobooks/" + itoa(a.ID) + "/chapters"
	}
	return v
}

func itoa(v uint64) string {
	return jsonNumber(v)
}

func jsonNumber(v uint64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
