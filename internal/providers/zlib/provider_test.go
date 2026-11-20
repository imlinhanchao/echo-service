package zlib

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imlinhanchao/echo-service/internal/providers"
)

func TestProviderMetadata(t *testing.T) {
	provider := New([]string{"http://127.0.0.1:1", "http://127.0.0.1:2/"}, 1024)

	if provider.ID() != "zlib" {
		t.Errorf("ID = %q", provider.ID())
	}
	if provider.DisplayName() != "Z-Library" {
		t.Errorf("DisplayName = %q", provider.DisplayName())
	}
	if got := strings.Join(provider.Capabilities(), ","); got != "search,download,login,cookies,quota" {
		t.Errorf("Capabilities = %q", got)
	}

	info := provider.Info()
	if info.ID != "zlib" || info.Name != "Z-Library" {
		t.Errorf("Info = %+v", info)
	}
	if info.DefaultDomain != "http://127.0.0.1:1" {
		t.Errorf("DefaultDomain = %q", info.DefaultDomain)
	}
	if got := strings.Join(info.Domains, ","); got != "http://127.0.0.1:1,http://127.0.0.1:2" {
		t.Errorf("Domains = %q", got)
	}
	if got := strings.Join(info.CredentialKeys, ","); got != "domain,remix_userid,remix_userkey" {
		t.Errorf("CredentialKeys = %q", got)
	}

	// The InfoProvider contract used by the registry must see our metadata.
	if described := providers.Describe(provider); described.ID != "zlib" || described.DefaultDomain != info.DefaultDomain {
		t.Errorf("Describe = %+v", described)
	}
}

func TestProviderLogin(t *testing.T) {
	var gotEmail, gotPassword string
	captured := make(chan struct{}, 1)
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != eapiLoginPath {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = r.ParseForm()
		gotEmail = r.PostForm.Get("email")
		gotPassword = r.PostForm.Get("password")
		captured <- struct{}{}
		writeJSON(w, `{"success":1,"user":{"id":9001,"remix_userkey":"LOGIN-KEY"}}`)
	})

	provider := New([]string{server.URL}, 0)
	creds, err := provider.Login(context.Background(), server.URL, "reader@example.com", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	<-captured
	if gotEmail != "reader@example.com" || gotPassword != "secret" {
		t.Errorf("submitted email/password = %q/%q", gotEmail, gotPassword)
	}
	want := map[string]string{
		"domain":        server.URL,
		"remix_userid":  "9001",
		"remix_userkey": "LOGIN-KEY",
	}
	for key, value := range want {
		if creds[key] != value {
			t.Errorf("credentials[%q] = %q, want %q", key, creds[key], value)
		}
	}
}

func TestProviderLoginFailure(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"success":0,"error":"Incorrect email or password"}`)
	})
	provider := New([]string{server.URL}, 0)
	_, err := provider.Login(context.Background(), server.URL, "reader@example.com", "wrong")
	if err == nil {
		t.Fatal("Login succeeded, want error")
	}
	if !strings.Contains(err.Error(), "Incorrect email or password") {
		t.Fatalf("error = %v, want the server message", err)
	}
}

func TestProviderVerifyCookies(t *testing.T) {
	captured := make(chan http.Header, 1)
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != eapiProfilePath {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		captured <- r.Header.Clone()
		writeJSON(w, `{"success":1,"user":{"id":42,"email":"reader@example.com",
			"downloads_today":3,"downloads_limit":10,"isPremium":true}}`)
	})

	provider := New([]string{server.URL}, 0)
	info, err := provider.VerifyCookies(context.Background(), server.URL, "42", "FAKE-KEY")
	if err != nil {
		t.Fatalf("VerifyCookies: %v", err)
	}
	headers := <-captured
	if headers.Get("remix-userid") != "42" || headers.Get("remix-userkey") != "FAKE-KEY" {
		t.Errorf("remix headers = %q/%q", headers.Get("remix-userid"), headers.Get("remix-userkey"))
	}
	if cookie, err := (&http.Request{Header: headers}).Cookie("remix_userkey"); err != nil || cookie.Value != "FAKE-KEY" {
		t.Errorf("remix_userkey cookie = %v/%v", cookie, err)
	}

	if info.Email != "reader@example.com" {
		t.Errorf("Email = %q", info.Email)
	}
	if info.DailyUsed != 3 || info.DailyLimit != 10 || info.DailyRemaining != 7 {
		t.Errorf("quota = %+v", info)
	}
	if !info.Premium || !info.QuotaKnown {
		t.Errorf("premium/quota flags = %+v", info)
	}

	// Verify goes through the same profile endpoint with the credential bag.
	info, err = provider.Verify(context.Background(), testCredentials(server.URL))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if info.DailyRemaining != 7 {
		t.Errorf("Verify quota = %+v", info)
	}

	if _, err := provider.Verify(context.Background(), providers.Credentials{"domain": server.URL}); err == nil {
		t.Fatal("Verify without remix credentials succeeded")
	} else {
		requireErrorIs(t, err, providers.ErrNotConfigured)
	}
}

func TestProviderSearch(t *testing.T) {
	captured := make(chan capturedRequest, 1)
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != eapiSearchPath {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = r.ParseForm()
		captured <- capturedRequest{form: r.PostForm.Encode()}
		writeJSON(w, `{
			"success": 1,
			"books": [
				{
					"id": 101,
					"hash": "hash-a",
					"title": "The Dispossessed",
					"author": "Ursula K. Le Guin, Kim Stanley Robinson",
					"year": 1974,
					"publisher": "Harper",
					"language": "english",
					"extension": "EPUB",
					"filesize": 12345,
					"identifier": "9780441478125",
					"cover": "/img/cover-a.jpg",
					"href": "/book/101/hash-a",
					"description": "A novel."
				},
				{
					"id": "202",
					"hash": "hash-b",
					"title": "Second Book",
					"author": "Single Author",
					"year": "2001",
					"language": "chinese",
					"extension": "pdf",
					"filesizeString": "2 MB",
					"isbn": "B000ASIN",
					"cover": "https://cdn.example/cover-b.jpg"
				}
			],
			"pagination": {"current": 2, "total_pages": 9}
		}`)
	})

	provider := New([]string{server.URL}, 0)
	result, err := provider.Search(context.Background(), testCredentials(server.URL), providers.SearchQuery{
		Q:          "dispossessed",
		Page:       2,
		Limit:      20,
		Extensions: []string{"EPUB"},
		Languages:  []string{"English"},
		YearFrom:   1900,
		YearTo:     2000,
		Order:      "popular",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	form := (<-captured).form
	for _, want := range []string{"message=dispossessed", "page=2", "limit=20", "yearFrom=1900", "yearTo=2000", "extensions%5B%5D=epub", "languages%5B%5D=english", "order=popular"} {
		if !strings.Contains(form, want) {
			t.Errorf("form %q is missing %q", form, want)
		}
	}

	if result.Page != 2 || result.TotalPages != 9 {
		t.Errorf("pagination = page %d of %d", result.Page, result.TotalPages)
	}
	if len(result.Books) != 2 {
		t.Fatalf("got %d books", len(result.Books))
	}

	first := result.Books[0]
	if first.Provider != "zlib" || first.Ref != "101:hash-a" {
		t.Errorf("first provider/ref = %q/%q", first.Provider, first.Ref)
	}
	if first.Title != "The Dispossessed" {
		t.Errorf("title = %q", first.Title)
	}
	if got := strings.Join(first.Authors, "|"); got != "Ursula K. Le Guin|Kim Stanley Robinson" {
		t.Errorf("authors = %q", got)
	}
	if first.Year != "1974" || first.Publisher != "Harper" || first.Language != "english" {
		t.Errorf("metadata = %+v", first)
	}
	if first.Format != "epub" || first.Size != "12345" || first.ISBN != "9780441478125" {
		t.Errorf("format/size/isbn = %q/%q/%q", first.Format, first.Size, first.ISBN)
	}
	if first.CoverURL != server.URL+"/img/cover-a.jpg" {
		t.Errorf("relative cover = %q", first.CoverURL)
	}
	if first.SourceURL != server.URL+"/book/101/hash-a" {
		t.Errorf("source URL = %q", first.SourceURL)
	}
	if first.Description != "A novel." {
		t.Errorf("description = %q", first.Description)
	}

	second := result.Books[1]
	if second.Ref != "202:hash-b" {
		t.Errorf("second ref = %q", second.Ref)
	}
	if second.Year != "2001" || second.Format != "pdf" || second.Size != "2 MB" || second.ISBN != "B000ASIN" {
		t.Errorf("second metadata = %+v", second)
	}
	if second.CoverURL != "https://cdn.example/cover-b.jpg" {
		t.Errorf("absolute cover = %q", second.CoverURL)
	}
	if second.SourceURL != server.URL+"/book/202/hash-b" {
		t.Errorf("constructed source URL = %q", second.SourceURL)
	}
}

func TestProviderSearchPaginationFallback(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"success":1,"books":[],"pagination":{"current":0,"total_pages":4}}`)
	})
	provider := New([]string{server.URL}, 0)
	result, err := provider.Search(context.Background(), nil, providers.SearchQuery{Q: "anything", Page: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Page != 3 || result.TotalPages != 4 {
		t.Fatalf("pagination = page %d of %d", result.Page, result.TotalPages)
	}
	if result.Books == nil {
		t.Fatal("Books is nil, want an empty slice")
	}
}

func TestProviderGetBook(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/eapi/book/101/hash-a" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		writeJSON(w, `{"success":1,"book":{
			"id":101,"hash":"hash-a","title":"The Dispossessed","author":"Ursula K. Le Guin",
			"year":"1974","publisher":"Harper","language":"english","extension":"epub",
			"filesizeString":"1.5 MB","identifier":"9780441478125","cover":"/img/cover-a.jpg",
			"href":"/book/101/hash-a","description":"A novel."}}`)
	})

	provider := New([]string{server.URL}, 0)
	book, err := provider.GetBook(context.Background(), testCredentials(server.URL), "101:hash-a")
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if book.Ref != "101:hash-a" || book.Title != "The Dispossessed" {
		t.Errorf("book = %+v", book)
	}
	if got := strings.Join(book.Authors, "|"); got != "Ursula K. Le Guin" {
		t.Errorf("authors = %q", got)
	}
	if book.Format != "epub" || book.Size != "1.5 MB" || book.ISBN != "9780441478125" {
		t.Errorf("format/size/isbn = %q/%q/%q", book.Format, book.Size, book.ISBN)
	}
	if book.CoverURL != server.URL+"/img/cover-a.jpg" || book.SourceURL != server.URL+"/book/101/hash-a" {
		t.Errorf("urls = %q/%q", book.CoverURL, book.SourceURL)
	}

	if _, err := provider.GetBook(context.Background(), nil, ""); err == nil {
		t.Fatal("GetBook with an empty reference succeeded")
	}
}

func TestProviderDownloadFromCDN(t *testing.T) {
	content := []byte("EPUB-BOOK-BYTES-" + strings.Repeat("x", 64))
	wantHash := sha256.Sum256(content)

	cdnHeaders := make(chan http.Header, 1)
	mirrorHeaders := make(chan http.Header, 1)

	cdn := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		cdnHeaders <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/epub+zip")
		w.Header().Set("Content-Disposition", `attachment; filename="../../evil book.epub"`)
		_, _ = w.Write(content)
	})
	mirror := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eapi/book/101/hash-a/file":
			mirrorHeaders <- r.Header.Clone()
			writeJSON(w, fmt.Sprintf(`{"success":1,"file":{"downloadLink":%q}}`, cdn.URL+"/download/abc"))
		default:
			http.NotFound(w, r)
		}
	})

	provider := New([]string{mirror.URL}, 0)
	dest := t.TempDir()
	var progressWritten, progressTotal int64
	result, err := provider.Download(context.Background(), testCredentials(mirror.URL), "101:hash-a", dest, func(written, total int64) {
		progressWritten, progressTotal = written, total
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	mirrorRequest := <-mirrorHeaders
	if mirrorRequest.Get("Cookie") == "" || !strings.Contains(mirrorRequest.Get("Cookie"), "remix_userkey=FAKE-KEY") {
		t.Errorf("mirror request is missing the session cookie: %q", mirrorRequest.Get("Cookie"))
	}
	cdnRequest := <-cdnHeaders
	for _, header := range []string{"Cookie", "remix-userid", "remix-userkey", "remix_userid", "remix_userkey"} {
		if value := cdnRequest.Get(header); value != "" {
			t.Errorf("CDN request leaked %s: %q", header, value)
		}
	}

	if result.Filename != "evil book.epub" {
		t.Errorf("Filename = %q", result.Filename)
	}
	if result.Path != filepath.Join(dest, "evil book.epub") {
		t.Errorf("Path = %q", result.Path)
	}
	if result.Format != "epub" {
		t.Errorf("Format = %q", result.Format)
	}
	if result.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", result.Size, len(content))
	}
	if result.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Errorf("SHA256 = %q", result.SHA256)
	}
	saved, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(saved) != string(content) {
		t.Errorf("saved content differs")
	}
	if progressWritten != int64(len(content)) || progressTotal != int64(len(content)) {
		t.Errorf("final progress = %d/%d", progressWritten, progressTotal)
	}

	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatalf("read dest dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "evil book.epub" {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("dest dir entries = %v, want only the finished file", names)
	}
}

func TestProviderDownloadFallbackFilename(t *testing.T) {
	content := []byte("%PDF-1.7 fake pdf")
	cdn := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(content)
	})
	mirror := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/eapi/book/202/hash-b/file" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, fmt.Sprintf(`{"success":1,"downloadLink":%q}`, cdn.URL+"/dl/xyz"))
	})

	provider := New([]string{mirror.URL}, 0)
	result, err := provider.Download(context.Background(), testCredentials(mirror.URL), "202:hash-b", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if result.Filename != "202_hash-b.pdf" {
		t.Errorf("Filename = %q", result.Filename)
	}
	if result.Format != "pdf" {
		t.Errorf("Format = %q", result.Format)
	}
}

func TestProviderDownloadSizeLimit(t *testing.T) {
	content := strings.Repeat("z", 256)
	cdn := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = io.WriteString(w, content)
	})
	mirror := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, fmt.Sprintf(`{"success":1,"file":{"downloadLink":%q}}`, cdn.URL+"/big"))
	})

	provider := New([]string{mirror.URL}, 64)
	dest := t.TempDir()
	_, err := provider.Download(context.Background(), testCredentials(mirror.URL), "303:hash-c", dest, nil)
	if err == nil {
		t.Fatal("Download succeeded, want a size-limit error")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("error = %v, want a size-limit message", err)
	}
	entries, readErr := os.ReadDir(dest)
	if readErr != nil {
		t.Fatalf("read dest dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("destination contains leftovers: %v", entries)
	}
}

func TestProviderSessionExpired(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"success":0,"error":"Invalid credentials"}`)
	})
	provider := New([]string{server.URL}, 0)
	_, err := provider.Search(context.Background(), testCredentials(server.URL), providers.SearchQuery{Q: "dune"})
	requireErrorIs(t, err, providers.ErrSessionExpired)
}

func TestProviderBlocked(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<!doctype html><html><body>DiamWall challenge</body></html>`)
	})
	provider := New([]string{server.URL}, 0)
	_, err := provider.Search(context.Background(), testCredentials(server.URL), providers.SearchQuery{Q: "dune"})
	requireErrorIs(t, err, providers.ErrBlocked)
}

func TestProviderQuotaExceeded(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"success":0,"error":"daily limit reached"}`)
	})
	provider := New([]string{server.URL}, 0)
	_, err := provider.Search(context.Background(), testCredentials(server.URL), providers.SearchQuery{Q: "dune"})
	requireErrorIs(t, err, providers.ErrQuotaExceeded)
}

func TestProviderDownloadRetry(t *testing.T) {
	original := downloadRetryBase
	downloadRetryBase = time.Millisecond
	t.Cleanup(func() { downloadRetryBase = original })

	var hits int32
	content := []byte("%PDF-1.7 retried")
	cdn := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(content)
	})
	mirror := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, fmt.Sprintf(`{"success":1,"file":{"downloadLink":%q}}`, cdn.URL+"/retry"))
	})

	provider := New([]string{mirror.URL}, 0)
	result, err := provider.Download(context.Background(), testCredentials(mirror.URL), "404:hash-d", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("CDN requests = %d, want 2", got)
	}
	saved, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(saved) != string(content) {
		t.Errorf("saved content = %q", saved)
	}
}

func TestProviderDomainFailover(t *testing.T) {
	var badHits, goodHits int32

	bad := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&badHits, 1)
		if r.URL.Path == eapiDomainsInfoPath {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "<html>challenge</html>")
	})
	good := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == eapiDomainsInfoPath {
			writeJSON(w, `{"success":1,"domains":["good.example"]}`)
			return
		}
		atomic.AddInt32(&goodHits, 1)
		if r.URL.Path != eapiSearchPath {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, `{"success":1,"books":[{"id":1,"hash":"h","title":"Mirror Book","author":"A Writer","extension":"epub"}],"pagination":{"current":1,"total_pages":1}}`)
	})

	provider := New([]string{bad.URL, good.URL}, 0)
	first, err := provider.Search(context.Background(), nil, providers.SearchQuery{Q: "mirror"})
	if err != nil {
		t.Fatalf("Search after failover: %v", err)
	}
	if len(first.Books) != 1 || first.Books[0].Title != "Mirror Book" {
		t.Fatalf("results = %+v", first.Books)
	}
	if atomic.LoadInt32(&badHits) != 1 {
		t.Errorf("bad mirror requests = %d, want 1", badHits)
	}
	if atomic.LoadInt32(&goodHits) != 1 {
		t.Errorf("good mirror requests = %d, want 1", goodHits)
	}

	// The healthy mirror is cached for ten minutes and should now be preferred.
	if _, err := provider.Search(context.Background(), nil, providers.SearchQuery{Q: "mirror again"}); err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if atomic.LoadInt32(&badHits) != 1 {
		t.Errorf("bad mirror was retried on the second search: %d requests", badHits)
	}

	if !provider.ProbeDomain(context.Background(), good.URL) {
		t.Error("ProbeDomain(good) = false")
	}
	if provider.ProbeDomain(context.Background(), bad.URL) {
		t.Error("ProbeDomain(bad) = true")
	}
}
