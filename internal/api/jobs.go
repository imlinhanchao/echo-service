package api

import (
	"net/http"
)

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	limit := clampInt(atoi(r.URL.Query().Get("limit"), 20), 1, 100)
	offset := atoi(r.URL.Query().Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	jobsList, total, err := s.repos.Jobs.List(r.Context(), user.ID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "读取任务失败")
		return
	}
	out := make([]jobView, 0, len(jobsList))
	for i := range jobsList {
		out = append(out, jobToView(&jobsList[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out, "total": total})
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	id := uint64(atoi(r.PathValue("id"), 0))
	job, err := s.repos.Jobs.ByID(r.Context(), user.ID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "JOB_NOT_FOUND", "任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": jobToView(job)})
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	id := uint64(atoi(r.PathValue("id"), 0))
	job, err := s.jobs.Cancel(r.Context(), user.ID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "JOB_NOT_FOUND", "任务不存在或无法取消")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": jobToView(job)})
}
