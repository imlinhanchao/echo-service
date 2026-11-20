package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/imlinhanchao/echo-service/internal/providers"
)

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	q := r.URL.Query()
	providerID := q.Get("provider")
	if providerID == "" {
		providerID = "zlib"
	}
	p, ok := s.registry.Get(providerID)
	if !ok {
		writeError(w, http.StatusNotFound, "PROVIDER_NOT_FOUND", "未知图书源")
		return
	}
	query := strings.TrimSpace(q.Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY", "请输入搜索关键词")
		return
	}
	sq := providers.SearchQuery{
		Q:          query,
		Page:       clampInt(atoi(q.Get("page"), 1), 1, 1000),
		Limit:      clampInt(atoi(q.Get("limit"), 20), 1, 50),
		Extensions: splitCSV(q.Get("ext")),
		Languages:  splitCSV(q.Get("lang")),
		YearFrom:   atoi(q.Get("year_from"), 0),
		YearTo:     atoi(q.Get("year_to"), 0),
		Order:      strings.TrimSpace(q.Get("order")),
	}
	creds, err := s.loadCredentials(r.Context(), user.ID, providerID)
	if err != nil {
		mapProviderError(w, err)
		return
	}
	res, err := p.Search(r.Context(), creds, sq)
	if err != nil {
		mapProviderError(w, err)
		return
	}
	books := res.Books
	if books == nil {
		books = []providers.Book{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"books":       books,
		"page":        res.Page,
		"total_pages": res.TotalPages,
		"provider":    providerID,
	})
}

func (s *Server) handleBookDetail(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	providerID := r.PathValue("id")
	ref := r.PathValue("ref")
	p, ok := s.registry.Get(providerID)
	if !ok {
		writeError(w, http.StatusNotFound, "PROVIDER_NOT_FOUND", "未知图书源")
		return
	}
	if strings.TrimSpace(ref) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REF", "缺少图书引用 id:hash")
		return
	}
	creds, err := s.loadCredentials(r.Context(), user.ID, providerID)
	if err != nil {
		mapProviderError(w, err)
		return
	}
	book, err := p.GetBook(r.Context(), creds, ref)
	if err != nil {
		mapProviderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"book": book})
}

func atoi(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
