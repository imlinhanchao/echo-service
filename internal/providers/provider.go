// Package providers defines the pluggable book-source contract. A source such
// as zlib implements Provider and is registered at startup; adding another
// source (Anna's Archive, Gutenberg, ...) means implementing this interface.
package providers

import (
	"context"
	"errors"
)

// Credentials is a provider-specific bag of secrets (never logged, never
// returned to clients).
type Credentials map[string]string

// SearchQuery is a normalized search request.
type SearchQuery struct {
	Q          string
	Page       int
	Limit      int
	Extensions []string
	Languages  []string
	YearFrom   int
	YearTo     int
	Order      string
}

// Book is a normalized search result / detail record.
type Book struct {
	Provider    string   `json:"provider"`
	Ref         string   `json:"ref"`
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	Year        string   `json:"year"`
	Publisher   string   `json:"publisher"`
	Language    string   `json:"language"`
	Format      string   `json:"format"`
	Size        string   `json:"size"`
	ISBN        string   `json:"isbn"`
	CoverURL    string   `json:"cover_url"`
	Description string   `json:"description"`
	SourceURL   string   `json:"source_url"`
}

// SearchResult is one page of search results.
type SearchResult struct {
	Books      []Book `json:"books"`
	Page       int    `json:"page"`
	TotalPages int    `json:"total_pages"`
}

// AccountInfo is the non-secret account summary shown to the user.
type AccountInfo struct {
	Email          string `json:"email"`
	DailyUsed      int    `json:"daily_used"`
	DailyLimit     int    `json:"daily_limit"`
	DailyRemaining int    `json:"daily_remaining"`
	Premium        bool   `json:"premium"`
	QuotaKnown     bool   `json:"quota_known"`
}

// DownloadResult describes a saved file.
type DownloadResult struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Format   string `json:"format"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

// ProgressFunc reports bytes written / total bytes (total may be -1).
type ProgressFunc func(written, total int64)

// Provider is the pluggable book-source interface.
type Provider interface {
	ID() string
	DisplayName() string
	Capabilities() []string
	Verify(ctx context.Context, creds Credentials) (AccountInfo, error)
	Search(ctx context.Context, creds Credentials, q SearchQuery) (SearchResult, error)
	GetBook(ctx context.Context, creds Credentials, ref string) (Book, error)
	Download(ctx context.Context, creds Credentials, ref, destDir string, progress ProgressFunc) (DownloadResult, error)
}

// Provider metadata exposed to the frontend.
type ProviderInfo struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Capabilities   []string `json:"capabilities"`
	DefaultDomain  string   `json:"default_domain"`
	Domains        []string `json:"domains"`
	CredentialKeys []string `json:"credential_keys"`
}

// Typed errors shared by all providers; the API layer maps them to HTTP codes.
var (
	ErrNotConfigured       = errors.New("provider not configured")
	ErrSessionExpired      = errors.New("provider session expired")
	ErrBlocked             = errors.New("provider blocked by bot protection")
	ErrQuotaExceeded       = errors.New("download quota exceeded")
	ErrNotFound            = errors.New("not found")
	ErrProviderUnavailable = errors.New("provider unavailable")
	ErrUnsupported         = errors.New("unsupported")
)
