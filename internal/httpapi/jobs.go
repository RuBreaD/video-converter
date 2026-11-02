package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"videojobs/internal/jobs"
)

func (h *Handler) list(w http.ResponseWriter, r *http.Request) { reply(w, 200, h.service.List()) }

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	j, ok := h.service.Get(r.PathValue("id"))
	if !ok {
		apiError(w, 404, "job not found")
		return
	}
	reply(w, 200, j)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	j, err := h.service.Cancel(r.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, jobs.ErrNotFound):
			apiError(w, 404, err.Error())
		case errors.Is(err, jobs.ErrFinished):
			apiError(w, 409, err.Error())
		default:
			apiError(w, 500, "cannot persist cancellation")
		}
		return
	}
	reply(w, 200, j)
}

func (h *Handler) result(w http.ResponseWriter, r *http.Request) {
	j, ok := h.service.Get(r.PathValue("id"))
	if !ok {
		apiError(w, 404, "job not found")
		return
	}
	if j.Status != jobs.Completed {
		apiError(w, 409, "result is available only for completed jobs")
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", j.ID+".mp4"))
	http.ServeFile(w, r, j.OutputFile)
}
