package zlib

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/imlinhanchao/echo-service/internal/providers"
)

const (
	providerID   = "zlib"
	providerName = "Z-Library"

	// A current desktop Chrome user agent. DiamWall is far more likely to serve
	// an HTML challenge to a request that announces itself as a bot.
	httpUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"

	defaultDomain = "https://z-lib.gd"

	eapiLoginPath       = "/eapi/user/login"
	eapiSearchPath      = "/eapi/book/search"
	eapiProfilePath     = "/eapi/user/profile"
	eapiDomainsInfoPath = "/eapi/info/domains"

	eapiTimeout          = 45 * time.Second
	probeTimeout         = 10 * time.Second
	domainHealthTTL      = 10 * time.Minute
	maxEAPIResponseBytes = 16 << 20

	// maxDownloadRetries is the number of retries after the first download
	// attempt; the delays before them are 0.5s, 1s and 2s.
	maxDownloadRetries = 3
	downloadChunkSize  = 32 * 1024
	maxFilenameRunes   = 180
)

var (
	// downloadRetryBase is the first retry delay; it doubles after every
	// failed attempt. It is a var so tests can shorten it.
	downloadRetryBase = 500 * time.Millisecond
	// downloadStallTimeout bounds how long a download may go without data.
	// A transfer that keeps making progress is never cut off.
	downloadStallTimeout = 2 * time.Minute
)

// errDownloadStalled is the cancellation cause recorded when a download stops
// delivering data for downloadStallTimeout.
var errDownloadStalled = errors.New("download stalled")

// client speaks the JSON EAPI of one Z-Library mirror. It carries the remix
// session cookies, if any, on every request.
type client struct {
	domain  string
	userID  string
	userKey string
	http    *http.Client
}

func newClient(domain, userID, userKey string, hc *http.Client) *client {
	if hc == nil {
		hc = &http.Client{Timeout: eapiTimeout}
	}
	return &client{
		domain:  strings.TrimRight(domain, "/"),
		userID:  strings.TrimSpace(userID),
		userKey: strings.TrimSpace(userKey),
		http:    hc,
	}
}

func (c *client) do(ctx context.Context, method, apiPath string, values url.Values) ([]byte, error) {
	return c.doWith(ctx, method, apiPath, values, false)
}

// doWith performs one EAPI request and classifies its response. login exempts
// the login endpoint from session-expiry classification: there the same
// envelope describes the submitted credentials, not a saved session.
func (c *client) doWith(ctx context.Context, method, apiPath string, values url.Values, login bool) ([]byte, error) {
	endpoint := c.domain + apiPath
	var requestBody io.Reader
	if method == http.MethodGet || method == http.MethodHead {
		if len(values) > 0 {
			separator := "?"
			if strings.Contains(endpoint, "?") {
				separator = "&"
			}
			endpoint += separator + values.Encode()
		}
	} else {
		requestBody = strings.NewReader(values.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, requestBody)
	if err != nil {
		return nil, fmt.Errorf("zlib: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("User-Agent", httpUserAgent)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c.userID != "" {
		// Z-Library mirrors have been observed reading either spelling, so
		// send both the hyphenated header and the cookie-style header.
		req.Header.Set("remix-userid", c.userID)
		req.Header.Set("remix_userid", c.userID)
		req.AddCookie(&http.Cookie{Name: "remix_userid", Value: c.userID})
	}
	if c.userKey != "" {
		req.Header.Set("remix-userkey", c.userKey)
		req.Header.Set("remix_userkey", c.userKey)
		req.AddCookie(&http.Cookie{Name: "remix_userkey", Value: c.userKey})
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", providers.ErrProviderUnavailable, err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxEAPIResponseBytes))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: read response: %v", providers.ErrProviderUnavailable, err)
	}
	status := resp.StatusCode

	// DiamWall serves an HTML challenge (often a meta refresh or redirect)
	// instead of JSON for a blocked client.
	if isBotProtectionResponse(status, resp.Header, responseBody) {
		return nil, fmt.Errorf("%w: HTTP %d at %s", providers.ErrBlocked, status, c.domain)
	}

	envelope := decodeEnvelope(responseBody)

	if login {
		if status >= http.StatusInternalServerError {
			return nil, fmt.Errorf("%w: HTTP %d at %s", providers.ErrProviderUnavailable, status, c.domain)
		}
		if status >= http.StatusBadRequest && envelope == nil {
			return nil, classifyHTTPStatus(status, c.domain)
		}
		// The login envelope is parsed by login, which reports the server's
		// own message ("Invalid email or password").
		return responseBody, nil
	}

	// A revoked or expired remix_userkey is answered with HTTP 400 and
	// {"success":0,"error":"Invalid credentials"} (or "Please login").
	if status == http.StatusBadRequest && c.userKey != "" && envelope != nil &&
		envelope.hasSuccess() && envelope.successValue() == 0 && isSessionRejection(envelope.errorMessage()) {
		return nil, fmt.Errorf("%w: %s", providers.ErrSessionExpired, envelope.errorMessage())
	}

	if status >= http.StatusInternalServerError {
		return nil, fmt.Errorf("%w: HTTP %d at %s", providers.ErrProviderUnavailable, status, c.domain)
	}
	if envelope != nil && envelope.hasSuccess() && envelope.successValue() != 1 {
		return nil, classifyEAPIError(envelope.errorMessage())
	}
	if status >= http.StatusBadRequest {
		return nil, classifyHTTPStatus(status, c.domain)
	}
	return responseBody, nil
}

// login exchanges an email and password for the remix session credentials.
func (c *client) login(ctx context.Context, email, password string) (userID, userKey string, err error) {
	body, err := c.doWith(ctx, http.MethodPost, eapiLoginPath, url.Values{
		"email":    {email},
		"password": {password},
	}, true)
	if err != nil {
		return "", "", err
	}

	var result struct {
		Success any    `json:"success"`
		Error   string `json:"error"`
		User    struct {
			ID             any    `json:"id"`
			RemixUserKey   string `json:"remix_userkey"`
			LegacyRemixKey string `json:"remix_user_key"`
		} `json:"user"`
	}
	if err := decodeJSON(body, &result); err != nil {
		return "", "", fmt.Errorf("zlib: invalid login response: %w", err)
	}
	if anyInt(result.Success) != 1 {
		message := strings.TrimSpace(result.Error)
		if message == "" {
			message = "login was rejected"
		}
		return "", "", fmt.Errorf("zlib: login failed: %s", message)
	}
	id := anyString(result.User.ID)
	key := strings.TrimSpace(result.User.RemixUserKey)
	if key == "" {
		key = strings.TrimSpace(result.User.LegacyRemixKey)
	}
	if id == "" || key == "" {
		return "", "", errors.New("zlib: login response did not include remix credentials")
	}
	return id, key, nil
}

// eapiEnvelope is the {"success":..,"error":".."} wrapper shared by EAPI
// responses.
type eapiEnvelope struct {
	Success any    `json:"success"`
	Error   string `json:"error"`
}

func decodeEnvelope(body []byte) *eapiEnvelope {
	body = bytes.TrimPrefix(bytes.TrimSpace(body), []byte("\xef\xbb\xbf"))
	if len(body) == 0 || body[0] != '{' {
		return nil
	}
	var envelope eapiEnvelope
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return nil
	}
	if envelope.Success == nil && envelope.Error == "" {
		return nil
	}
	return &envelope
}

func (e *eapiEnvelope) hasSuccess() bool {
	return e != nil && e.Success != nil
}

func (e *eapiEnvelope) successValue() int {
	if e == nil {
		return 0
	}
	return anyInt(e.Success)
}

func (e *eapiEnvelope) errorMessage() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.Error)
}

// eapiBook is a book record from a search or detail response. Scalar fields
// that mirrors answer as either a number or a string use any.
type eapiBook struct {
	ID             any    `json:"id"`
	Hash           string `json:"hash"`
	Title          string `json:"title"`
	Author         string `json:"author"`
	Year           any    `json:"year"`
	Publisher      string `json:"publisher"`
	Language       string `json:"language"`
	Extension      string `json:"extension"`
	Filesize       any    `json:"filesize"`
	FilesizeString string `json:"filesizeString"`
	Identifier     any    `json:"identifier"`
	ISBN           any    `json:"isbn"`
	Cover          string `json:"cover"`
	Href           string `json:"href"`
	DownloadPath   string `json:"dl"`
	Description    string `json:"description"`
}

// parseSearchResponse decodes /eapi/book/search.
func parseSearchResponse(body []byte, requestedPage int) (books []eapiBook, page, totalPages int, err error) {
	var result struct {
		Success    any        `json:"success"`
		Error      string     `json:"error"`
		Books      []eapiBook `json:"books"`
		Pagination struct {
			Current    int `json:"current"`
			TotalPages int `json:"total_pages"`
		} `json:"pagination"`
	}
	if err := decodeJSON(body, &result); err != nil {
		return nil, 0, 0, fmt.Errorf("zlib: invalid search response: %w", err)
	}
	if anyInt(result.Success) != 1 {
		return nil, 0, 0, classifyEAPIError(result.Error)
	}
	page = result.Pagination.Current
	if page == 0 {
		page = requestedPage
	}
	return result.Books, page, result.Pagination.TotalPages, nil
}

// parseBookResponse decodes /eapi/book/{id}/{hash}.
func parseBookResponse(body []byte, id, hash string) (eapiBook, error) {
	var result struct {
		Success any       `json:"success"`
		Error   string    `json:"error"`
		Book    *eapiBook `json:"book"`
	}
	if err := decodeJSON(body, &result); err != nil {
		return eapiBook{}, fmt.Errorf("zlib: invalid book response: %w", err)
	}
	if anyInt(result.Success) != 1 {
		return eapiBook{}, classifyEAPIError(result.Error)
	}
	if result.Book == nil {
		return eapiBook{}, fmt.Errorf("%w: book response is missing the book", providers.ErrNotFound)
	}
	book := *result.Book
	if anyString(book.ID) == "" {
		book.ID = id
	}
	if strings.TrimSpace(book.Hash) == "" {
		book.Hash = hash
	}
	return book, nil
}

// parseFileLink decodes /eapi/book/{id}/{hash}/file. Mirrors have used several
// shapes for the link over time, so accept all of them.
func parseFileLink(body []byte) (string, error) {
	var result struct {
		Success any    `json:"success"`
		Error   string `json:"error"`
		File    struct {
			DownloadLink string `json:"downloadLink"`
		} `json:"file"`
		DownloadLink string `json:"downloadLink"`
		URL          string `json:"url"`
		Link         string `json:"link"`
	}
	if err := decodeJSON(body, &result); err != nil {
		return "", fmt.Errorf("zlib: invalid file response: %w", err)
	}
	if anyInt(result.Success) != 1 {
		return "", classifyEAPIError(result.Error)
	}
	for _, link := range []string{result.File.DownloadLink, result.DownloadLink, result.URL, result.Link} {
		if link = strings.TrimSpace(link); link != "" {
			return link, nil
		}
	}
	return "", fmt.Errorf("%w: file response did not include a download link", providers.ErrNotFound)
}

// eapiProfile is the account summary returned by /eapi/user/profile.
type eapiProfile struct {
	Email      string
	Used       int
	Limit      int
	LimitKnown bool
	Premium    bool
}

func parseProfile(body []byte) (eapiProfile, error) {
	var result struct {
		Success any    `json:"success"`
		Error   string `json:"error"`
		User    *struct {
			Email               string `json:"email"`
			DownloadsToday      any    `json:"downloads_today"`
			DownloadsLimit      any    `json:"downloads_limit"`
			DailyDownloadsCount any    `json:"dailyDownloadsCount"`
			DailyDownloadLimit  any    `json:"dailyDownloadLimit"`
			Premium             any    `json:"isPremium"`
		} `json:"user"`
	}
	if err := decodeJSON(body, &result); err != nil {
		return eapiProfile{}, fmt.Errorf("zlib: invalid profile response: %w", err)
	}
	if anyInt(result.Success) != 1 {
		return eapiProfile{}, classifyEAPIError(result.Error)
	}
	if result.User == nil {
		return eapiProfile{}, errors.New("zlib: profile response is missing the user")
	}

	profile := eapiProfile{
		Email:   strings.TrimSpace(result.User.Email),
		Premium: anyBool(result.User.Premium),
	}
	if value, ok := firstPresent(result.User.DownloadsToday, result.User.DailyDownloadsCount); ok {
		profile.Used = anyInt(value)
	}
	if value, ok := firstPresent(result.User.DownloadsLimit, result.User.DailyDownloadLimit); ok {
		profile.Limit, profile.LimitKnown = anyIntOK(value)
	}
	return profile, nil
}

func firstPresent(values ...any) (any, bool) {
	for _, value := range values {
		if value != nil {
			return value, true
		}
	}
	return nil, false
}

// probeDomain performs the unauthenticated mirror health check.
func probeDomain(ctx context.Context, domain string, hc *http.Client) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, domain+eapiDomainsInfoPath, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", httpUserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode == http.StatusOK
}

// isBotProtectionResponse reports whether an EAPI response is an anti-bot
// challenge. Only 403 and 503 qualify: a JSON 503 is a genuine gateway error.
func isBotProtectionResponse(status int, header http.Header, body []byte) bool {
	if status != http.StatusForbidden && status != http.StatusServiceUnavailable {
		return false
	}
	if header != nil && header.Get("Location") != "" {
		return true
	}
	if header != nil {
		contentType := strings.ToLower(header.Get("Content-Type"))
		if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml") {
			return true
		}
	}
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '<'
}

// classifyEAPIError maps the server's {"success":0,"error":"..."} message to
// the shared typed errors.
func classifyEAPIError(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return errors.New("zlib: request rejected by server")
	}
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "daily limit"),
		strings.Contains(lower, "limit reached"),
		strings.Contains(lower, "quota"):
		return fmt.Errorf("%w: %s", providers.ErrQuotaExceeded, message)
	case isSessionRejection(message),
		strings.Contains(lower, "unauthorized"):
		return fmt.Errorf("%w: %s", providers.ErrSessionExpired, message)
	case strings.Contains(lower, "not found"):
		return fmt.Errorf("%w: %s", providers.ErrNotFound, message)
	default:
		return fmt.Errorf("zlib: %s", message)
	}
}

func isSessionRejection(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	switch lower {
	case "invalid credentials", "please login", "not authorized", "unauthorized", "session expired":
		return true
	}
	return strings.Contains(lower, "invalid credentials") ||
		strings.Contains(lower, "please login") ||
		strings.Contains(lower, "not authorized") ||
		strings.Contains(lower, "session expired")
}

func classifyHTTPStatus(status int, domain string) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: HTTP %d at %s", providers.ErrSessionExpired, status, domain)
	case http.StatusNotFound:
		return fmt.Errorf("%w: HTTP %d at %s", providers.ErrNotFound, status, domain)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: HTTP %d at %s", providers.ErrQuotaExceeded, status, domain)
	}
	if status >= http.StatusInternalServerError {
		return fmt.Errorf("%w: HTTP %d at %s", providers.ErrProviderUnavailable, status, domain)
	}
	return fmt.Errorf("zlib: unexpected HTTP %d at %s", status, domain)
}

// decodeJSON decodes an EAPI JSON document. UseNumber keeps large numeric IDs
// exact instead of routing them through float64.
func decodeJSON(body []byte, target any) error {
	body = bytes.TrimPrefix(bytes.TrimSpace(body), []byte("\xef\xbb\xbf"))
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func anyString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return ""
	}
}

func anyInt(value any) int {
	number, _ := anyIntOK(value)
	return number
}

func anyIntOK(value any) (int, bool) {
	switch typed := value.(type) {
	case nil:
		return 0, false
	case json.Number:
		if number, err := typed.Int64(); err == nil {
			return int(number), true
		}
		if number, err := typed.Float64(); err == nil {
			return int(number), true
		}
		return 0, false
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	case string:
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return 0, false
		}
		if number, err := strconv.Atoi(typed); err == nil {
			return number, true
		}
		if number, err := strconv.ParseFloat(typed, 64); err == nil {
			return int(number), true
		}
		return 0, false
	default:
		return 0, false
	}
}

func anyBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on", "premium":
			return true
		}
		return false
	default:
		number, ok := anyIntOK(value)
		return ok && number != 0
	}
}

// contentTypeExtensions maps download response media types to a file
// extension when the server does not name the file.
var contentTypeExtensions = map[string]string{
	"application/pdf":                "pdf",
	"application/epub+zip":           "epub",
	"application/x-mobipocket-ebook": "mobi",
	"application/vnd.amazon.ebook":   "azw3",
	"application/x-fictionbook+xml":  "fb2",
	"application/x-fictionbook":      "fb2",
	"application/x-djvu":             "djvu",
	"image/vnd.djvu":                 "djvu",
	"application/rtf":                "rtf",
	"text/plain":                     "txt",
	"text/html":                      "html",
	"application/zip":                "zip",
	"application/x-rar-compressed":   "rar",
	"application/msword":             "doc",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
}

func extFromContentType(contentType string) string {
	if strings.TrimSpace(contentType) == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		if index := strings.Index(contentType, ";"); index >= 0 {
			contentType = contentType[:index]
		}
		mediaType = strings.ToLower(strings.TrimSpace(contentType))
	}
	return contentTypeExtensions[strings.ToLower(mediaType)]
}

// filenameFromResponse extracts a server-provided filename from
// Content-Disposition, preferring the RFC 5987 filename* form.
func filenameFromResponse(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	disposition := strings.TrimSpace(resp.Header.Get("Content-Disposition"))
	if disposition == "" {
		return ""
	}
	if _, params, err := mime.ParseMediaType(disposition); err == nil {
		if name := strings.TrimSpace(params["filename"]); name != "" {
			return name
		}
	}
	for _, part := range strings.Split(disposition, ";") {
		part = strings.TrimSpace(part)
		lower := strings.ToLower(part)
		switch {
		case strings.HasPrefix(lower, "filename*="):
			value := part[len("filename*="):]
			if index := strings.Index(value, "''"); index >= 0 {
				value = value[index+2:]
			}
			value = strings.Trim(value, `"'`)
			if decoded, err := url.QueryUnescape(value); err == nil {
				value = decoded
			}
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		case strings.HasPrefix(lower, "filename="):
			value := strings.Trim(strings.TrimSpace(part[len("filename="):]), `"'`)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

// sanitizeFilename makes a server-provided filename safe to join with destDir.
// It drops path components, control characters and bidi overrides, and caps
// the length. It returns "" when nothing usable is left.
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || isBidiControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || name == "/" {
		return ""
	}
	return truncateFilename(name, maxFilenameRunes)
}

func truncateFilename(name string, limit int) string {
	runes := []rune(name)
	if len(runes) <= limit {
		return name
	}
	extension := []rune(filepath.Ext(name))
	if len(extension) >= limit {
		return string(runes[:limit])
	}
	stem := strings.TrimRight(string(runes[:limit-len(extension)]), " .")
	if stem == "" {
		return string(extension)
	}
	return stem + string(extension)
}

func isBidiControl(r rune) bool {
	switch {
	case r >= 0x202a && r <= 0x202e,
		r >= 0x2066 && r <= 0x2069,
		r == 0x200e, r == 0x200f:
		return true
	default:
		return false
	}
}

// extOf returns a lowercase, sanitized extension of name, or "".
func extOf(name string) string {
	extension := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if extension == "" || len(extension) > 12 {
		return ""
	}
	for _, r := range extension {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return extension
}

// absolutize resolves a possibly relative link against the mirror origin.
func absolutize(domain, reference string) string {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return ""
	}
	parsed, err := url.Parse(reference)
	if err != nil {
		return reference
	}
	if parsed.IsAbs() {
		return parsed.String()
	}
	base, err := url.Parse(domain)
	if err != nil {
		return reference
	}
	return base.ResolveReference(parsed).String()
}
