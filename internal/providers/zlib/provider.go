package zlib

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/imlinhanchao/echo-service/internal/providers"
)

// Provider implements providers.Provider (and providers.InfoProvider) for
// Z-Library. It speaks the EAPI protocol over net/http; no external zlib
// binary is ever executed.
type Provider struct {
	domains          []string
	implicitDefault  bool
	maxDownloadBytes int64
	httpClient       *http.Client
	downloadClient   *http.Client

	mu              sync.Mutex
	preferredDomain string
	healthyDomains  map[string]time.Time
}

var _ providers.Provider = (*Provider)(nil)
var _ providers.InfoProvider = (*Provider)(nil)

// New creates a Z-Library provider. domains is the ordered list of mirror
// origins to try. maxDownloadBytes caps a single download; zero or a negative
// value means no limit.
func New(domains []string, maxDownloadBytes int64) *Provider {
	provider := &Provider{
		maxDownloadBytes: maxDownloadBytes,
		healthyDomains:   make(map[string]time.Time),
		httpClient:       &http.Client{Timeout: eapiTimeout},
	}
	for _, raw := range domains {
		domain := normalizeDomain(raw)
		if domain == "" || containsString(provider.domains, domain) {
			continue
		}
		provider.domains = append(provider.domains, domain)
	}
	if len(provider.domains) == 0 {
		provider.domains = []string{defaultDomain}
		provider.implicitDefault = true
	}
	provider.downloadClient = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("zlib: too many redirects")
			}
			if len(via) > 0 && !sameOrigin(req.URL, via[0].URL) {
				// A CDN link needs no account credentials; drop them rather
				// than leak the reusable session to another host.
				req.Header.Del("Cookie")
				req.Header.Del("remix-userid")
				req.Header.Del("remix_userid")
				req.Header.Del("remix-userkey")
				req.Header.Del("remix_userkey")
			}
			return nil
		},
	}
	return provider
}

func (p *Provider) ID() string { return providerID }

func (p *Provider) DisplayName() string { return providerName }

func (p *Provider) Capabilities() []string {
	return []string{"search", "download", "login", "cookies", "quota"}
}

// Info exposes the provider metadata to the API layer.
func (p *Provider) Info() providers.ProviderInfo {
	domains := append([]string(nil), p.domains...)
	return providers.ProviderInfo{
		ID:             providerID,
		Name:           providerName,
		Capabilities:   p.Capabilities(),
		DefaultDomain:  domains[0],
		Domains:        domains,
		CredentialKeys: []string{"domain", "remix_userid", "remix_userkey"},
	}
}

// ProbeDomain checks a mirror without credentials by requesting
// /eapi/info/domains; an HTTP 200 means the mirror is healthy. The result is
// recorded in the ten-minute domain health cache.
func (p *Provider) ProbeDomain(ctx context.Context, domain string) bool {
	normalized := normalizeDomain(domain)
	if normalized == "" {
		return false
	}
	probeCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(ctx, probeTimeout)
		defer cancel()
	}
	if !probeDomain(probeCtx, normalized, p.httpClient) {
		p.markUnhealthy(normalized)
		return false
	}
	p.markHealthy(normalized)
	return true
}

// Login authenticates against one mirror and returns the credentials the API
// layer stores for this account.
func (p *Provider) Login(ctx context.Context, domain, email, password string) (providers.Credentials, error) {
	target := normalizeDomain(domain)
	if target == "" {
		if len(p.domains) == 0 {
			return nil, fmt.Errorf("%w: a Z-Library domain is required", providers.ErrNotConfigured)
		}
		target = p.domains[0]
	}
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return nil, fmt.Errorf("%w: email and password are required", providers.ErrNotConfigured)
	}

	var lastErr error
	for attempt, domain := range p.candidates(providers.Credentials{"domain": target}) {
		if attempt >= 2 {
			break
		}
		account := newClient(domain, "", "", p.httpClient)
		userID, userKey, err := account.login(ctx, email, password)
		if err == nil {
			p.markHealthy(domain)
			return providers.Credentials{
				"domain":        domain,
				"remix_userid":  userID,
				"remix_userkey": userKey,
			}, nil
		}
		if ctx.Err() != nil || !isDomainFailure(err) {
			return nil, err
		}
		lastErr = err
		p.markUnhealthy(domain)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: no Z-Library mirror is available", providers.ErrProviderUnavailable)
	}
	return nil, lastErr
}

// Verify checks stored credentials against /eapi/user/profile.
func (p *Provider) Verify(ctx context.Context, creds providers.Credentials) (providers.AccountInfo, error) {
	return p.VerifyCookies(ctx, credentialDomain(creds), creds["remix_userid"], creds["remix_userkey"])
}

// VerifyCookies validates a remix session and returns the account summary.
func (p *Provider) VerifyCookies(ctx context.Context, domain, userID, userKey string) (providers.AccountInfo, error) {
	target := normalizeDomain(domain)
	if target == "" {
		if len(p.domains) == 0 {
			return providers.AccountInfo{}, fmt.Errorf("%w: a Z-Library domain is required", providers.ErrNotConfigured)
		}
		target = p.domains[0]
	}
	userID = strings.TrimSpace(userID)
	userKey = strings.TrimSpace(userKey)
	if userID == "" || userKey == "" {
		return providers.AccountInfo{}, fmt.Errorf("%w: remix_userid and remix_userkey are required", providers.ErrNotConfigured)
	}

	body, _, err := p.request(ctx, providers.Credentials{
		"domain":        target,
		"remix_userid":  userID,
		"remix_userkey": userKey,
	}, http.MethodGet, eapiProfilePath, nil)
	if err != nil {
		return providers.AccountInfo{}, err
	}
	profile, err := parseProfile(body)
	if err != nil {
		return providers.AccountInfo{}, err
	}

	info := providers.AccountInfo{
		Email:      profile.Email,
		DailyUsed:  profile.Used,
		DailyLimit: profile.Limit,
		Premium:    profile.Premium,
		QuotaKnown: profile.LimitKnown,
	}
	if profile.LimitKnown {
		info.DailyRemaining = profile.Limit - profile.Used
		if info.DailyRemaining < 0 {
			info.DailyRemaining = 0
		}
	}
	return info, nil
}

// Search runs an EAPI book search.
func (p *Provider) Search(ctx context.Context, creds providers.Credentials, query providers.SearchQuery) (providers.SearchResult, error) {
	if strings.TrimSpace(query.Q) == "" {
		return providers.SearchResult{}, errors.New("zlib: search query is empty")
	}
	page := query.Page
	if page < 1 {
		page = 1
	}

	body, domain, err := p.request(ctx, creds, http.MethodPost, eapiSearchPath, searchValues(query))
	if err != nil {
		return providers.SearchResult{}, err
	}
	items, currentPage, totalPages, err := parseSearchResponse(body, page)
	if err != nil {
		return providers.SearchResult{}, err
	}

	books := make([]providers.Book, 0, len(items))
	for _, item := range items {
		books = append(books, mapBook(domain, item))
	}
	return providers.SearchResult{Books: books, Page: currentPage, TotalPages: totalPages}, nil
}

// GetBook fetches the details of one book. ref is "id:hash".
func (p *Provider) GetBook(ctx context.Context, creds providers.Credentials, ref string) (providers.Book, error) {
	reference, err := parseBookRef(ref)
	if err != nil {
		return providers.Book{}, err
	}
	body, domain, err := p.request(ctx, creds, http.MethodGet, bookAPIPath(reference.id, reference.hash), nil)
	if err != nil {
		return providers.Book{}, err
	}
	item, err := parseBookResponse(body, reference.id, reference.hash)
	if err != nil {
		return providers.Book{}, err
	}
	return mapBook(domain, item), nil
}

// Download resolves a book's file link and streams it into destDir. ref is
// "id:hash"; progress may be nil.
func (p *Provider) Download(ctx context.Context, creds providers.Credentials, ref, destDir string, progress providers.ProgressFunc) (providers.DownloadResult, error) {
	reference, err := parseBookRef(ref)
	if err != nil {
		return providers.DownloadResult{}, err
	}
	if reference.hash == "" {
		return providers.DownloadResult{}, fmt.Errorf("%w: a Z-Library download reference must be id:hash", providers.ErrUnsupported)
	}
	if strings.TrimSpace(destDir) == "" {
		return providers.DownloadResult{}, errors.New("zlib: destination directory is required")
	}
	userID, userKey := credentialUser(creds)

	link, mirrorDomain, err := p.resolveDownloadLink(ctx, creds, reference)
	if err != nil {
		return providers.DownloadResult{}, err
	}
	return p.fetchDownload(ctx, link, mirrorDomain, userID, userKey, reference, destDir, progress)
}

// request performs one EAPI call, switching to the next candidate domain once
// when the current one is blocked or unreachable. Successful domains are
// preferred for the next ten minutes.
func (p *Provider) request(ctx context.Context, creds providers.Credentials, method, apiPath string, values url.Values) ([]byte, string, error) {
	userID, userKey := credentialUser(creds)
	var lastErr error

	for attempt, domain := range p.candidates(creds) {
		if attempt >= 2 {
			break
		}
		current := newClient(domain, userID, userKey, p.httpClient)
		body, err := current.do(ctx, method, apiPath, values)
		if err == nil {
			p.markHealthy(domain)
			return body, domain, nil
		}
		if ctx.Err() != nil {
			return nil, domain, err
		}
		lastErr = err
		if !isDomainFailure(err) {
			return nil, domain, err
		}
		p.markUnhealthy(domain)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: no Z-Library mirror is available", providers.ErrProviderUnavailable)
	}
	return nil, "", lastErr
}

// candidates returns the domains to try for this request, most promising
// first. A domain explicitly stored in the credentials always comes first.
func (p *Provider) candidates(creds providers.Credentials) []string {
	explicit := credentialDomain(creds)

	p.mu.Lock()
	preferred := p.preferredDomain
	if preferred != "" {
		if until, ok := p.healthyDomains[preferred]; !ok || time.Now().After(until) {
			preferred = ""
		}
	}
	p.mu.Unlock()

	out := make([]string, 0, len(p.domains)+2)
	add := func(domain string) {
		if domain == "" || containsString(out, domain) {
			return
		}
		out = append(out, domain)
	}
	if explicit != "" {
		add(explicit)
	}
	if preferred != "" {
		add(preferred)
	}
	for _, domain := range p.domains {
		if p.implicitDefault && explicit != "" {
			// Never fall back to the built-in default when the caller named a
			// specific mirror; that could silently reach the public network.
			continue
		}
		add(domain)
	}
	if len(out) == 0 {
		add(defaultDomain)
	}
	return out
}

func (p *Provider) markHealthy(domain string) {
	if domain == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.healthyDomains[domain] = time.Now().Add(domainHealthTTL)
	p.preferredDomain = domain
}

func (p *Provider) markUnhealthy(domain string) {
	if domain == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.healthyDomains, domain)
	if p.preferredDomain == domain {
		p.preferredDomain = ""
	}
}

func isDomainFailure(err error) bool {
	return errors.Is(err, providers.ErrBlocked) || errors.Is(err, providers.ErrProviderUnavailable)
}

// resolveDownloadLink asks the mirror for the real file URL. A response may
// point back at the mirror's own /eapi/.../file endpoint, so follow that up to
// a few times before handing the link to the CDN downloader.
func (p *Provider) resolveDownloadLink(ctx context.Context, creds providers.Credentials, reference bookReference) (string, string, error) {
	apiPath := bookAPIPath(reference.id, reference.hash) + "/file"
	for attempt := 0; attempt < 3; attempt++ {
		body, domain, err := p.request(ctx, creds, http.MethodGet, apiPath, nil)
		if err != nil {
			return "", "", err
		}
		link, err := parseFileLink(body)
		if err != nil {
			return "", "", err
		}
		link = absolutize(domain, link)

		parsed, err := url.Parse(link)
		if err == nil && isOwnFileEndpoint(parsed, domain) {
			apiPath = parsed.RequestURI()
			continue
		}
		return link, domain, nil
	}
	return "", "", errors.New("zlib: too many file-link redirects")
}

func isOwnFileEndpoint(candidate *url.URL, domain string) bool {
	if candidate == nil {
		return false
	}
	base, err := url.Parse(domain)
	if err != nil || !sameOrigin(candidate, base) {
		return false
	}
	return strings.HasPrefix(candidate.Path, "/eapi/book/") && strings.HasSuffix(candidate.Path, "/file")
}

// fetchDownload downloads link with bounded retries, a two-minute stall
// timeout and a hard byte cap, then moves the completed file into place.
func (p *Provider) fetchDownload(ctx context.Context, link, mirrorDomain, userID, userKey string, reference bookReference, destDir string, progress providers.ProgressFunc) (providers.DownloadResult, error) {
	stallCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stallTimer := time.AfterFunc(downloadStallTimeout, func() { cancel(errDownloadStalled) })
	defer stallTimer.Stop()
	touch := func() { stallTimer.Reset(downloadStallTimeout) }

	var lastErr error
	delay := downloadRetryBase
	for attempt := 0; attempt <= maxDownloadRetries; attempt++ {
		if attempt > 0 {
			if err := sleepContext(stallCtx, delay); err != nil {
				return providers.DownloadResult{}, transferContextError(stallCtx)
			}
			delay *= 2
		}

		resp, err := p.openDownload(stallCtx, link, mirrorDomain, userID, userKey)
		if err != nil {
			if stallCtx.Err() != nil {
				return providers.DownloadResult{}, transferContextError(stallCtx)
			}
			lastErr = err
			continue
		}
		if isRetryableStatus(resp.StatusCode) {
			lastErr = fmt.Errorf("%w: HTTP %d", providers.ErrProviderUnavailable, resp.StatusCode)
			drainAndClose(resp.Body)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			statusErr := classifyDownloadStatus(resp.StatusCode, resp.Header)
			drainAndClose(resp.Body)
			return providers.DownloadResult{}, statusErr
		}

		result, saveErr := p.saveDownload(stallCtx, resp, destDir, reference, touch, progress)
		_ = resp.Body.Close()
		if saveErr == nil {
			return result, nil
		}
		if stallCtx.Err() != nil {
			return providers.DownloadResult{}, transferContextError(stallCtx)
		}
		if errors.Is(saveErr, providers.ErrProviderUnavailable) {
			lastErr = saveErr
			continue
		}
		return providers.DownloadResult{}, saveErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: download failed", providers.ErrProviderUnavailable)
	}
	return providers.DownloadResult{}, lastErr
}

func (p *Provider) openDownload(ctx context.Context, link, mirrorDomain, userID, userKey string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, fmt.Errorf("zlib: build download request: %w", err)
	}
	req.Header.Set("User-Agent", httpUserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "identity")
	if mirrorDomain != "" {
		req.Header.Set("Referer", strings.TrimRight(mirrorDomain, "/")+"/")
	}
	// Only the mirror itself gets the session; a CDN link is signed and must
	// never receive a reusable credential.
	if sameOriginString(req.URL, mirrorDomain) {
		if userID != "" {
			req.Header.Set("remix-userid", userID)
			req.AddCookie(&http.Cookie{Name: "remix_userid", Value: userID})
		}
		if userKey != "" {
			req.Header.Set("remix-userkey", userKey)
			req.AddCookie(&http.Cookie{Name: "remix_userkey", Value: userKey})
		}
	}

	resp, err := p.downloadClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", providers.ErrProviderUnavailable, err)
	}
	return resp, nil
}

// saveDownload streams a 200 response into a temporary .part file, hashes it
// and renames it into destDir only once it is complete.
func (p *Provider) saveDownload(ctx context.Context, resp *http.Response, destDir string, reference bookReference, touch func(), progress providers.ProgressFunc) (providers.DownloadResult, error) {
	filename := sanitizeFilename(filenameFromResponse(resp))
	format := extOf(filename)
	if format == "" {
		format = extFromContentType(resp.Header.Get("Content-Type"))
	}
	if format == "" {
		format = reference.format
	}
	if format == "" {
		format = "bin"
	}
	if filename == "" {
		base := reference.id
		if reference.hash != "" {
			base += "_" + reference.hash
		}
		filename = sanitizeFilename(base + "." + format)
	}
	if filename == "" {
		filename = "download." + format
	}

	total := resp.ContentLength
	if total <= 0 {
		total = -1
	}
	if p.maxDownloadBytes > 0 && total > p.maxDownloadBytes {
		return providers.DownloadResult{}, fmt.Errorf("zlib: file is %d bytes, larger than the %d byte limit", total, p.maxDownloadBytes)
	}

	tmp, err := os.CreateTemp(destDir, ".zlib-*.part")
	if err != nil {
		return providers.DownloadResult{}, fmt.Errorf("zlib: create temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	writer := io.MultiWriter(tmp, hasher)
	var written int64
	if progress != nil {
		progress(0, total)
	}
	buffer := make([]byte, downloadChunkSize)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			if p.maxDownloadBytes > 0 && written+int64(n) > p.maxDownloadBytes {
				return providers.DownloadResult{}, fmt.Errorf("zlib: download exceeded the %d byte limit", p.maxDownloadBytes)
			}
			nw, writeErr := writer.Write(buffer[:n])
			written += int64(nw)
			if writeErr != nil {
				return providers.DownloadResult{}, fmt.Errorf("zlib: write download: %w", writeErr)
			}
			if touch != nil {
				touch()
			}
			if progress != nil {
				progress(written, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return providers.DownloadResult{}, ctx.Err()
			}
			return providers.DownloadResult{}, fmt.Errorf("%w: read download: %v", providers.ErrProviderUnavailable, readErr)
		}
	}

	// CreateTemp creates the file with 0600; give the finished book the
	// ordinary permissions a shared library directory expects.
	if err := tmp.Chmod(0o644); err != nil {
		return providers.DownloadResult{}, fmt.Errorf("zlib: chmod download: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return providers.DownloadResult{}, fmt.Errorf("zlib: close download: %w", err)
	}
	finalPath := filepath.Join(destDir, filename)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return providers.DownloadResult{}, fmt.Errorf("zlib: move download into place: %w", err)
	}
	committed = true

	return providers.DownloadResult{
		Path:     finalPath,
		Filename: filename,
		Format:   format,
		Size:     written,
		SHA256:   hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func classifyDownloadStatus(status int, header http.Header) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		if header != nil && strings.Contains(strings.ToLower(header.Get("Content-Type")), "text/html") {
			return fmt.Errorf("%w: HTTP %d", providers.ErrBlocked, status)
		}
		return fmt.Errorf("%w: HTTP %d", providers.ErrSessionExpired, status)
	case http.StatusNotFound:
		return fmt.Errorf("%w: download link not found", providers.ErrNotFound)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: HTTP %d", providers.ErrQuotaExceeded, status)
	}
	if status >= http.StatusInternalServerError {
		return fmt.Errorf("%w: HTTP %d", providers.ErrProviderUnavailable, status)
	}
	return fmt.Errorf("zlib: download failed: HTTP %d", status)
}

func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func transferContextError(ctx context.Context) error {
	if errors.Is(context.Cause(ctx), errDownloadStalled) {
		return fmt.Errorf("%w: no data received for %s", providers.ErrProviderUnavailable, downloadStallTimeout)
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	return context.Canceled
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64*1024))
	_ = body.Close()
}

func searchValues(query providers.SearchQuery) url.Values {
	page := query.Page
	if page < 1 {
		page = 1
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	values := url.Values{
		"message": {query.Q},
		"page":    {strconv.Itoa(page)},
		"limit":   {strconv.Itoa(limit)},
	}
	if query.YearFrom > 0 {
		values.Set("yearFrom", strconv.Itoa(query.YearFrom))
	}
	if query.YearTo > 0 {
		values.Set("yearTo", strconv.Itoa(query.YearTo))
	}
	for _, language := range query.Languages {
		if language = strings.TrimSpace(language); language != "" {
			values.Add("languages[]", strings.ToLower(language))
		}
	}
	for _, extension := range query.Extensions {
		if extension = strings.TrimSpace(extension); extension != "" {
			values.Add("extensions[]", strings.ToLower(extension))
		}
	}
	if query.Order != "" {
		values.Set("order", query.Order)
	}
	return values
}

func mapBook(domain string, item eapiBook) providers.Book {
	id := anyString(item.ID)
	hash := strings.TrimSpace(item.Hash)

	size := strings.TrimSpace(item.FilesizeString)
	if size == "" {
		size = anyString(item.Filesize)
	}
	isbn := strings.TrimSpace(anyString(item.Identifier))
	if isbn == "" {
		isbn = strings.TrimSpace(anyString(item.ISBN))
	}

	book := providers.Book{
		Provider:    providerID,
		Ref:         bookRefString(id, hash),
		Title:       item.Title,
		Authors:     splitAuthors(item.Author),
		Year:        anyString(item.Year),
		Publisher:   item.Publisher,
		Language:    item.Language,
		Format:      strings.ToLower(strings.TrimPrefix(strings.TrimSpace(item.Extension), ".")),
		Size:        size,
		ISBN:        isbn,
		CoverURL:    absolutize(domain, item.Cover),
		Description: item.Description,
		SourceURL:   absolutize(domain, item.Href),
	}
	if book.SourceURL == "" && id != "" {
		book.SourceURL = strings.TrimRight(domain, "/") + "/book/" + url.PathEscape(id)
		if hash != "" {
			book.SourceURL += "/" + url.PathEscape(hash)
		}
	}
	return book
}

func splitAuthors(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	normalized := strings.NewReplacer(
		";", ",",
		" & ", ",",
		" and ", ",",
		"|", ",",
	).Replace(raw)
	parts := strings.Split(normalized, ",")
	authors := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			authors = append(authors, part)
		}
	}
	if len(authors) == 0 {
		return []string{raw}
	}
	return authors
}

type bookReference struct {
	id     string
	hash   string
	format string
}

func parseBookRef(raw string) (bookReference, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return bookReference{}, fmt.Errorf("%w: empty book reference", providers.ErrNotFound)
	}
	parts := strings.Split(raw, ":")
	reference := bookReference{id: strings.TrimSpace(parts[0])}
	if reference.id == "" {
		return bookReference{}, fmt.Errorf("%w: book reference must be id:hash", providers.ErrNotFound)
	}
	if len(parts) >= 2 {
		reference.hash = strings.TrimSpace(parts[1])
	}
	if len(parts) >= 3 {
		reference.format = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(parts[2]), "."))
	}
	if len(parts) >= 2 && reference.hash == "" {
		return bookReference{}, fmt.Errorf("%w: book reference must be id:hash", providers.ErrNotFound)
	}
	return reference, nil
}

func bookAPIPath(id, hash string) string {
	apiPath := "/eapi/book/" + url.PathEscape(id)
	if hash != "" {
		apiPath += "/" + url.PathEscape(hash)
	}
	return apiPath
}

func bookRefString(id, hash string) string {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return id
	}
	return id + ":" + hash
}

func credentialDomain(creds providers.Credentials) string {
	if creds == nil {
		return ""
	}
	return normalizeDomain(creds["domain"])
}

func credentialUser(creds providers.Credentials) (string, string) {
	if creds == nil {
		return "", ""
	}
	return strings.TrimSpace(creds["remix_userid"]), strings.TrimSpace(creds["remix_userkey"])
}

// normalizeDomain turns a configured mirror into a scheme-qualified origin
// without a trailing slash.
func normalizeDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func sameOriginString(candidate *url.URL, domain string) bool {
	base, err := url.Parse(domain)
	if err != nil {
		return false
	}
	return sameOrigin(candidate, base)
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
